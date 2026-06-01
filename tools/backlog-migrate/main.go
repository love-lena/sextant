// Command backlog-migrate is a one-shot tool that imports sextant's
// hand-rolled plans/issues/*.md tickets into Backlog.md.
//
// It parses each ticket's frontmatter + body, shells out to `backlog task
// create` with the mapped fields, restores the original created_at onto the
// generated file, and finally archives the closed tickets. Creating every
// ticket before archiving any is deliberate: Backlog.md reuses numeric IDs
// freed by archiving, so archiving mid-run would cause collisions. Slugs (not
// the numeric IDs) are the durable identity, carried as a `slug:<slug>` label.
//
// Usage:
//
//	go run ./tools/backlog-migrate \
//	    -backlog tools/backlog/node_modules/.bin/backlog \
//	    -issues plans/issues -repo .
//
// Run once against a freshly `backlog init`-ed repo; re-running duplicates.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	yaml "gopkg.in/yaml.v3"
)

type frontmatter struct {
	Title        string   `yaml:"title"`
	Status       string   `yaml:"status"`
	Priority     string   `yaml:"priority"`
	Labels       []string `yaml:"labels"`
	CreatedAt    string   `yaml:"created_at"`
	DiscoveredIn string   `yaml:"discovered_in"`
	FixedIn      string   `yaml:"fixed_in"`
	FixedAt      string   `yaml:"fixed_at"`
	ResolvedAt   string   `yaml:"resolved_at"`
	ResolvedBy   string   `yaml:"resolved_by"`
	Resolution   string   `yaml:"resolution"`
}

type ticket struct {
	slug string
	fm   frontmatter
	body string
}

func main() {
	backlogBin := flag.String("backlog", "backlog", "path to the backlog binary")
	issuesDir := flag.String("issues", "plans/issues", "directory of plans/issues/*.md tickets")
	repoRoot := flag.String("repo", ".", "repo root where backlog/ lives (backlog runs with this as cwd)")
	flag.Parse()

	root, err := filepath.Abs(*repoRoot)
	if err != nil {
		fatalf("resolve repo root: %v", err)
	}

	tickets, err := loadTickets(*issuesDir)
	if err != nil {
		fatalf("load tickets: %v", err)
	}
	fmt.Printf("loaded %d tickets from %s\n", len(tickets), *issuesDir)

	// Create everything first so IDs are unique task-1..task-N.
	created := make([]createResult, 0, len(tickets))
	var failures []string
	for _, t := range tickets {
		res, err := createTask(*backlogBin, root, t)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", t.slug, err))
			fmt.Printf("  FAIL %-50s %v\n", t.slug, err)
			continue
		}
		if err := restoreCreatedDate(res.file, t.fm.CreatedAt); err != nil {
			fmt.Printf("  warn %-50s created_date not restored: %v\n", t.slug, err)
		}
		created = append(created, res)
		fmt.Printf("  ok   %-50s -> task-%s (%s)\n", t.slug, res.id, t.fm.Status)
	}

	// Archive the closed tickets, now that all IDs are stable.
	archived := 0
	for _, res := range created {
		if !res.archive {
			continue
		}
		cmd := exec.Command(*backlogBin, "task", "archive", res.id) //nolint:gosec // G204: same as createTask — invoking the pinned backlog CLI
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			failures = append(failures, fmt.Sprintf("archive %s: %v: %s", res.slug, err, out))
			continue
		}
		archived++
	}

	report(tickets, created, archived, failures)
	if len(failures) > 0 {
		os.Exit(1)
	}
}

type createResult struct {
	slug    string
	id      string
	file    string
	archive bool
}

