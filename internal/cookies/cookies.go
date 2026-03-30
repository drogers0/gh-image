package cookies

import (
	"context"
	"fmt"
	"net/http"

	"github.com/browserutils/kooky"
	_ "github.com/browserutils/kooky/browser/brave"
	_ "github.com/browserutils/kooky/browser/chrome"
	_ "github.com/browserutils/kooky/browser/chromium"
	_ "github.com/browserutils/kooky/browser/edge"
)

// GetGitHubSession returns the user_session cookie for github.com.
// It searches Chrome, Brave, Edge, and Chromium (via kooky's registered
// finders), returning the cookie from the first browser that has one.
func GetGitHubSession() (*http.Cookie, error) {
	ctx := context.Background()

	cookies, err := kooky.ReadCookies(ctx,
		kooky.Valid,
		kooky.DomainHasSuffix("github.com"),
		kooky.Name("user_session"),
	)

	// kooky returns errors for browsers/profiles that don't exist alongside
	// cookies from ones that do. Only fail if we got zero cookies.
	if len(cookies) > 0 {
		return &cookies[0].Cookie, nil
	}

	// Fallback: read cookies directly via sqlite3 + platform keychain.
	// Handles browsers kooky doesn't support (e.g. Arc) and works around
	// kooky decryption failures.
	if c, fallbackErr := directReadGitHubSession(); fallbackErr == nil {
		return c, nil
	}

	if err != nil {
		return nil, fmt.Errorf("reading browser cookies: %w", err)
	}

	return nil, fmt.Errorf("no github.com user_session cookie found in any supported browser — are you logged into GitHub?")
}
