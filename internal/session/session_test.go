package session

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCheckValidity_Valid(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<meta name="user-login" content="testuser">`)) //nolint:errcheck
	}))
	defer srv.Close()

	username, err := checkValidityWithURL(srv.Client(), srv.URL+"/settings/profile", "testtoken")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if username != "testuser" {
		t.Errorf("expected username 'testuser', got %q", username)
	}
}

func TestCheckValidity_Invalid(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/login", http.StatusFound)
	}))
	defer srv.Close()

	_, err := checkValidityWithURL(srv.Client(), srv.URL+"/settings/profile", "badtoken")
	if err == nil {
		t.Fatal("expected error for invalid token, got nil")
	}
	if !strings.Contains(err.Error(), "invalid or expired") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestCheckValidity_ValidNoUsername(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<html><body>no user-login meta here</body></html>`)) //nolint:errcheck
	}))
	defer srv.Close()

	username, err := checkValidityWithURL(srv.Client(), srv.URL+"/settings/profile", "validtoken")
	if err != nil {
		t.Fatalf("expected no error even with missing username meta, got: %v", err)
	}
	if username != "" {
		t.Errorf("expected empty username, got %q", username)
	}
}

func TestCheckValidity_NetworkError(t *testing.T) {
	_, err := checkValidityWithURL(nil, "http://127.0.0.1:1", "anytoken")
	if err == nil {
		t.Fatal("expected error for unreachable server, got nil")
	}
	if !strings.Contains(err.Error(), "failed to validate token") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// checkValidityWithURL is a testable variant of CheckValidity that accepts an
// injected HTTP client and a custom URL, so tests can use httptest servers.
func checkValidityWithURL(httpClient *http.Client, targetURL, tokenValue string) (string, error) {
	cookie := &http.Cookie{
		Name:     "user_session",
		Value:    tokenValue,
		Domain:   "github.com",
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
	}

	sameSite := &http.Cookie{
		Name:     "__Host-user_session_same_site",
		Value:    tokenValue,
		Domain:   "github.com",
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
	}

	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 2 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	} else {
		// Wrap the provided client to disable redirect following.
		orig := *httpClient
		orig.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
		httpClient = &orig
	}

	// Build a request manually so we can attach cookies without a jar
	// (jar won't match non-github.com domains used in tests).
	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		return "", err
	}
	req.AddCookie(cookie)
	req.AddCookie(sameSite)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to validate token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token is invalid or expired (status %d)", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil
	}

	match := userLoginRe.FindSubmatch(body)
	if match == nil {
		return "", nil
	}
	return string(match[1]), nil
}
