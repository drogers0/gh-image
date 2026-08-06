//go:build hbd

package cookies

import (
	"testing"

	"github.com/moond4rk/hackbrowserdata/types"
)

// entry builds a CookieEntry with just the fields mapExtract reads.
func entry(host, name, value string) types.CookieEntry {
	return types.CookieEntry{Host: host, Name: name, Value: value}
}

// result wraps cookies in one profile's ExtractResult.
func result(profile string, cookies ...types.CookieEntry) types.ExtractResult {
	return types.ExtractResult{
		Profile: types.Profile{Name: profile},
		Data:    &types.BrowserData{Cookies: cookies},
	}
}

func TestMapExtract(t *testing.T) {
	t.Run("keeps github.com (and subdomains), derives store key, drops others", func(t *testing.T) {
		results := []types.ExtractResult{
			result("Default",
				entry("github.com", "user_session", "tok"),
				entry(".github.com", "logged_in", "yes"),
				entry("gist.github.com", "sub", "x"), // subdomain kept here; narrowed in groupCandidates
				entry("example.com", "other", "nope"),
			),
		}
		got := mapExtract("/data", results)
		if len(got) != 3 {
			t.Fatalf("got %d cookies, want 3 (example.com dropped)", len(got))
		}
		if got[0].store != "/data\x00Default" || got[0].name != "user_session" || got[0].value != "tok" {
			t.Errorf("cookie 0 = %+v", got[0])
		}
		for _, c := range got {
			if c.domain == "example.com" {
				t.Errorf("non-github domain leaked: %+v", c)
			}
		}
	})

	t.Run("nil Data is skipped", func(t *testing.T) {
		results := []types.ExtractResult{{Profile: types.Profile{Name: "P"}, Data: nil}}
		if got := mapExtract("/d", results); len(got) != 0 {
			t.Errorf("want 0 cookies for nil Data, got %d", len(got))
		}
	})

	t.Run("store key separates profiles under one data dir", func(t *testing.T) {
		results := []types.ExtractResult{
			result("Default", entry("github.com", "user_session", "a")),
			result("Profile 1", entry("github.com", "user_session", "b")),
		}
		got := mapExtract("/d", results)
		if len(got) != 2 || got[0].store == got[1].store {
			t.Fatalf("profiles must map to distinct stores, got %+v", got)
		}
	})
}
