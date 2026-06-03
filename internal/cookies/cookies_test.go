package cookies

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestNewSessionCookie(t *testing.T) {
	c := NewSessionCookie("  raw value  ")
	if c.Name != "user_session" {
		t.Errorf("Name = %q, want user_session", c.Name)
	}
	if c.Value != "  raw value  " {
		t.Errorf("Value = %q, want the raw value passed through untrimmed", c.Value)
	}
	if c.Domain != "github.com" || c.Path != "/" || !c.Secure || !c.HttpOnly {
		t.Errorf("unexpected shape: Domain=%q Path=%q Secure=%v HttpOnly=%v", c.Domain, c.Path, c.Secure, c.HttpOnly)
	}
}

// us and li build github.com rawCookies of each kind for a given store.
func us(store, value string, creation time.Time) rawCookie {
	return rawCookie{store: store, domain: "github.com", name: "user_session", value: value, creation: creation}
}
func li(store, value string) rawCookie {
	return rawCookie{store: store, domain: "github.com", name: "logged_in", value: value}
}

func candByStore(cands []sessionCandidate, store string) (sessionCandidate, bool) {
	for _, c := range cands {
		if c.store == store {
			return c, true
		}
	}
	return sessionCandidate{}, false
}

func TestGroupCandidates(t *testing.T) {
	t.Run("single logged-in store", func(t *testing.T) {
		got := groupCandidates([]rawCookie{us("A", "tok", time.Time{}), li("A", "yes")})
		if len(got) != 1 || !got[0].loggedIn || got[0].cookie.Value != "tok" {
			t.Fatalf("got %+v, want one logged-in candidate with value tok", got)
		}
	})

	t.Run("#15 regression: logged-out store not conflated with logged-in one", func(t *testing.T) {
		got := groupCandidates([]rawCookie{
			us("A", "active", time.Time{}), li("A", "yes"),
			us("B", "stale", time.Time{}), // store B is logged out (no logged_in)
		})
		if len(got) != 2 {
			t.Fatalf("want 2 candidates, got %d", len(got))
		}
		a, _ := candByStore(got, "A")
		b, _ := candByStore(got, "B")
		if !a.loggedIn || b.loggedIn {
			t.Errorf("loggedIn flags wrong: A=%v (want true) B=%v (want false)", a.loggedIn, b.loggedIn)
		}
	})

	t.Run("cross-store guard: logged_in in A must not mark B", func(t *testing.T) {
		got := groupCandidates([]rawCookie{us("A", "a", time.Time{}), li("A", "yes"), us("B", "b", time.Time{})})
		b, _ := candByStore(got, "B")
		if b.loggedIn {
			t.Error("store B wrongly marked logged in by store A's logged_in cookie")
		}
	})

	t.Run("logged_in values other than yes are not logged in", func(t *testing.T) {
		for _, v := range []string{"no", "", "maybe", "Yes"} {
			got := groupCandidates([]rawCookie{us("A", "t", time.Time{}), li("A", v)})
			if got[0].loggedIn {
				t.Errorf("logged_in=%q should yield loggedIn=false", v)
			}
		}
	})

	t.Run("logged_in without user_session yields no candidate", func(t *testing.T) {
		if got := groupCandidates([]rawCookie{li("A", "yes")}); len(got) != 0 {
			t.Errorf("want no candidates, got %d", len(got))
		}
	})

	t.Run("subdomain cookies are ignored", func(t *testing.T) {
		raw := []rawCookie{
			{store: "A", domain: "gist.github.com", name: "user_session", value: "sub"},
			{store: "A", domain: "gist.github.com", name: "logged_in", value: "yes"},
		}
		if got := groupCandidates(raw); len(got) != 0 {
			t.Errorf("subdomain cookies must not produce a github.com candidate, got %d", len(got))
		}
	})

	t.Run("most-recent user_session per store kept; zero-creation safe", func(t *testing.T) {
		newer := time.Unix(1000, 0)
		raw := []rawCookie{us("A", "old", time.Time{}), us("A", "new", newer)}
		got := groupCandidates(raw)
		if len(got) != 1 || got[0].cookie.Value != "new" {
			t.Fatalf("want single candidate with newest value 'new', got %+v", got)
		}
	})

	t.Run("equal creation within a store: last-seen wins", func(t *testing.T) {
		eq := time.Unix(500, 0)
		got := groupCandidates([]rawCookie{us("A", "first", eq), us("A", "second", eq)})
		if len(got) != 1 || got[0].cookie.Value != "second" {
			t.Fatalf("want last-seen 'second', got %+v", got)
		}
	})

	t.Run("leading-dot github.com domain is accepted; subdomain still rejected", func(t *testing.T) {
		raw := []rawCookie{
			{store: "A", domain: ".github.com", name: "user_session", value: "dot"},
			{store: "A", domain: ".github.com", name: "logged_in", value: "yes"},
		}
		got := groupCandidates(raw)
		if len(got) != 1 || got[0].cookie.Value != "dot" || !got[0].loggedIn {
			t.Fatalf("want one logged-in candidate from .github.com, got %+v", got)
		}
	})
}

