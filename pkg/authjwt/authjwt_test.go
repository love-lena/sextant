package authjwt

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func writeCA(t *testing.T, dir string) (privPath, pubPath string) {
	t.Helper()
	privPEM, pubPEM, err := GenerateCA()
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	privPath = filepath.Join(dir, "ca.key")
	pubPath = filepath.Join(dir, "ca.pub")
	if err := os.WriteFile(privPath, privPEM, 0o600); err != nil {
		t.Fatalf("write priv: %v", err)
	}
	if err := os.WriteFile(pubPath, pubPEM, 0o644); err != nil { //nolint:gosec // ca.pub is world-readable by design
		t.Fatalf("write pub: %v", err)
	}
	return privPath, pubPath
}

func TestGenerateAndLoadCA(t *testing.T) {
	dir := t.TempDir()
	privPath, pubPath := writeCA(t, dir)
	ca, err := LoadCA(privPath, pubPath)
	if err != nil {
		t.Fatalf("LoadCA: %v", err)
	}
	if ca.PublicKey() == nil {
		t.Fatal("PublicKey nil")
	}
	if len(ca.PublicKey()) != 32 {
		t.Fatalf("PublicKey len = %d, want 32", len(ca.PublicKey()))
	}
}

func TestIssueAndVerifyRoundtrip(t *testing.T) {
	dir := t.TempDir()
	privPath, pubPath := writeCA(t, dir)
	ca, err := LoadCA(privPath, pubPath)
	if err != nil {
		t.Fatalf("LoadCA: %v", err)
	}
	agent := uuid.New()
	inc := uuid.New()
	now := time.Now().UTC()
	in := Claims{
		AgentUUID:     agent,
		IncarnationID: inc,
		Capabilities:  []string{"control.prompt", "read.agents"},
		IssuedAt:      now,
		ExpiresAt:     now.Add(1 * time.Hour),
		Issuer:        "sextantd@test",
	}
	tok, err := ca.Issue(in)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if !strings.Contains(tok, ".") {
		t.Fatalf("token does not look like a JWT: %q", tok)
	}
	out, err := ca.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if out.AgentUUID != agent {
		t.Errorf("AgentUUID = %s, want %s", out.AgentUUID, agent)
	}
	if out.IncarnationID != inc {
		t.Errorf("IncarnationID = %s, want %s", out.IncarnationID, inc)
	}
	if len(out.Capabilities) != 2 || out.Capabilities[0] != "control.prompt" {
		t.Errorf("Capabilities = %v", out.Capabilities)
	}
	if out.Issuer != "sextantd@test" {
		t.Errorf("Issuer = %s", out.Issuer)
	}
}

func TestVerifyRejectsTamperedToken(t *testing.T) {
	dir := t.TempDir()
	privPath, pubPath := writeCA(t, dir)
	ca, err := LoadCA(privPath, pubPath)
	if err != nil {
		t.Fatalf("LoadCA: %v", err)
	}
	now := time.Now().UTC()
	tok, err := ca.Issue(Claims{
		AgentUUID:     uuid.New(),
		IncarnationID: uuid.New(),
		IssuedAt:      now,
		ExpiresAt:     now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	// Flip a character inside the payload section.
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("not 3 parts: %v", parts)
	}
	if parts[1][0] == 'A' {
		parts[1] = "B" + parts[1][1:]
	} else {
		parts[1] = "A" + parts[1][1:]
	}
	tampered := strings.Join(parts, ".")
	if _, err := ca.Verify(tampered); err == nil {
		t.Fatal("expected Verify to reject tampered token")
	} else if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("error not wrapping ErrInvalidToken: %v", err)
	}
}

func TestVerifyRejectsExpiredToken(t *testing.T) {
	dir := t.TempDir()
	privPath, pubPath := writeCA(t, dir)
	ca, err := LoadCA(privPath, pubPath)
	if err != nil {
		t.Fatalf("LoadCA: %v", err)
	}
	now := time.Now().UTC()
	tok, err := ca.Issue(Claims{
		AgentUUID:     uuid.New(),
		IncarnationID: uuid.New(),
		IssuedAt:      now.Add(-2 * time.Hour),
		ExpiresAt:     now.Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := ca.Verify(tok); err == nil {
		t.Fatal("expected Verify to reject expired token")
	} else if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("error not wrapping ErrInvalidToken: %v", err)
	}
}

func TestVerifyRejectsForeignSignature(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	privA, pubA := writeCA(t, a)
	_, pubB := writeCA(t, b)

	caA, err := LoadCA(privA, pubA)
	if err != nil {
		t.Fatalf("LoadCA(A): %v", err)
	}
	// Mismatch the verifier: priv from A, pub from B.
	if _, err := LoadCA(privA, pubB); err == nil {
		t.Fatal("expected LoadCA to reject mismatched pub/priv")
	}

	now := time.Now().UTC()
	tok, err := caA.Issue(Claims{
		AgentUUID:     uuid.New(),
		IncarnationID: uuid.New(),
		IssuedAt:      now,
		ExpiresAt:     now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("Issue(A): %v", err)
	}
	// Build a CA from B's matching pair to verify the foreign token.
	privBPEM, pubBPEM, err := GenerateCA()
	if err != nil {
		t.Fatalf("GenerateCA(B): %v", err)
	}
	privBPath := filepath.Join(b, "ca2.key")
	pubBPath := filepath.Join(b, "ca2.pub")
	if err := os.WriteFile(privBPath, privBPEM, 0o600); err != nil {
		t.Fatalf("write privB: %v", err)
	}
	if err := os.WriteFile(pubBPath, pubBPEM, 0o644); err != nil { //nolint:gosec // ca.pub is world-readable by design
		t.Fatalf("write pubB: %v", err)
	}
	caB, err := LoadCA(privBPath, pubBPath)
	if err != nil {
		t.Fatalf("LoadCA(B): %v", err)
	}
	if _, err := caB.Verify(tok); err == nil {
		t.Fatal("expected B to reject A's token")
	} else if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("error not wrapping ErrInvalidToken: %v", err)
	}
}

func TestLoadCAReportsMissing(t *testing.T) {
	_, err := LoadCA("/nonexistent/priv", "/nonexistent/pub")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrCAKeyMissing) {
		t.Fatalf("error not wrapping ErrCAKeyMissing: %v", err)
	}
}

func TestIssueRequiresClaims(t *testing.T) {
	dir := t.TempDir()
	privPath, pubPath := writeCA(t, dir)
	ca, err := LoadCA(privPath, pubPath)
	if err != nil {
		t.Fatalf("LoadCA: %v", err)
	}
	cases := []struct {
		name   string
		claims Claims
	}{
		{
			name:   "missing agent",
			claims: Claims{IncarnationID: uuid.New(), IssuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)},
		},
		{
			name:   "missing incarnation",
			claims: Claims{AgentUUID: uuid.New(), IssuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)},
		},
		{
			name: "exp not after iat",
			claims: Claims{
				AgentUUID: uuid.New(), IncarnationID: uuid.New(),
				IssuedAt: time.Now(), ExpiresAt: time.Now().Add(-time.Hour),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ca.Issue(tc.claims); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}