func createTask(bin, root string, t ticket) (createResult, error) {
	status, statusLabels, archive := mapStatus(t.fm.Status)
	prio, tier := mapPriority(t.fm.Priority)

	labels := append([]string{}, t.fm.Labels...)
	labels = append(labels, "slug:"+t.slug)
	if tier != "" {
		labels = append(labels, tier)
	}
	labels = append(labels, statusLabels...)
	labels = dedup(labels)

	args := []string{"task", "create", "-s", status}
	if t.body != "" {
		args = append(args, "-d", t.body)
	}
	if prio != "" {
		args = append(args, "--priority", prio)
	}
	if len(labels) > 0 {
		args = append(args, "-l", strings.Join(labels, ","))
	}
	if notes := provenance(t.slug, t.fm); notes != "" {
		args = append(args, "--notes", notes)
	}
	if t.fm.Resolution != "" {
		args = append(args, "--final-summary", strings.TrimSpace(t.fm.Resolution))
	}
	// Title goes last, after `--`, so titles beginning with "-" (e.g.
	// "`--help` prints empty output...") aren't parsed as flags.
	args = append(args, "--", t.fm.Title)

	cmd := exec.Command(bin, args...) //nolint:gosec // G204: invoking the pinned backlog CLI with fields mapped from local repo tickets is this tool's sole purpose
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return createResult{}, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	id := reTaskID.FindStringSubmatch(string(out))
	file := reTaskFile.FindStringSubmatch(string(out))
	if id == nil || file == nil {
		return createResult{}, fmt.Errorf("could not parse create output: %s", strings.TrimSpace(string(out)))
	}
	return createResult{slug: t.slug, id: id[1], file: strings.TrimSpace(file[1]), archive: archive}, nil
}

var (
	reTaskID   = regexp.MustCompile(`(?i)Created task TASK-(\d+)`)
	reTaskFile = regexp.MustCompile(`(?m)^File:\s*(.+)$`)
	reCreated  = regexp.MustCompile(`(?m)^created_date:.*$`)
	reLink     = regexp.MustCompile(`\[\[([^\]]+)\]\]`)
)

// restoreCreatedDate overwrites the CLI-stamped created_date with the ticket's
// original created_at, normalized to Backlog.md's "yyyy-mm-dd HH:MM" format.
func restoreCreatedDate(file, createdAt string) error {
	ts := normalizeDate(createdAt)
	if ts == "" {
		return fmt.Errorf("unparseable created_at %q", createdAt)
	}
	b, err := os.ReadFile(file) //nolint:gosec // G304: reading a backlog-generated file by the path backlog reported
	if err != nil {
		return err
	}
	if !reCreated.Match(b) {
		return fmt.Errorf("no created_date line in %s", file)
	}
	out := reCreated.ReplaceAll(b, []byte("created_date: '"+ts+"'"))
	return os.WriteFile(file, out, 0o600) //nolint:gosec // G304/G703: file is the path backlog reported for a file it just created under backlog/
}

func normalizeDate(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	layouts := []string{
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02T15:04Z07:00",
		"2006-01-02T15:04:05-07:00",
		"2006-01-02T15:04-07:00",
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t.Format("2006-01-02 15:04")
		}
	}
	return ""
}

func mapStatus(s string) (status string, labels []string, archive bool) {
	switch strings.TrimSpace(s) {
	case "open":
		return "To Do", nil, false
	case "in-progress":
		return "In Progress", nil, false
	case "fixed":
		return "Done", []string{"closed:fixed"}, true
	case "resolved":
		return "Done", []string{"closed:resolved"}, true
	case "wontfix":
		return "Done", []string{"wontfix"}, true
	case "deferred":
		return "To Do", []string{"deferred"}, false
	default:
		return "To Do", []string{"migrate:unknown-status"}, false
	}
}

func mapPriority(p string) (prio, tier string) {
	switch strings.ToUpper(strings.TrimSpace(p)) {
	case "P1":
		return "high", "P1"
	case "P2":
		return "medium", "P2"
	case "P3":
		return "low", "P3"
	default:
		return "", ""
	}
}

func provenance(slug string, fm frontmatter) string {
	var lines []string
	lines = append(lines, "Migrated from plans/issues/"+slug+".md")
	if fm.DiscoveredIn != "" {
		lines = append(lines, "Discovered in: "+fm.DiscoveredIn)
	}
	if fm.CreatedAt != "" {
		lines = append(lines, "Original created_at: "+fm.CreatedAt)
	}
	if fm.FixedIn != "" {
		if fm.FixedAt != "" {
			lines = append(lines, fmt.Sprintf("Fixed in: %s (%s)", fm.FixedIn, fm.FixedAt))
		} else {
			lines = append(lines, "Fixed in: "+fm.FixedIn)
		}
	}
	if fm.ResolvedAt != "" {
		if fm.ResolvedBy != "" {
			lines = append(lines, fmt.Sprintf("Resolved at: %s (by %s)", fm.ResolvedAt, fm.ResolvedBy))
		} else {
			lines = append(lines, "Resolved at: "+fm.ResolvedAt)
		}
	}
	return strings.Join(lines, "\n")
}

