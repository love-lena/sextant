package statushook

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// RecentActivity reads the tail of a Claude Code transcript (JSONL) and digests
// it into a compact, newline-separated string for the Haiku prompt: one line per
// transcript entry (oldest-to-newest of the kept window), each a role + its prose
// and any tool names. It keeps the last maxLines entries so the prompt stays small.
//
// Parsing is lenient — Claude Code's transcript schema can drift, and a status
// digest is best-effort: an unparseable line is skipped, never fatal.
func RecentActivity(transcriptPath string, maxLines int) (string, error) {
	f, err := os.Open(transcriptPath)
	if err != nil {
		return "", fmt.Errorf("statushook: open transcript: %w", err)
	}
	defer f.Close()

	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // transcript lines can be large
	for sc.Scan() {
		if t := strings.TrimSpace(sc.Text()); t != "" {
			lines = append(lines, t)
		}
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("statushook: read transcript: %w", err)
	}
	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}

	var out []string
	for _, ln := range lines {
		if s := digestEntry(ln); s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, "\n"), nil
}

// digestEntry turns one JSONL transcript line into a short "role: prose [tool: X]"
// summary, or "" if there's nothing useful. Tolerant of content being either a
// string or an array of typed blocks.
func digestEntry(line string) string {
	var e struct {
		Type    string `json:"type"`
		Message struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(line), &e); err != nil {
		return ""
	}
	role := e.Message.Role
	if role == "" {
		role = e.Type
	}

	// content: a plain string …
	var asStr string
	if json.Unmarshal(e.Message.Content, &asStr) == nil && asStr != "" {
		return role + ": " + truncate(asStr, 280)
	}
	// … or an array of blocks.
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
		Name string `json:"name"`
	}
	if json.Unmarshal(e.Message.Content, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if t := strings.TrimSpace(b.Text); t != "" {
				parts = append(parts, truncate(t, 280))
			}
		case "tool_use":
			if b.Name != "" {
				parts = append(parts, "[tool: "+b.Name+"]")
			}
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return role + ": " + strings.Join(parts, " ")
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ") // collapse whitespace/newlines
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
