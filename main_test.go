package main

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/drogers0/gh-image/internal/upload"
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
	cookie, err := resolveSessionCookieWithGetter("flag_token", "env_token", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cookie.Value != "flag_token" {
		t.Errorf("expected flag_token to win, got %q", cookie.Value)
	}
}

// TestResolveSessionCookie_EnvFallback verifies GH_SESSION_TOKEN is used when no flag.
func TestResolveSessionCookie_EnvFallback(t *testing.T) {
	cookie, err := resolveSessionCookieWithGetter("", "env_token_value", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cookie.Value != "env_token_value" {
		t.Errorf("expected env_token_value, got %q", cookie.Value)
	}
}

// TestResolveSessionCookie_BrowserFallbackError verifies browser error is wrapped correctly.
func TestResolveSessionCookie_BrowserFallbackError(t *testing.T) {
	_, err := resolveSessionCookieWithGetter("", "", func() (*http.Cookie, error) {
		return nil, fmt.Errorf("no browser cookies available")
	})
	if err == nil {
		t.Fatal("expected error when browser getter fails, got nil")
	}
	if !strings.Contains(err.Error(), "no session token found") {
		t.Errorf("expected 'no session token found' in error, got: %v", err)
	}
}

func TestResolveSessionCookie_BrowserFallbackSuccess(t *testing.T) {
	cookie, err := resolveSessionCookieWithGetter("", "", func() (*http.Cookie, error) {
		return &http.Cookie{
			Name:  "user_session",
			Value: "browser_token",
		}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cookie == nil {
		t.Fatal("expected non-nil cookie")
	}
	if cookie.Value != "browser_token" {
		t.Fatalf("expected browser token, got %q", cookie.Value)
	}
}

// TestCookieFromValue_UsableByNewClient verifies the cookie produced by
// cookieFromValue can be passed to upload.NewClient.
func TestCookieFromValue_UsableByNewClient(t *testing.T) {
	cookie, err := cookieFromValue("testtoken")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	client := upload.NewClient(cookie)
	if client == nil {
		t.Fatal("expected upload.NewClient to return a non-nil client")
	}
}

func TestExtractToken_Success(t *testing.T) {
	value, err := extractToken(func() (*http.Cookie, error) {
		return &http.Cookie{Name: "user_session", Value: "browser_abc"}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if value != "browser_abc" {
		t.Errorf("expected 'browser_abc', got %q", value)
	}
}

func TestExtractToken_Error(t *testing.T) {
	_, err := extractToken(func() (*http.Cookie, error) {
		return nil, fmt.Errorf("no cookies")
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestCheckToken_Success(t *testing.T) {
	username, err := checkToken("sometoken",
		func(token string) (*http.Cookie, error) {
			return cookieFromValue(token)
		},
		func(c *http.Cookie) (string, error) {
			return "testuser", nil
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if username != "testuser" {
		t.Errorf("expected 'testuser', got %q", username)
	}
}

func TestCheckToken_ResolverError(t *testing.T) {
	_, err := checkToken("",
		func(token string) (*http.Cookie, error) {
			return nil, fmt.Errorf("no token")
		},
		func(c *http.Cookie) (string, error) {
			return "unused", nil
		},
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestCheckToken_ValidatorError(t *testing.T) {
	_, err := checkToken("sometoken",
		func(token string) (*http.Cookie, error) {
			return cookieFromValue(token)
		},
		func(c *http.Cookie) (string, error) {
			return "", fmt.Errorf("token expired")
		},
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestResolveSessionCookie_WhitespaceEnvVar(t *testing.T) {
	browserCalled := false
	_, err := resolveSessionCookieWithGetter("", "   ", func() (*http.Cookie, error) {
		browserCalled = true
		return &http.Cookie{Name: "user_session", Value: "browser_token"}, nil
	})
	if err == nil {
		t.Fatal("expected error for whitespace-only env token, got nil")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("expected error containing 'empty', got: %v", err)
	}
	if browserCalled {
		t.Error("whitespace-only env token should not fall through to browser getter")
	}
}

func TestClassifySubcommand(t *testing.T) {
	tests := []struct {
		name                    string
		imagePaths              []string
		firstPosAfterDoubleDash bool
		tokenFlag               string
		wantSubcommand          string
		wantErrContains         string
	}{
		{
			name:           "extract-token selected",
			imagePaths:     []string{"extract-token"},
			wantSubcommand: "extract-token",
		},
		{
			name:           "check-token selected",
			imagePaths:     []string{"check-token"},
			wantSubcommand: "check-token",
		},
		{
			name:                    "double-dash treats check-token as filename",
			imagePaths:              []string{"check-token"},
			firstPosAfterDoubleDash: true,
			wantSubcommand:          "",
		},
		{
			name:                    "double-dash treats extract-token as filename",
			imagePaths:              []string{"extract-token"},
			firstPosAfterDoubleDash: true,
			wantSubcommand:          "",
		},
		{
			name:            "extract-token with extra args errors",
			imagePaths:      []string{"extract-token", "extra"},
			wantErrContains: "does not take positional arguments",
		},
		{
			name:            "check-token with extra args errors",
			imagePaths:      []string{"check-token", "extra"},
			wantErrContains: "does not take positional arguments",
		},
		{
			name:            "extract-token with token flag errors",
			imagePaths:      []string{"extract-token"},
			tokenFlag:       "abc123",
			wantErrContains: "--token cannot be combined",
		},
		{
			name:           "non-subcommand remains upload mode",
			imagePaths:     []string{"image.png"},
			wantSubcommand: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotSubcommand, err := classifySubcommand(tc.imagePaths, tc.firstPosAfterDoubleDash, tc.tokenFlag)
			if tc.wantErrContains != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErrContains)
				}
				if !strings.Contains(err.Error(), tc.wantErrContains) {
					t.Fatalf("expected error containing %q, got %q", tc.wantErrContains, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotSubcommand != tc.wantSubcommand {
				t.Fatalf("expected subcommand %q, got %q", tc.wantSubcommand, gotSubcommand)
			}
		})
	}
}
