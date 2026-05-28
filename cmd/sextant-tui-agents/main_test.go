package main

import "testing"

func TestSanitizeOperator(t *testing.T) {
	cases := map[string]string{
		"lena":          "lena",
		"lena.dev":      "lena_dev",
		"User Name":     "User_Name",
		"alice@example": "alice_example",
		"":              "",
	}
	for in, want := range cases {
		if got := sanitizeOperator(in); got != want {
			t.Errorf("sanitizeOperator(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveOperatorPrefersFlag(t *testing.T) {
	got, err := resolveOperator("explicit")
	if err != nil {
		t.Fatalf("resolveOperator: %v", err)
	}
	if got != "explicit" {
		t.Fatalf("got %q, want %q", got, "explicit")
	}
}

func TestResolveOperatorFallsBackToEnv(t *testing.T) {
	t.Setenv("SEXTANT_OPERATOR", "from-env")
	got, err := resolveOperator("")
	if err != nil {
		t.Fatalf("resolveOperator: %v", err)
	}
	if got != "from-env" {
		t.Fatalf("got %q, want %q", got, "from-env")
	}
}
