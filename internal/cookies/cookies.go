package cookies

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/browserutils/kooky"
	_ "github.com/browserutils/kooky/browser/brave"
	_ "github.com/browserutils/kooky/browser/chrome"
	_ "github.com/browserutils/kooky/browser/chromium"
	_ "github.com/browserutils/kooky/browser/edge"
	_ "github.com/browserutils/kooky/browser/firefox"
	_ "github.com/browserutils/kooky/browser/opera"
	_ "github.com/browserutils/kooky/browser/safari"
)

// NewSessionCookie builds a github.com user_session cookie from a raw value.
// Shape only — it does not trim or validate the value; callers handling
// user-supplied tokens layer those checks on top.
func NewSessionCookie(value string) *http.Cookie {
	return &http.Cookie{
		Name:     "user_session",
		Value:    value,
		Domain:   "github.com",
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
	}
}

// rawCookie is a github.com cookie reduced to the fields selection needs,
// decoupled from kooky so the selection logic is unit-testable.
type rawCookie struct {
	store    string // FilePath()+"\x00"+Container — identifies one cookie store
	domain   string
	name     string
	value    string
	creation time.Time
}

// sessionCandidate is a user_session cookie plus whether its store is logged in.
type sessionCandidate struct {
	cookie   *http.Cookie
	store    string
	loggedIn bool // a logged_in=yes cookie exists in the SAME store
	creation time.Time
}

// readRawCookies reads every valid github.com cookie across all supported
// browsers/profiles and reduces each to a rawCookie. It is the only part of
// selection that touches the real browser stores, so it is kept thin; the
// ranking logic lives in the pure functions below.
func readRawCookies() ([]rawCookie, error) {
	ctx := context.Background()

	// No Name filter: we need logged_in alongside user_session to tell an active
	// session from a stale logged-out one in the same store.
	kcookies, err := kooky.ReadCookies(ctx,
		kooky.Valid,
		kooky.DomainHasSuffix("github.com"),
	)

	out := make([]rawCookie, 0, len(kcookies))
	for _, c := range kcookies {
		store := c.Container
		if c.Browser != nil {
			store = c.Browser.FilePath() + "\x00" + c.Container
		}
		out = append(out, rawCookie{
			store:    store,
			domain:   c.Domain,
			name:     c.Name,
			value:    c.Value,
			creation: c.Creation,
		})
	}
	return out, err
}

// groupCandidates buckets raw cookies by store and produces one candidate per
// store that holds a user_session, recording whether that same store is logged
// in. Only host-only github.com cookies are considered, so subdomain cookies
// (gist.github.com, …) can't pollute the logged_in correlation.
func groupCandidates(raw []rawCookie) []sessionCandidate {
	type store struct {
		session  *rawCookie
		loggedIn bool
	}
	stores := map[string]*store{}
	for i := range raw {
		c := &raw[i]
		// Host-only github.com only; tolerate a leading-dot domain but still
		// exclude subdomains (gist.github.com, …) from the logged_in correlation.
		if strings.TrimPrefix(c.domain, ".") != "github.com" {
			continue
		}
		s := stores[c.store]
		if s == nil {
			s = &store{}
			stores[c.store] = s
		}
		switch c.name {
		case "user_session":
			// Most-recent within a store; on a tie, last-seen wins. Intra-store
			// ties are effectively impossible (distinct rowids / real timestamps).
			if s.session == nil || !c.creation.Before(s.session.creation) {
				s.session = c
			}
		case "logged_in":
			if c.value == "yes" {
				s.loggedIn = true
			}
		}
	}

	out := make([]sessionCandidate, 0, len(stores))
	for key, s := range stores {
		if s.session == nil {
			continue
		}
		out = append(out, sessionCandidate{
			cookie:   NewSessionCookie(s.session.value),
			store:    key,
			loggedIn: s.loggedIn,
			creation: s.session.creation,
		})
	}
	return out
}

func filterLoggedIn(cands []sessionCandidate) []sessionCandidate {
	out := make([]sessionCandidate, 0, len(cands))
	for _, c := range cands {
		if c.loggedIn {
			out = append(out, c)
		}
	}
	return out
}

// selectSession chooses the best candidate. validate may be nil to skip network
// validation (an offline, local-only pick).
//
// It prefers stores that are actually logged in, then disambiguates any
// remaining tie by validating against GitHub — but only when more than one
// candidate survives, since a lone candidate is the only choice anyway. It is a
// picker, not a gate: if validation is inconclusive (all fail, or the network
// is down) it returns the most-recent candidate and lets the caller surface the
// authoritative error.
func selectSession(cands []sessionCandidate, validate func(*http.Cookie) error) (*http.Cookie, error) {
	if len(cands) == 0 {
		return nil, fmt.Errorf("no github.com user_session cookie found in any supported browser — are you logged into GitHub?")
	}

	// filterLoggedIn allocates; the fallback copies — so we never sort the
	// caller's slice in place.
	pool := filterLoggedIn(cands)
	if len(pool) == 0 {
		pool = append([]sessionCandidate(nil), cands...)
	}
	// Creation is a weak signal (rowid-derived, not wall-clock, on Chromium), so
	// it only orders our attempts; the store key gives a deterministic tiebreak
	// despite kooky's nondeterministic discovery order.
	sort.SliceStable(pool, func(i, j int) bool {
		if !pool[i].creation.Equal(pool[j].creation) {
			return pool[i].creation.After(pool[j].creation)
		}
		return pool[i].store < pool[j].store
	})

	if len(pool) == 1 || validate == nil {
		return pool[0].cookie, nil
	}

	for _, c := range pool {
		if validate(c.cookie) == nil {
			return c.cookie, nil
		}
	}
	return pool[0].cookie, nil
}

// GetGitHubSession returns the best github.com user_session cookie found across
// supported browsers. When more than one logged-in candidate exists, validate
// is used to pick a live one; pass nil to skip network validation.
func GetGitHubSession(validate func(*http.Cookie) error) (*http.Cookie, error) {
	raw, err := readRawCookies()
	cands := groupCandidates(raw)
	if len(cands) == 0 {
		// kooky reports errors for absent browsers/profiles alongside cookies
		// from present ones; only surface the read error if nothing usable came back.
		if err != nil {
			return nil, fmt.Errorf("reading browser cookies: %w", err)
		}
		return nil, fmt.Errorf("no github.com user_session cookie found in any supported browser — are you logged into GitHub?")
	}
	return selectSession(cands, validate)
}
