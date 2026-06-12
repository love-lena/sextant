package dashapi

import (
	_ "embed"
	"net/http"
)

// debugPage is the built-in zero-design debug surface (web/debug.html), embedded
// so a fresh build serves it with no separate asset step. It is the D1
// verification harness and the opinion-free baseline for the designed UI (D2),
// which a custom --ui dir replaces wholesale.
//
//go:embed web/debug.html
var debugPage []byte

// handleRoot serves the frontend at /. With a configured UIDir it serves that
// directory (the D2 hook); otherwise it serves the built-in debug page at / and
// 404s any other path. The page is static and carries no secrets, so it is not
// token-gated — the token rides in its URL and gates the API it calls.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if s.uiDir != "" {
		http.FileServer(http.Dir(s.uiDir)).ServeHTTP(w, r)
		return
	}
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(debugPage)
}