// recordingValidator returns a validator that fails for any cookie whose value
// is in dead, succeeds otherwise, and records the order of values it was asked about.
func recordingValidator(dead ...string) (func(*http.Cookie) error, *[]string) {
	deadSet := map[string]bool{}
	for _, d := range dead {
		deadSet[d] = true
	}
	var calls []string
	return func(c *http.Cookie) error {
		calls = append(calls, c.Value)
		if deadSet[c.Value] {
			return fmt.Errorf("token is invalid or expired")
		}
		return nil
	}, &calls
}

func cand(store, value string, loggedIn bool, creation time.Time) sessionCandidate {
	return sessionCandidate{cookie: NewSessionCookie(value), store: store, loggedIn: loggedIn, creation: creation}
}

func TestSelectSession(t *testing.T) {
	t.Run("empty is an error", func(t *testing.T) {
		if _, err := selectSession(nil, nil); err == nil {
			t.Error("expected error for empty candidates")
		}
	})

	t.Run("single candidate skips validation entirely", func(t *testing.T) {
		validate, calls := recordingValidator()
		got, err := selectSession([]sessionCandidate{cand("A", "only", true, time.Time{})}, validate)
		if err != nil || got.Value != "only" {
			t.Fatalf("got (%v, %v), want only/nil", got, err)
		}
		if len(*calls) != 0 {
			t.Errorf("validate must not be called for a single candidate; calls=%v", *calls)
		}
	})

	t.Run("nil validator returns most-recent without calls", func(t *testing.T) {
		older, newer := time.Unix(1, 0), time.Unix(2, 0)
		got, _ := selectSession([]sessionCandidate{cand("A", "old", true, older), cand("B", "new", true, newer)}, nil)
		if got.Value != "new" {
			t.Errorf("got %q, want most-recent 'new'", got.Value)
		}
	})

	t.Run("most-recent valid wins and short-circuits", func(t *testing.T) {
		older, newer := time.Unix(1, 0), time.Unix(2, 0)
		validate, calls := recordingValidator()
		got, _ := selectSession([]sessionCandidate{cand("A", "old", true, older), cand("B", "new", true, newer)}, validate)
		if got.Value != "new" {
			t.Errorf("got %q, want 'new'", got.Value)
		}
		if len(*calls) != 1 || (*calls)[0] != "new" {
			t.Errorf("expected to validate only 'new' then stop; calls=%v", *calls)
		}
	})

	t.Run("falls through to next when most-recent is dead", func(t *testing.T) {
		older, newer := time.Unix(1, 0), time.Unix(2, 0)
		validate, calls := recordingValidator("new") // newest is server-revoked
		got, _ := selectSession([]sessionCandidate{cand("A", "old", true, older), cand("B", "new", true, newer)}, validate)
		if got.Value != "old" {
			t.Errorf("got %q, want the live 'old'", got.Value)
		}
		if len(*calls) != 2 {
			t.Errorf("expected to validate both; calls=%v", *calls)
		}
	})

	t.Run("all dead falls back to most-recent (picker not gate)", func(t *testing.T) {
		older, newer := time.Unix(1, 0), time.Unix(2, 0)
		validate, _ := recordingValidator("old", "new")
		got, err := selectSession([]sessionCandidate{cand("A", "old", true, older), cand("B", "new", true, newer)}, validate)
		if err != nil || got.Value != "new" {
			t.Errorf("got (%q, %v), want most-recent 'new' and no error", got.Value, err)
		}
	})

	t.Run("no logged-in candidates falls back to the full set", func(t *testing.T) {
		older, newer := time.Unix(1, 0), time.Unix(2, 0)
		got, _ := selectSession([]sessionCandidate{cand("A", "old", false, older), cand("B", "new", false, newer)}, nil)
		if got.Value != "new" {
			t.Errorf("got %q, want 'new' from the full set", got.Value)
		}
	})

	t.Run("equal creation resolves deterministically by store key", func(t *testing.T) {
		eq := time.Unix(5, 0)
		forward := []sessionCandidate{cand("A", "a", true, eq), cand("B", "b", true, eq)}
		reverse := []sessionCandidate{cand("B", "b", true, eq), cand("A", "a", true, eq)}
		g1, _ := selectSession(forward, nil)
		g2, _ := selectSession(reverse, nil)
		if g1.Value != g2.Value || g1.Value != "a" {
			t.Errorf("nondeterministic tiebreak: %q vs %q (want both 'a')", g1.Value, g2.Value)
		}
	})
}
