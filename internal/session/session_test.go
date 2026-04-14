package session

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// noRedirectClient returns the httptest server's TLS client with redirect
// following disabled, matching the production CheckValidity configuration.
func noRedirectClient(srv *httptest.Server) *http.Client {
	client := srv.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return client
}

func TestCheckValidity_Valid(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<meta name="user-login" content="testuser">`)) //nolint:errcheck
	}))
	defer srv.Close()

	cookie := &http.Cookie{Name: "user_session", Value: "testtoken"}

	username, err := checkValidity(noRedirectClient(srv), srv.URL+"/settings/profile", cookie)
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

	cookie := &http.Cookie{Name: "user_session", Value: "badtoken"}

	_, err := checkValidity(noRedirectClient(srv), srv.URL+"/settings/profile", cookie)
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

	cookie := &http.Cookie{Name: "user_session", Value: "validtoken"}

	username, err := checkValidity(noRedirectClient(srv), srv.URL+"/settings/profile", cookie)
	if err != nil {
		t.Fatalf("expected no error even with missing username meta, got: %v", err)
	}
	if username != "" {
		t.Errorf("expected empty username, got %q", username)
	}
}

func TestCheckValidity_NetworkError(t *testing.T) {
	client := &http.Client{
		Timeout: 2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	cookie := &http.Cookie{Name: "user_session", Value: "anytoken"}

	_, err := checkValidity(client, "http://127.0.0.1:1/settings/profile", cookie)
	if err == nil {
		t.Fatal("expected error for unreachable server, got nil")
	}
	if !strings.Contains(err.Error(), "failed to validate token") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestCheckValidity_NilCookie(t *testing.T) {
	_, err := CheckValidity(nil)
	if err == nil {
		t.Fatal("expected error for nil cookie, got nil")
	}
	if !strings.Contains(err.Error(), "required") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestCheckValidity_EmptyCookieValue(t *testing.T) {
	_, err := CheckValidity(&http.Cookie{Name: "user_session", Value: ""})
	if err == nil {
		t.Fatal("expected error for empty cookie value, got nil")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestCheckValidity_UnexpectedStatus(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	cookie := &http.Cookie{Name: "user_session", Value: "testtoken"}

	_, err := checkValidity(noRedirectClient(srv), srv.URL+"/settings/profile", cookie)
	if err == nil {
		t.Fatal("expected error for unexpected status, got nil")
	}
	if !strings.Contains(err.Error(), "unexpected status while validating token") {
		t.Errorf("unexpected error message: %v", err)
	}
}
