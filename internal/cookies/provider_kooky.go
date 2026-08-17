//go:build !hbd

// Package cookies default provider: browserutils/kooky. Selected whenever the
// `hbd` build tag is absent, so a stock `go build` links kooky and nothing from
// HackBrowserData. Build with -tags hbd to swap in provider_hbd.go instead.
package cookies

import (
	"context"

	"github.com/browserutils/kooky"
	_ "github.com/browserutils/kooky/browser/brave"
	_ "github.com/browserutils/kooky/browser/chrome"
	_ "github.com/browserutils/kooky/browser/chromium"
	_ "github.com/browserutils/kooky/browser/edge"
	_ "github.com/browserutils/kooky/browser/firefox"
	_ "github.com/browserutils/kooky/browser/opera"
	_ "github.com/browserutils/kooky/browser/safari"
)

// browserReadHints for kooky. Matching is by substring — the triggering strings
// originate outside our code and are not exported sentinels:
//   - "decryption failed" is a bare errors.New from kooky, confirmed present in
//     v0.2.10 (browserutils/kooky/internal/chrome/chrome.go). Re-verify on bump.
//   - "being used by another process" is the Windows sharing-violation OS string,
//     taken verbatim from the captured output in issue #5. NOTE: this text is
//     locale-dependent — non-English Windows won't match, which degrades to no
//     hint (never a wrong hint), consistent with annotateReadError's wrap-not-replace.
var browserReadHints = []readHint{
	{
		match: "being used by another process",
		hint: "A running browser is locking its cookie database. Close the " +
			"browser completely and try again, or set GH_SESSION_TOKEN to supply " +
			"the cookie manually.",
	},
	{
		match: "decryption failed",
		hint: "A Chromium browser (Chrome 127+, Edge, Brave, or Opera) is blocking " +
			"cookie decryption with App-Bound Encryption. Copy the github.com " +
			"user_session cookie value from that browser's DevTools " +
			"(F12 > Application > Cookies > github.com > user_session) and set " +
			"GH_SESSION_TOKEN to it.",
	},
}

// readRawCookies reads every valid github.com cookie across all supported
// browsers/profiles and reduces each to a rawCookie. It is the only part of
// selection that touches the real browser stores, so it is kept thin; the
// ranking logic lives in the pure functions in cookies.go.
func readRawCookies() ([]rawCookie, error) {
	ctx := context.Background()

	// No Name filter: we need logged_in alongside user_session to tell an active
	// session from a stale logged-out one in the same store.
	kcookies, err := kooky.ReadCookies(ctx,
		kooky.Valid,
		kooky.DomainHasSuffix("github.com"),
	)
	return mapKookyCookies(kcookies), err
}

// mapKookyCookies reduces kooky cookies to the fields selection needs. It is split
// from readRawCookies so the (pure) store-key derivation is unit-testable without
// touching real browser stores.
func mapKookyCookies(kcookies []*kooky.Cookie) []rawCookie {
	out := make([]rawCookie, 0, len(kcookies))
	for _, c := range kcookies {
		store := c.Container
		if c.Browser != nil {
			store = c.Browser.FilePath() + "\x00" + c.Container
		}
		out = append(out, rawCookie{
			store:  store,
			domain: c.Domain,
			name:   c.Name,
			value:  c.Value,
		})
	}
	return out
}
