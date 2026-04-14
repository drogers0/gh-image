package cookies

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
)

// SessionCookiePair returns the user_session cookie and its
// __Host-user_session_same_site counterpart. GitHub requires both for
// CSRF validation. The same-site cookie is synthesized from the session
// cookie's value and attributes.
func SessionCookiePair(sessionCookie *http.Cookie) [2]*http.Cookie {
	if sessionCookie == nil {
		panic("cookies: SessionCookiePair called with nil sessionCookie")
	}
	sameSite := &http.Cookie{
		Name:     "__Host-user_session_same_site",
		Value:    sessionCookie.Value,
		Domain:   sessionCookie.Domain,
		Path:     sessionCookie.Path,
		Secure:   sessionCookie.Secure,
		HttpOnly: sessionCookie.HttpOnly,
	}
	return [2]*http.Cookie{sessionCookie, sameSite}
}

// NewGitHubCookieJar creates a cookie jar pre-loaded with the given session
// cookie and its __Host-user_session_same_site counterpart, both set on
// https://github.com. GitHub requires both cookies for CSRF validation.
func NewGitHubCookieJar(sessionCookie *http.Cookie) http.CookieJar {
	// cookiejar.New only errors when Options is non-nil and has an invalid
	// PublicSuffixList; passing nil is always safe.
	jar, _ := cookiejar.New(nil)
	// url.Parse with a hardcoded, valid URL never returns an error.
	ghURL, _ := url.Parse("https://github.com")
	pair := SessionCookiePair(sessionCookie)
	jar.SetCookies(ghURL, pair[:]) // convert fixed-size array to slice
	return jar
}
