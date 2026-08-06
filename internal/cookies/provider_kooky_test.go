//go:build !hbd

package cookies

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/browserutils/kooky"
)

// fakeBrowser is a minimal kooky.BrowserInfo for testing the store-key derivation.
type fakeBrowser struct{ path string }

func (f fakeBrowser) Browser() string        { return "chrome" }
func (f fakeBrowser) Profile() string        { return "default" }
func (f fakeBrowser) IsDefaultProfile() bool { return true }
func (f fakeBrowser) FilePath() string       { return f.path }

func TestMapKookyCookies(t *testing.T) {
	in := []*kooky.Cookie{
		{Cookie: http.Cookie{Domain: "github.com", Name: "user_session", Value: "v1"}, Container: "c1"},
		{Cookie: http.Cookie{Domain: "github.com", Name: "logged_in", Value: "yes"}, Container: "c2", Browser: fakeBrowser{path: "/p"}},
	}
	got := mapKookyCookies(in)
	if len(got) != 2 {
		t.Fatalf("got %d cookies, want 2", len(got))
	}
	// Browser nil → store is just the container.
	if got[0].store != "c1" || got[0].name != "user_session" || got[0].value != "v1" || got[0].domain != "github.com" {
		t.Errorf("cookie 0 = %+v", got[0])
	}
	// Browser set → store is FilePath()+"\x00"+Container.
	if got[1].store != "/p\x00c2" || got[1].name != "logged_in" {
		t.Errorf("cookie 1 = %+v", got[1])
	}
}

func TestAnnotateReadError(t *testing.T) {
	t.Run("ABE only", func(t *testing.T) {
		err := annotateReadError(fmt.Errorf("cookie store: chrome: decryption failed"))
		for _, want := range []string{"reading browser cookies", "decryption failed", "hint:", "GH_SESSION_TOKEN", "App-Bound Encryption"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("missing %q in %v", want, err)
			}
		}
	})

	t.Run("lock only", func(t *testing.T) {
		err := annotateReadError(fmt.Errorf("cookie store: open: The process cannot access the file because it is being used by another process."))
		for _, want := range []string{"Close the browser", "GH_SESSION_TOKEN"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("missing %q in %v", want, err)
			}
		}
	})

	t.Run("both present, lock hint first", func(t *testing.T) {
		err := annotateReadError(fmt.Errorf("edge: being used by another process; chrome: decryption failed"))
		msg := err.Error()
		if got := strings.Count(msg, "hint:"); got != 2 {
			t.Fatalf("expected 2 hints, got %d in %v", got, msg)
		}
		if strings.Index(msg, "Close the browser") > strings.Index(msg, "App-Bound Encryption") {
			t.Errorf("expected lock hint before ABE hint, got %v", msg)
		}
	})

	t.Run("no match leaves error untouched", func(t *testing.T) {
		err := annotateReadError(fmt.Errorf("keyring locked"))
		if got := err.Error(); got != "reading browser cookies: keyring locked" {
			t.Errorf("expected bare wrap, got %q", got)
		}
	})
}
