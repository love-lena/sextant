package dashapi

import (
	"embed"
	"io/fs"
	"net/http"
)

// debugPage is the built-in zero-design debug surface (web/debug.html), embedded
// so a fresh build serves it with no separate asset step. It is the D1
// verification harness and the opinion-free baseline; it lives at /debug beside
// the designed UI.
//
//go:embed web/debug.html
var debugPage []byte

// appAssets is the embedded intentionally-designed UI (D2, TASK-71): the
// frontend served at / by default. A configured UIDir overrides it (dev hook).
//
// The six *.js bundles are GENERATED from their *.jsx sources by
// scripts/build-dash-ui.sh (gitignored, not committed — TASK-121). They are
// named explicitly below so a build that skipped `make ui` fails to COMPILE
// (a loud "no matching files found" error) rather than silently embedding a
// stale or missing UI. The trailing all:web/app pulls in the static assets
// (index.html, styles.css, favicon.svg, image-slot.js, vendor/).
//
//go:generate bash ../../scripts/build-dash-ui.sh
//go:embed web/app/app.js web/app/artifact.js web/app/artifacts.js web/app/home.js web/app/sidebar.js web/app/tweaks-panel.js
//go:embed all:web/app
var appAssets embed.FS

// appRoot roots the embedded app at web/app so its files serve from / (index.html
// at /, styles.css at /styles.css, and so on).
var appRoot = func() fs.FS {
	sub, err := fs.Sub(appAssets, "web/app")
	if err != nil {
		panic(err) // a build that embedded web/app cannot fail to sub it
	}
	return sub
}()

// handleRoot serves the frontend at /. With a configured UIDir it serves that
// directory (the dev override); otherwise it serves the embedded designed UI
// (D2). The zero-design debug surface lives at /debug. The assets are static and
// carry no secrets, so they are not token-gated — the token rides in the URL and
// gates the API they call.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if s.uiDir != "" {
		// Dev override: serve the live directory with no caching so a browser
		// refresh always picks up edits (hot-reload during UI iteration).
		w.Header().Set("Cache-Control", "no-store")
		http.FileServer(http.Dir(s.uiDir)).ServeHTTP(w, r)
		return
	}
	http.FileServer(http.FS(appRoot)).ServeHTTP(w, r)
}

// handleDebug serves the built-in zero-design debug surface at /debug — the D1
// verification harness, kept reachable beside the designed UI. Like the app it
// is static and not token-gated.
func (s *Server) handleDebug(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(debugPage)
}
