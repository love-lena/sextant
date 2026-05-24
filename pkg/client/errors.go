package client

import "errors"

// ErrKVKeyNotFound is returned by GetKV when the requested key does not
// exist in the bucket. Callers should use errors.Is to test.
var ErrKVKeyNotFound = errors.New("client: kv key not found")

// ErrClosed is returned by methods called after Close.
var ErrClosed = errors.New("client: closed")
