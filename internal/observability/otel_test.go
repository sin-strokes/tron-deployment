package observability

import (
	"context"
	"testing"
	"time"
)

func TestSchemeOf(t *testing.T) {
	cases := map[string]string{
		"http://localhost:4318":    "http",
		"https://otel.example.com": "https",
		"localhost:4318":           "",
		"":                         "",
	}
	for in, want := range cases {
		if got := schemeOf(in); got != want {
			t.Errorf("schemeOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStripScheme(t *testing.T) {
	cases := map[string]string{
		"http://localhost:4318":    "localhost:4318",
		"https://otel.example.com": "otel.example.com",
		"localhost:4318":           "localhost:4318",
	}
	for in, want := range cases {
		if got := stripScheme(in); got != want {
			t.Errorf("stripScheme(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseTimeout_Default(t *testing.T) {
	t.Setenv(envTimeout, "")
	if got := parseTimeout(); got != 5*time.Second {
		t.Errorf("default timeout should be 5s, got %v", got)
	}
}

func TestParseTimeout_Override(t *testing.T) {
	t.Setenv(envTimeout, "12s")
	if got := parseTimeout(); got != 12*time.Second {
		t.Errorf("expected 12s, got %v", got)
	}
}

func TestParseTimeout_InvalidFallsBackToDefault(t *testing.T) {
	t.Setenv(envTimeout, "not-a-duration")
	if got := parseTimeout(); got != 5*time.Second {
		t.Errorf("invalid input should fall back to 5s default, got %v", got)
	}
}

func TestParseHeaders_Empty(t *testing.T) {
	t.Setenv(envHeaders, "")
	if got := parseHeaders(); got != nil {
		t.Errorf("expected nil for empty env, got %v", got)
	}
}

func TestParseHeaders_TwoEntries(t *testing.T) {
	t.Setenv(envHeaders, "api-key=secret123,tenant=acme")
	got := parseHeaders()
	if got["api-key"] != "secret123" {
		t.Errorf("api-key = %q, want secret123", got["api-key"])
	}
	if got["tenant"] != "acme" {
		t.Errorf("tenant = %q, want acme", got["tenant"])
	}
	if len(got) != 2 {
		t.Errorf("expected 2 headers, got %d: %v", len(got), got)
	}
}

func TestParseHeaders_TrimsWhitespace(t *testing.T) {
	t.Setenv(envHeaders, "  k1  =  v1 ,  k2= v2")
	got := parseHeaders()
	if got["k1"] != "v1" || got["k2"] != "v2" {
		t.Errorf("expected trimmed k1=v1 k2=v2, got %v", got)
	}
}

func TestParseHeaders_SkipsMalformed(t *testing.T) {
	t.Setenv(envHeaders, "missingequals,k=v,=novalue")
	got := parseHeaders()
	if _, ok := got["missingequals"]; ok {
		t.Error("expected missingequals to be skipped")
	}
	if got["k"] != "v" {
		t.Errorf("expected k=v survived, got %v", got)
	}
}

func TestInit_NoOpWhenEndpointUnset(t *testing.T) {
	// Confirm Init returns a non-nil shutdown function and never
	// panics when the endpoint is missing.
	t.Setenv(envEndpoint, "")
	shutdown := Init(context.Background(), "test-version")
	if shutdown == nil {
		t.Fatal("Init should always return a non-nil shutdown")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("noop shutdown should never error, got %v", err)
	}
	// Tracer() should also work without panicking.
	_ = Tracer()
}