func loadTickets(dir string) ([]ticket, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var tickets []ticket
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".md") || name == "README.md" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // G304: reading local ticket markdown from the issues dir
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		fmText, body, err := splitDoc(string(b))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		var fm frontmatter
		if err := yaml.Unmarshal([]byte(sanitizeFrontmatter(fmText)), &fm); err != nil {
			return nil, fmt.Errorf("%s: parse frontmatter: %w", name, err)
		}
		tickets = append(tickets, ticket{
			slug: strings.TrimSuffix(name, ".md"),
			fm:   fm,
			body: body,
		})
	}
	// Create in chronological order so task-N roughly tracks history.
	sort.SliceStable(tickets, func(i, j int) bool {
		return tickets[i].fm.CreatedAt < tickets[j].fm.CreatedAt
	})
	return tickets, nil
}

// scalarKey matches a single-line scalar frontmatter field at column 0.
// `labels` (a list) and `resolution` (a block scalar) are intentionally absent
// so they're left for the YAML parser to handle.
var scalarKey = regexp.MustCompile(`^(title|status|priority|created_at|discovered_in|fixed_in|fixed_at|resolved_at|resolved_by):[ \t]*(.*)$`)

// sanitizeFrontmatter single-quotes the value of each single-line scalar field.
// The hand-written tickets routinely contain unquoted colons in titles and
// `discovered_in` prose (e.g. "front door: daemon ..."), which strict YAML
// rejects as mapping syntax. Values already quoted (' or ") or introducing a
// block scalar (| or >) are left untouched.
func sanitizeFrontmatter(fm string) string {
	lines := strings.Split(fm, "\n")
	for i, ln := range lines {
		m := scalarKey.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		key, val := m[1], strings.TrimRight(m[2], " \t")
		if val == "" {
			continue
		}
		switch val[0] {
		case '\'', '"', '|', '>':
			continue
		}
		lines[i] = key + ": '" + strings.ReplaceAll(val, "'", "''") + "'"
	}
	return strings.Join(lines, "\n")
}

func splitDoc(s string) (fm, body string, err error) {
	lines := strings.Split(s, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", "", fmt.Errorf("missing opening ---")
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return "", "", fmt.Errorf("missing closing ---")
	}
	fm = strings.Join(lines[1:end], "\n")
	body = strings.TrimLeft(strings.Join(lines[end+1:], "\n"), "\n")
	return fm, body, nil
}

func report(tickets []ticket, created []createResult, archived int, failures []string) {
	fmt.Println("\n=== migration report ===")
	fmt.Printf("created:  %d / %d\n", len(created), len(tickets))
	fmt.Printf("archived: %d\n", archived)

	byStatus := map[string]int{}
	byPrio := map[string]int{}
	for _, t := range tickets {
		byStatus[t.fm.Status]++
		byPrio[strings.ToUpper(t.fm.Priority)]++
	}
	fmt.Printf("source status: %v\n", byStatus)
	fmt.Printf("source priority: %v\n", byPrio)

	// Dangling-link report: [[X]] referencing a slug that was never a ticket.
	have := map[string]bool{}
	for _, t := range tickets {
		have[t.slug] = true
	}
	dangling := map[string]int{}
	for _, t := range tickets {
		for _, m := range reLink.FindAllStringSubmatch(t.body, -1) {
			ref := strings.TrimSpace(m[1])
			if !have[ref] {
				dangling[ref]++
			}
		}
	}
	if len(dangling) > 0 {
		refs := make([]string, 0, len(dangling))
		for r := range dangling {
			refs = append(refs, r)
		}
		sort.Strings(refs)
		fmt.Printf("\ndangling [[links]] (%d distinct; reference a slug that was never a ticket):\n", len(refs))
		for _, r := range refs {
			fmt.Printf("  [[%s]] x%d\n", r, dangling[r])
		}
	} else {
		fmt.Println("\nno dangling [[links]] — every reference resolves to a migrated slug")
	}

	if len(failures) > 0 {
		fmt.Printf("\nFAILURES (%d):\n", len(failures))
		for _, f := range failures {
			fmt.Printf("  %s\n", f)
		}
	}
}

func dedup(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "backlog-migrate: "+format+"\n", args...)
	os.Exit(1)
}
