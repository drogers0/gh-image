package cookies

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
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
// decoupled from the provider so the selection logic is unit-testable. Providers
// (see provider_kooky.go / provider_hbd.go, selected by build tag) read the real
// browser stores and hand back a slice of these.
type rawCookie struct {
	store  string // provider store key — identifies one cookie store (profile/container)
	domain string
	name   string
	value  string
}

// sessionCandidate is a user_session cookie plus whether its store is logged in.
type sessionCandidate struct {
	cookie   *http.Cookie
	store    string
	loggedIn bool // a logged_in=yes cookie exists in the SAME store
}

// noSessionMsg is shown when no user_session cookie was found in any browser and
// there was no read error. Defined once — used by both empty-candidate paths.
const noSessionMsg = "no github.com user_session cookie found in any supported " +
	"browser — are you logged into GitHub? Set GH_SESSION_TOKEN to supply the " +
	"cookie manually, or log into GitHub in Chrome, Chromium, Edge, Firefox, " +
	"Brave, Opera, or Safari."

// readHint maps a substring that may appear in a browser-read error to actionable
// guidance. The trigger strings originate outside our code (provider/OS), so
// matching is by substring, not errors.Is. The concrete set lives in the active
// provider file (browserReadHints), since the strings are provider-specific.
type readHint struct{ match, hint string }

// annotateReadError wraps a browser-read error as "reading browser cookies" and
// appends any actionable hints whose trigger substring is present. Pure and
// unit-testable; the wrapping matches the previous inline behavior when no hint
// matches. We append rather than replace, so an upstream/OS wording change
// degrades to the raw error instead of a wrong message. Precondition: err is
// non-nil (callers only invoke it on a read error).
func annotateReadError(err error) error {
	wrapped := fmt.Errorf("reading browser cookies: %w", err)
	var hints []string
	msg := err.Error()
	for _, h := range browserReadHints {
		if strings.Contains(msg, h.match) {
			hints = append(hints, h.hint)
		}
	}
	if len(hints) == 0 {
		return wrapped
	}
	return fmt.Errorf("%w\nhint: %s", wrapped, strings.Join(hints, "\nhint: "))
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
			// A store essentially never holds two user_session cookies; if it
			// does, last-seen wins. There's no reliable recency signal to prefer
			// one, and the final pick across stores is made deterministic in
			// selectSession.
			s.session = c
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
// is down) it returns the first candidate (by store key) and lets the caller
// surface the authoritative error.
func selectSession(cands []sessionCandidate, validate func(*http.Cookie) error) (*http.Cookie, error) {
	if len(cands) == 0 {
		return nil, fmt.Errorf("%s", noSessionMsg)
	}

	// filterLoggedIn allocates; the fallback copies — so we never sort the
	// caller's slice in place.
	pool := filterLoggedIn(cands)
	if len(pool) == 0 {
		pool = append([]sessionCandidate(nil), cands...)
	}
	// Order by store key for a stable pick across runs (provider discovery order
	// is nondeterministic). There is no trustworthy recency signal to prefer one
	// store over another, so this order is arbitrary-but-deterministic; when it
	// matters (2+ live candidates) validation, not ordering, makes the choice.
	sort.Slice(pool, func(i, j int) bool {
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

// chooseSession turns a raw cookie read (and any read error) into the selected
// session cookie. Splitting this from readRawCookies keeps the provider browser
// read the only part of the package that isn't unit-testable.
func chooseSession(raw []rawCookie, readErr error, validate func(*http.Cookie) error) (*http.Cookie, error) {
	cands := groupCandidates(raw)
	if len(cands) == 0 {
		// Providers report errors for absent browsers/profiles alongside cookies
		// from present ones; only surface the read error if nothing usable came back.
		if readErr != nil {
			return nil, annotateReadError(readErr)
		}
		return nil, fmt.Errorf("%s", noSessionMsg)
	}
	return selectSession(cands, validate)
}

// GetGitHubSession returns the best github.com user_session cookie found across
// supported browsers. When more than one logged-in candidate exists, validate
// is used to pick a live one; pass nil to skip network validation. The browser
// read itself is delegated to the build-tag-selected provider's readRawCookies.
func GetGitHubSession(validate func(*http.Cookie) error) (*http.Cookie, error) {
	raw, err := readRawCookies()
	return chooseSession(raw, err, validate)
}
