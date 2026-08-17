//go:build hbd

// Package cookies opt-in provider: moond4rk/hackbrowserdata. Selected with
// -tags hbd. Reads github.com cookies through HackBrowserData's importable
// browser packages instead of kooky. Credential retrieval is deliberately wired
// to the conservative, native per-OS path (see retrievers_hbd_*.go): no macOS
// login-password prompt, no Windows App-Bound-Encryption reflective injection.
package cookies

import (
	"errors"
	"strings"

	"github.com/moond4rk/hackbrowserdata/browser"
	"github.com/moond4rk/hackbrowserdata/types"
)

// browserReadHints is empty for the HackBrowserData provider: Extract swallows
// per-cookie decryption failures (it keeps the raw value) and only surfaces store
// read/open errors, none of which map to an actionable user hint. The "no cookie
// found" case is already covered by noSessionMsg. annotateReadError still needs
// the symbol, so it stays declared but nil.
var browserReadHints []readHint

// readRawCookies discovers every supported browser, decrypts its cookies via the
// native per-OS retrievers, and reduces the github.com ones to rawCookie. It is
// the only part of selection that touches real browser stores; the ranking logic
// lives in the pure functions in cookies.go.
func readRawCookies() ([]rawCookie, error) {
	// DiscoverBrowsers does not inject credentials (so macOS never prompts here);
	// we attach our own conservative retrievers below, bypassing HackBrowserData's
	// default injector and its login-password TTY prompt. Its error is joined, not
	// fatal — like the kooky reader we return whatever cookies were found alongside
	// the error and let chooseSession decide whether to surface it.
	browsers, err := browser.DiscoverBrowsers(browser.DiscoverOptions{Name: "all"})
	errs := []error{err}

	var raw []rawCookie
	for _, b := range browsers {
		if km, ok := b.(browser.KeyManager); ok {
			km.SetRetrievers(nativeRetrievers())
		}
		results, extractErr := b.Extract([]types.Category{types.Cookie})
		if extractErr != nil {
			errs = append(errs, extractErr)
			continue
		}
		raw = append(raw, mapExtract(b.UserDataDir(), results)...)
	}
	return raw, errors.Join(errs...)
}

// mapExtract reduces one browser's extract results to the github.com rawCookies
// selection needs. Split out so the (pure) store-key derivation and domain filter
// are unit-testable without touching real browser stores. The domain filter
// mirrors kooky's DomainHasSuffix("github.com") — subdomains are kept here and
// narrowed to host-only in groupCandidates.
func mapExtract(userDataDir string, results []types.ExtractResult) []rawCookie {
	var out []rawCookie
	for _, r := range results {
		if r.Data == nil {
			continue
		}
		for _, c := range r.Data.Cookies {
			if !strings.HasSuffix(strings.TrimPrefix(c.Host, "."), "github.com") {
				continue
			}
			out = append(out, rawCookie{
				store:  userDataDir + "\x00" + r.Profile.Name,
				domain: c.Host,
				name:   c.Name,
				value:  c.Value,
			})
		}
	}
	return out
}
