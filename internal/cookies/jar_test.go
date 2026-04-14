package cookies

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestSessionCookiePair(t *testing.T) {
	input := &http.Cookie{
		Name:     "user_session",
		Value:    "abc123",
		Domain:   "github.com",
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
	}

	pair := SessionCookiePair(input)

	if pair[0] != input {
		t.Error("expected first element to be the input cookie")
	}

	companion := pair[1]
	if companion.Name != "__Host-user_session_same_site" {
		t.Errorf("expected companion name '__Host-user_session_same_site', got %q", companion.Name)
	}
	if companion.Value != input.Value {
		t.Errorf("expected companion value %q, got %q", input.Value, companion.Value)
	}
	if companion.Domain != input.Domain {
		t.Errorf("expected companion domain %q, got %q", input.Domain, companion.Domain)
	}
	if companion.Path != input.Path {
		t.Errorf("expected companion path %q, got %q", input.Path, companion.Path)
	}
	if companion.Secure != input.Secure {
		t.Errorf("expected companion Secure=%v, got %v", input.Secure, companion.Secure)
	}
	if companion.HttpOnly != input.HttpOnly {
		t.Errorf("expected companion HttpOnly=%v, got %v", input.HttpOnly, companion.HttpOnly)
	}
}

func TestNewGitHubCookieJar_ReturnsBothCookies(t *testing.T) {
	session := &http.Cookie{
		Name:     "user_session",
		Value:    "abc123",
		Domain:   "github.com",
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
	}

	jar := NewGitHubCookieJar(session)

	ghURL, _ := url.Parse("https://github.com")
	cookies := jar.Cookies(ghURL)

	if len(cookies) != 2 {
		t.Fatalf("expected 2 cookies in jar, got %d", len(cookies))
	}

	names := map[string]bool{}
	for _, c := range cookies {
		names[c.Name] = true
		if c.Value != "abc123" {
			t.Errorf("cookie %q has unexpected value %q", c.Name, c.Value)
		}
	}

	if !names["user_session"] {
		t.Error("jar missing user_session cookie")
	}
	if !names["__Host-user_session_same_site"] {
		t.Error("jar missing __Host-user_session_same_site cookie")
	}
}

func TestSessionCookiePair_NilPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for nil sessionCookie, got none")
		}
		msg := fmt.Sprint(r)
		if !strings.Contains(msg, "nil sessionCookie") {
			t.Errorf("expected panic message containing 'nil sessionCookie', got: %s", msg)
		}
	}()
	SessionCookiePair(nil)
}
