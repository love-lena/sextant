package main

import (
	"fmt"
	"io"
)

// printf writes to w discarding the return values. We use this from
// progress-reporting paths where a failed Stdout write does not signal a
// meaningful error condition — the parent shell already gives up on
// piped readers. Centralizing the discard so errcheck stays clean.
func printf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

func println(w io.Writer, args ...any) {
	_, _ = fmt.Fprintln(w, args...)
}
