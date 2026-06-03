package cookies

import (
	"fmt"
	"net/http"
	"testing"
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
func us(store, value string) rawCookie {
	return rawCookie{store: store, domain: "github.com", name: "user_session", value: value}
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
		got := groupCandidates([]rawCookie{us("A", "tok"), li("A", "yes")})
		if len(got) != 1 || !got[0].loggedIn || got[0].cookie.Value != "tok" {
			t.Fatalf("got %+v, want one logged-in candidate with value tok", got)
		}
	})

	t.Run("#15 regression: logged-out store not conflated with logged-in one", func(t *testing.T) {
		got := groupCandidates([]rawCookie{
			us("A", "active"), li("A", "yes"),
			us("B", "stale"), // store B is logged out (no logged_in)
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
		got := groupCandidates([]rawCookie{us("A", "a"), li("A", "yes"), us("B", "b")})
		b, _ := candByStore(got, "B")
		if b.loggedIn {
			t.Error("store B wrongly marked logged in by store A's logged_in cookie")
		}
	})

	t.Run("logged_in values other than yes are not logged in", func(t *testing.T) {
		for _, v := range []string{"no", "", "maybe", "Yes"} {
			got := groupCandidates([]rawCookie{us("A", "t"), li("A", v)})
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

	t.Run("multiple user_session in one store: last-seen wins", func(t *testing.T) {
		got := groupCandidates([]rawCookie{us("A", "first"), us("A", "second")})
		if len(got) != 1 || got[0].cookie.Value != "second" {
			t.Fatalf("want single candidate with last-seen value 'second', got %+v", got)
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

func cand(store, value string, loggedIn bool) sessionCandidate {
	return sessionCandidate{cookie: NewSessionCookie(value), store: store, loggedIn: loggedIn}
}

func TestSelectSession(t *testing.T) {
	t.Run("empty is an error", func(t *testing.T) {
		if _, err := selectSession(nil, nil); err == nil {
			t.Error("expected error for empty candidates")
		}
	})

	t.Run("single candidate skips validation entirely", func(t *testing.T) {
		validate, calls := recordingValidator()
		got, err := selectSession([]sessionCandidate{cand("A", "only", true)}, validate)
		if err != nil || got.Value != "only" {
			t.Fatalf("got (%v, %v), want only/nil", got, err)
		}
		if len(*calls) != 0 {
			t.Errorf("validate must not be called for a single candidate; calls=%v", *calls)
		}
	})

	t.Run("nil validator returns first by store key without calls", func(t *testing.T) {
		got, _ := selectSession([]sessionCandidate{cand("B", "b", true), cand("A", "a", true)}, nil)
		if got.Value != "a" {
			t.Errorf("got %q, want first-by-store-key 'a'", got.Value)
		}
	})

	t.Run("first valid by store key wins and short-circuits", func(t *testing.T) {
		validate, calls := recordingValidator()
		got, _ := selectSession([]sessionCandidate{cand("B", "b", true), cand("A", "a", true)}, validate)
		if got.Value != "a" {
			t.Errorf("got %q, want 'a'", got.Value)
		}
		if len(*calls) != 1 || (*calls)[0] != "a" {
			t.Errorf("expected to validate only 'a' then stop; calls=%v", *calls)
		}
	})

	t.Run("falls through to next when the first is dead", func(t *testing.T) {
		validate, calls := recordingValidator("a") // store A's session is server-revoked
		got, _ := selectSession([]sessionCandidate{cand("A", "a", true), cand("B", "b", true)}, validate)
		if got.Value != "b" {
			t.Errorf("got %q, want the live 'b'", got.Value)
		}
		if len(*calls) != 2 {
			t.Errorf("expected to validate both; calls=%v", *calls)
		}
	})

	t.Run("all dead falls back to first by store key (picker not gate)", func(t *testing.T) {
		validate, _ := recordingValidator("a", "b")
		got, err := selectSession([]sessionCandidate{cand("A", "a", true), cand("B", "b", true)}, validate)
		if err != nil || got.Value != "a" {
			t.Errorf("got (%q, %v), want first-by-store-key 'a' and no error", got.Value, err)
		}
	})

	t.Run("no logged-in candidates falls back to the full set", func(t *testing.T) {
		got, _ := selectSession([]sessionCandidate{cand("B", "b", false), cand("A", "a", false)}, nil)
		if got.Value != "a" {
			t.Errorf("got %q, want 'a' from the full set", got.Value)
		}
	})

	t.Run("pick is deterministic by store key regardless of input order", func(t *testing.T) {
		forward := []sessionCandidate{cand("A", "a", true), cand("B", "b", true)}
		reverse := []sessionCandidate{cand("B", "b", true), cand("A", "a", true)}
		g1, _ := selectSession(forward, nil)
		g2, _ := selectSession(reverse, nil)
		if g1.Value != g2.Value || g1.Value != "a" {
			t.Errorf("nondeterministic tiebreak: %q vs %q (want both 'a')", g1.Value, g2.Value)
		}
	})
}
