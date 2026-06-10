package selfenroll

// EnrollCredsPath exposes the unexported creds-path convention to the external
// test package, which pins it against pkg/bus.EnrollCredsPath (the write side).
// The production package deliberately never links pkg/bus (see the doc on
// enrollCredsPath), so the equality lives in a test: the one place both sides
// may be in the same binary.
var EnrollCredsPath = enrollCredsPath
