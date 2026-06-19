package surface

// This file exposes a minimal test-only surface for the external surface_test
// package: constructors for the internal error messages, so a golden test can
// drive each surface's error-footer path through Update without a live bus and
// without exporting the error types into the production API. It compiles only
// under test (the _test.go suffix), so nothing here reaches a built binary.

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/love-lena/sextant/clients/go/apps/internal/tui/busfeed"
)

// NewClientsErrMsg builds the presence fetch-error message a failed ListClients
// would produce, for driving the presence error footer in a golden.
func NewClientsErrMsg(err error) any { return clientsErrMsg{err: err} }

// NewArtifactErrMsg builds the artifact fetch-error message a failed GetArtifact
// would produce, for driving the artifact error footer in a golden. It is
// untagged (nil owner), which a surface treats as its own.
func NewArtifactErrMsg(err error) any { return artifactErrMsg{err: err} }

// NewArtifactsErrMsg builds the artifacts-browser fetch-error message a failed
// ListArtifacts would produce, for driving the browser's error footer in a
// golden.
func NewArtifactsErrMsg(err error) any { return artifactsErrMsg{err: err} }

// NewPublishedErrMsg builds the publish-result message a failed compose/comment
// publish would produce, for driving the stream/artifact error footer in a
// golden. A nil err is the success case (which clears the footer).
func NewPublishedErrMsg(err error) any { return publishedMsg{err: err} }

// ErrMsg re-exports busfeed.ErrMsg so a test can drive the stream's terminal
// feed-error footer without importing busfeed solely for that. It is the message
// a failed subscribe surfaces.
func NewFeedErrMsg(err error) any { return busfeed.ErrMsg{Err: err} }

// NewTopicsErrMsg builds the discovery feed-error message a terminal busfeed
// error would produce on the topics browser, for driving the discovery error
// footer in a golden. It reuses busfeed.ErrMsg directly (the topics browser
// handles it via claims+ErrMsg), untagged so the browser claims it at the list.
func NewTopicsErrMsg(err error) any { return busfeed.ErrMsg{Err: err} }

// NextChangeCmd exposes the artifact watch pump step so an integration test can
// resume reading watch changes after driving a first one to completion. It is the
// same command the surface returns on each delivered change.
func (a *Artifact) NextChangeCmd() tea.Cmd { return a.nextChange() }

// HardBreak exposes the stream's cell-width line splitter so the wrap tests can
// assert grapheme-cluster integrity (a ZWJ emoji or a combining-mark sequence at
// the break boundary moves whole) directly, without composing a full stream view.
func HardBreak(line string, width int) []string { return hardBreak(line, width) }
