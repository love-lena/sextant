package clickhouseboot

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

// clickhousePath skips when no `clickhouse` binary is available.
func clickhousePath(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("clickhouse")
	if err != nil {
		t.Skipf("clickhouse not on PATH: %v", err)
	}
	return p
}

func TestStartApplyAndStop(t *testing.T) {
	bin := clickhousePath(t)
	dir := t.TempDir()
	cfg := DefaultConfig(filepath.Join(dir, "ch"))
	cfg.ClickHouseBinary = bin

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	srv, err := Start(ctx, cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })

	conn, err := srv.Open(ctx)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if err := Apply(ctx, conn); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// Idempotency.
	if err := Apply(ctx, conn); err != nil {
		t.Fatalf("Apply (second call): %v", err)
	}

	// Verify the expected tables exist.
	tables := []string{
		"sextant_migrations",
		"events",
		"audit",
		"telemetry_traces",
		"telemetry_metrics",
		"telemetry_logs",
		"agent_definitions_history",
	}
	for _, name := range tables {
		rows, err := conn.Query(ctx, "EXISTS TABLE "+name)
		if err != nil {
			t.Fatalf("EXISTS TABLE %s: %v", name, err)
		}
		if !rows.Next() {
			_ = rows.Close()
			t.Fatalf("EXISTS TABLE %s: no row", name)
		}
		var exists uint8
		if err := rows.Scan(&exists); err != nil {
			_ = rows.Close()
			t.Fatalf("scan %s: %v", name, err)
		}
		_ = rows.Close()
		if exists != 1 {
			t.Fatalf("table %s missing", name)
		}
	}
}

func TestRoundtripInsertQuery(t *testing.T) {
	bin := clickhousePath(t)
	dir := t.TempDir()
	cfg := DefaultConfig(filepath.Join(dir, "ch"))
	cfg.ClickHouseBinary = bin

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	srv, err := Start(ctx, cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })

	conn, err := srv.Open(ctx)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if err := Apply(ctx, conn); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	id := uuid.New()
	traceID := uuid.New()
	spanID := uuid.New()
	now := time.Now().UTC()

	if err := conn.Exec(ctx,
		`INSERT INTO events (id, ts, subject, from_kind, from_id, to_kind, to_id,
			trace_id, span_id, parent_span_id, kind, proto_version, payload,
			idempotency_key, reply_to)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, now, "agents.test.frames", "agent", id.String(), "ui", "",
		traceID, spanID, uuid.Nil, "agent_frame", "1.0",
		`{"frame_kind":"assistant_text"}`, "", ""); err != nil {
		t.Fatalf("INSERT: %v", err)
	}

	rows, err := conn.Query(ctx, `SELECT subject, kind FROM events WHERE id = ?`, id)
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	defer func() { _ = rows.Close() }()

	if !rows.Next() {
		t.Fatalf("no row found for id %s", id)
	}
	var subject, kind string
	if err := rows.Scan(&subject, &kind); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if subject != "agents.test.frames" || kind != "agent_frame" {
		t.Fatalf("row mismatch: subject=%q kind=%q", subject, kind)
	}
}

func TestLoadMigrationsFromDisk(t *testing.T) {
	migs, err := LoadMigrations()
	if err != nil {
		t.Fatalf("LoadMigrations: %v", err)
	}
	if len(migs) < 6 {
		t.Fatalf("expected >= 6 migrations, got %d", len(migs))
	}
	// Versions are strictly ascending.
	for i := 1; i < len(migs); i++ {
		if migs[i].Version <= migs[i-1].Version {
			t.Fatalf("migrations not ascending at index %d: %d -> %d",
				i, migs[i-1].Version, migs[i].Version)
		}
	}
	// SHA256 is filled.
	for _, m := range migs {
		if m.SHA256 == "" {
			t.Fatalf("migration %d has empty sha256", m.Version)
		}
	}
}

func TestConfigRejectsBindRoutable(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.ListenHost = "0.0.0.0"
	if _, err := cfg.validateAndFill(); err == nil {
		t.Fatal("expected validateAndFill to reject 0.0.0.0")
	}
}

func TestSplitStatementsCorrectlyHandlesCommentsAndQuotes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "two simple",
			in:   "SELECT 1; SELECT 2;",
			want: []string{"SELECT 1", " SELECT 2"},
		},
		{
			name: "semicolon in string",
			in:   "INSERT INTO t VALUES ('hello; world'); SELECT 1;",
			want: []string{"INSERT INTO t VALUES ('hello; world')", " SELECT 1"},
		},
		{
			name: "line comment",
			in:   "-- a comment with ;\nSELECT 1;",
			want: []string{"-- a comment with ;\nSELECT 1"},
		},
		{
			name: "trailing text after last semicolon",
			in:   "SELECT 1; trailing",
			want: []string{"SELECT 1", " trailing"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitStatements(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("len mismatch: got %d (%q), want %d (%q)", len(got), got, len(tc.want), tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("stmt %d: got %q want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}
