package cookies

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
)

// NewGitHubCookieJar creates a cookie jar pre-loaded with the given session
// cookie and its __Host-user_session_same_site counterpart, both set on
// https://github.com. GitHub requires both cookies for CSRF validation.
func NewGitHubCookieJar(sessionCookie *http.Cookie) http.CookieJar {
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
	return jar
}
