package main

import "github.com/love-lena/sextant-initial/pkg/authjwt"

// generateCAForTest wraps authjwt.GenerateCA so sextantd_test.go can
// build a CA pair without importing cmd/sextant (cmd packages aren't
// importable).
func generateCAForTest() (priv, pub []byte, err error) {
	return authjwt.GenerateCA()
}
