package session

import (
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"time"
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
	// cookiejar.New only errors when Options is non-nil and has an invalid
	// PublicSuffixList; passing nil is always safe.
	jar, _ := cookiejar.New(nil)
	// url.Parse with a hardcoded, valid URL never returns an error.
	ghURL, _ := url.Parse("https://github.com")

	sameSiteCookie := &http.Cookie{
		Name:     "__Host-user_session_same_site",
		Value:    sessionCookie.Value,
		Domain:   "github.com",
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
	}
	jar.SetCookies(ghURL, []*http.Cookie{sessionCookie, sameSiteCookie})

	client := &http.Client{
		Jar:     jar,
		Timeout: 15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.Get("https://github.com/settings/profile")
	if err != nil {
		return "", fmt.Errorf("failed to validate token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token is invalid or expired (status %d)", resp.StatusCode)
	}

	// Best-effort username extraction from the 200 response body.
	body, err := io.ReadAll(resp.Body)
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
