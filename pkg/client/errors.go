package client

import "errors"

// ErrNotImplementedYet is returned by API methods whose milestone has
// not landed yet. The error message names the milestone so callers can
// trace it back.
//
// In M4 this is returned by Query (see specs/components/client-libraries.md
// "Milestone scoping (Go)") and is the load-bearing reason Query must not
// silently return an empty slice.
var ErrNotImplementedYet = errors.New("client: not implemented yet, lands in M7 (see plans/bootstrap.md#M7)")

// ErrKVKeyNotFound is returned by GetKV when the requested key does not
// exist in the bucket. Callers should use errors.Is to test.
var ErrKVKeyNotFound = errors.New("client: kv key not found")

// ErrClosed is returned by methods called after Close.
var ErrClosed = errors.New("client: closed")
