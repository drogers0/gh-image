package session

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/drogers0/gh-image/internal/cookies"
)

var userLoginRe = regexp.MustCompile(`<meta name="user-login" content="([^"]+)"`)

// CheckValidity verifies a GitHub session cookie is valid.
// It returns the authenticated username on success, or an error if the token
// is invalid, expired, or if the network request fails.
//
// Output conventions (for callers that print results):
//   - username → stdout
//   - status message → stderr
func CheckValidity(sessionCookie *http.Cookie) (string, error) {
	if sessionCookie == nil {
		return "", fmt.Errorf("session cookie is required")
	}
	if sessionCookie.Value == "" {
		return "", fmt.Errorf("session token is empty")
	}

	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return checkValidity(client, "https://github.com/settings/profile", sessionCookie)
}

// checkValidity does the real work of validating a session cookie against a
// target URL. The exported CheckValidity calls this with production defaults.
// Tests call this directly with httptest servers and custom clients.
func checkValidity(client *http.Client, targetURL string, sessionCookie *http.Cookie) (string, error) {
	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to validate token: %w", err)
	}
	for _, c := range cookies.SessionCookiePair(sessionCookie) {
		req.AddCookie(c)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to validate token: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// Continue below to parse the response body.
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return "", fmt.Errorf("token is invalid or expired (status %d)", resp.StatusCode)
	default:
		return "", fmt.Errorf("unexpected status while validating token: %d", resp.StatusCode)
	}

	// Best-effort username extraction from the 200 response body.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		// We already confirmed 200, so return empty username rather than failing.
		return "", nil
	}

	match := userLoginRe.FindSubmatch(body)
	if match == nil {
		return "", nil
	}

	return string(match[1]), nil
}
