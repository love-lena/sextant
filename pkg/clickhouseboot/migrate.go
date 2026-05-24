package clickhouseboot

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// migrationsFS embeds every .sql file under migrations/ so the binary
// ships with the schema; no separate file copy at install time.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// MigrationsFS exposes the embedded migrations for callers that need to
// inspect or override the source (e.g. tests pointing at a custom dir).
func MigrationsFS() fs.FS { return migrationsFS }

// Migration is one sequenced SQL file.
type Migration struct {
	Version int
	Name    string
	SQL     string
	SHA256  string
}

// LoadMigrations returns the migrations baked into this binary.
func LoadMigrations() ([]Migration, error) {
	return loadMigrationsFromFS(migrationsFS, "migrations")
}

func loadMigrationsFromFS(fsys fs.FS, dir string) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}
	pattern := regexp.MustCompile(`^(\d+)-([a-z0-9-]+)\.sql$`)
	out := make([]Migration, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := pattern.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("bad migration filename %q (must match NNN-name.sql)", e.Name())
		}
		version, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, fmt.Errorf("parse version from %q: %w", e.Name(), err)
		}
		raw, err := fs.ReadFile(fsys, dir+"/"+e.Name())
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		sum := sha256.Sum256(raw)
		out = append(out, Migration{
			Version: version,
			Name:    m[2],
			SQL:     string(raw),
			SHA256:  hex.EncodeToString(sum[:]),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	// Reject duplicate versions; that would corrupt apply ordering.
	for i := 1; i < len(out); i++ {
		if out[i].Version == out[i-1].Version {
			return nil, fmt.Errorf("duplicate migration version %03d (%s and %s)",
				out[i].Version, out[i-1].Name, out[i].Name)
		}
	}
	return out, nil
}

// Apply runs every migration that has not yet been recorded in the
// sextant_migrations bookkeeping table. Re-applying an already-applied
// migration is a no-op. Mismatched SHA256 between disk and bookkeeping
// returns an error to flag tampering or accidental edits to applied
// migrations.
func Apply(ctx context.Context, conn driver.Conn) error {
	migs, err := LoadMigrations()
	if err != nil {
		return err
	}
	return apply(ctx, conn, migs)
}

func apply(ctx context.Context, conn driver.Conn, migs []Migration) error {
	if err := ensureBookkeeping(ctx, conn); err != nil {
		return err
	}
	applied, err := loadApplied(ctx, conn)
	if err != nil {
		return err
	}
	for _, m := range migs {
		prev, ok := applied[m.Version]
		switch {
		case ok && prev.sha == m.SHA256:
			// idempotent re-apply: skip
			continue
		case ok && prev.sha != m.SHA256:
			return fmt.Errorf("migration %03d (%s): on-disk SHA256 %s does not match applied %s — refusing to re-apply edited migration",
				m.Version, m.Name, m.SHA256, prev.sha)
		}
		if err := runMigrationStatements(ctx, conn, m); err != nil {
			return fmt.Errorf("migration %03d (%s): %w", m.Version, m.Name, err)
		}
		if err := recordApplied(ctx, conn, m); err != nil {
			return fmt.Errorf("record migration %03d: %w", m.Version, err)
		}
	}
	return nil
}

type appliedRow struct {
	sha string
}

func ensureBookkeeping(ctx context.Context, conn driver.Conn) error {
	const ddl = `
CREATE TABLE IF NOT EXISTS sextant_migrations (
    version UInt32,
    name    String,
    sha256  String,
    applied_at DateTime64(6) DEFAULT now64(6)
) ENGINE = MergeTree()
ORDER BY version
`
	return conn.Exec(ctx, ddl)
}

func loadApplied(ctx context.Context, conn driver.Conn) (map[int]appliedRow, error) {
	rows, err := conn.Query(ctx, `SELECT version, sha256 FROM sextant_migrations`)
	if err != nil {
		return nil, fmt.Errorf("query sextant_migrations: %w", err)
	}
	defer rows.Close() //nolint:errcheck // rows close best-effort
	out := map[int]appliedRow{}
	for rows.Next() {
		var (
			v   uint32
			sha string
		)
		if err := rows.Scan(&v, &sha); err != nil {
			return nil, fmt.Errorf("scan sextant_migrations row: %w", err)
		}
		out[int(v)] = appliedRow{sha: sha}
	}
	return out, rows.Err()
}

// runMigrationStatements splits the SQL file on bare `;` statement
// terminators and executes each non-empty statement individually.
// ClickHouse-go's Exec rejects multi-statement strings, so we cannot
// hand the whole file over verbatim.
func runMigrationStatements(ctx context.Context, conn driver.Conn, m Migration) error {
	for i, stmt := range splitStatements(m.SQL) {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if err := conn.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("statement %d: %w\n--- sql ---\n%s", i+1, err, stmt)
		}
	}
	return nil
}

// splitStatements walks the SQL file char-by-char, splitting on `;` that
// are not inside single-quote strings, double-quote identifiers, or
// `--`/`/* */` comments.
func splitStatements(s string) []string {
	var (
		out   []string
		buf   strings.Builder
		i     int
		n     = len(s)
		inSQ  bool
		inDQ  bool
		inLC  bool // line comment
		inBC  bool // block comment
		prevC byte
	)
	for i < n {
		c := s[i]
		switch {
		case inLC:
			if c == '\n' {
				inLC = false
			}
			buf.WriteByte(c)
		case inBC:
			if prevC == '*' && c == '/' {
				inBC = false
			}
			buf.WriteByte(c)
		case inSQ:
			buf.WriteByte(c)
			if c == '\'' && prevC != '\\' {
				inSQ = false
			}
		case inDQ:
			buf.WriteByte(c)
			if c == '"' && prevC != '\\' {
				inDQ = false
			}
		case c == '\'':
			inSQ = true
			buf.WriteByte(c)
		case c == '"':
			inDQ = true
			buf.WriteByte(c)
		case c == '-' && i+1 < n && s[i+1] == '-':
			inLC = true
			buf.WriteByte(c)
		case c == '/' && i+1 < n && s[i+1] == '*':
			inBC = true
			buf.WriteByte(c)
		case c == ';':
			out = append(out, buf.String())
			buf.Reset()
		default:
			buf.WriteByte(c)
		}
		prevC = c
		i++
	}
	if buf.Len() > 0 {
		out = append(out, buf.String())
	}
	return out
}

func recordApplied(ctx context.Context, conn driver.Conn, m Migration) error {
	if m.Version < 0 {
		return fmt.Errorf("negative migration version %d", m.Version)
	}
	return conn.Exec(ctx,
		`INSERT INTO sextant_migrations (version, name, sha256) VALUES (?, ?, ?)`,
		uint32(m.Version), m.Name, m.SHA256, //nolint:gosec // bounds-checked above
	)
}

// ErrNoMigrations is returned by LoadMigrations when no .sql files are
// present in the embedded dir. Likely a build misconfiguration.
var ErrNoMigrations = errors.New("clickhouseboot: no migrations found")
