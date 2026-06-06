package main

import (
	"github.com/love-lena/sextant/internal/clictx"
	"github.com/love-lena/sextant/pkg/sextant"
)

// saveSelfContext records a just-self-enrolled identity as a local context: it
// writes the creds into the context store (0600), saves a context carrying the
// bus-minted identity, and makes it active. Self-enrollment is "I am now this
// identity," so it always activates — `context use` switches away. Returns the
// creds path written.
func saveSelfContext(name, kind, url string, issued sextant.IssuedClient) (string, error) {
	credsPath, err := clictx.WriteCreds(name, issued.Creds)
	if err != nil {
		return "", err
	}
	if err := clictx.Save(clictx.Context{
		Name: name, URL: url, ID: issued.ID, Display: name, Kind: kind, Creds: credsPath,
	}); err != nil {
		return "", err
	}
	if err := clictx.SetActive(name); err != nil {
		return "", err
	}
	return credsPath, nil
}
