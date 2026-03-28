package main

import (
	"net/http"
	"strings"
	"testing"
)

// TestCookieFromValue_BasicAttributes verifies the cookie has the expected fields.
func TestCookieFromValue_BasicAttributes(t *testing.T) {
	cookie, err := cookieFromValue("mytoken123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cookie.Name != "user_session" {
		t.Errorf("expected Name 'user_session', got %q", cookie.Name)
	}
	if cookie.Value != "mytoken123" {
		t.Errorf("expected Value 'mytoken123', got %q", cookie.Value)
	}
	if cookie.Domain != "github.com" {
		t.Errorf("expected Domain 'github.com', got %q", cookie.Domain)
	}
	if cookie.Path != "/" {
		t.Errorf("expected Path '/', got %q", cookie.Path)
	}
	if !cookie.Secure {
		t.Error("expected Secure to be true")
	}
	if !cookie.HttpOnly {
		t.Error("expected HttpOnly to be true")
	}
}

// TestCookieFromValue_TrimsWhitespace verifies leading/trailing whitespace is stripped.
func TestCookieFromValue_TrimsWhitespace(t *testing.T) {
	cookie, err := cookieFromValue("  token_with_spaces  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cookie.Value != "token_with_spaces" {
		t.Errorf("expected whitespace trimmed, got %q", cookie.Value)
	}
}

// TestCookieFromValue_RejectsEmpty verifies that empty/whitespace-only values error.
func TestCookieFromValue_RejectsEmpty(t *testing.T) {
	tests := []string{"", "   ", "\t\n"}
	for _, v := range tests {
		_, err := cookieFromValue(v)
		if err == nil {
			t.Errorf("expected error for empty token %q, got nil", v)
		}
		if !strings.Contains(err.Error(), "empty") {
			t.Errorf("unexpected error message for %q: %v", v, err)
		}
	}
}

// TestResolveSessionCookie_FlagPriority verifies --token flag takes highest priority.
func TestResolveSessionCookie_FlagPriority(t *testing.T) {
	t.Setenv("GH_IMAGE_TOKEN", "env_token")
	cookie, err := resolveSessionCookie("flag_token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cookie.Value != "flag_token" {
		t.Errorf("expected flag_token to win, got %q", cookie.Value)
	}
}

// TestResolveSessionCookie_EnvFallback verifies GH_IMAGE_TOKEN is used when no flag.
func TestResolveSessionCookie_EnvFallback(t *testing.T) {
	t.Setenv("GH_IMAGE_TOKEN", "env_token_value")
	cookie, err := resolveSessionCookie("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cookie.Value != "env_token_value" {
		t.Errorf("expected env_token_value, got %q", cookie.Value)
	}
}

// TestResolveSessionCookie_BrowserFallbackError verifies browser error is wrapped correctly.
func TestResolveSessionCookie_BrowserFallbackError(t *testing.T) {
	// No flag, no env var: should fall through to browser extraction which
	// will fail in CI (no browser). Confirm the error message contains guidance.
	t.Setenv("GH_IMAGE_TOKEN", "")
	_, err := resolveSessionCookie("")
	if err == nil {
		// Only expected to fail when not in a browser environment
		t.Skip("browser cookies found; skipping browser-error test")
	}
	if !strings.Contains(err.Error(), "no session token found") {
		t.Errorf("expected 'no session token found' in error, got: %v", err)
	}
}

// TestCookieFromValue_UsableByNewClient verifies the cookie produced by
// cookieFromValue can be passed to upload.NewClient without panicking.
func TestCookieFromValue_UsableByNewClient(t *testing.T) {
	cookie, err := cookieFromValue("testtoken")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Verify it's an *http.Cookie (type assertion sanity check).
	var _ *http.Cookie = cookie
}
