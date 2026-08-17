package download

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testToken = "test-session-token"

// testClient wires a cookie-only Client at an httptest server, the shape most
// tests want. TLS is used because the presigned classification requires the
// redirect target to share the base scheme, and the production base is https.
func testClient(t *testing.T, handler http.Handler) (*Client, *httptest.Server) {
	t.Helper()
	return routedClient(t, handler, nil)
}

// routedClient is testClient with an explicit bearer route. A nil newBearer
// pins the run to the session cookie.
func routedClient(t *testing.T, handler http.Handler, newBearer func() (string, error)) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)

	c := NewClient(newBearer, func() (*http.Cookie, error) {
		return &http.Cookie{Name: "user_session", Value: testToken}, nil
	}, func(string) {})
	c.baseURL = srv.URL
	c.resolveClient.Transport = srv.Client().Transport
	c.fetchClient.Transport = srv.Client().Transport
	return c, srv
}

// assetRef and fileRef are the two shapes, parsed the way callers get them.
func assetRef(t *testing.T) Ref {
	t.Helper()
	r, err := ParseRef("https://github.com/user-attachments/assets/9f57198c-19d3-4ba0-a48d-ba4bcaccf9f0")
	if err != nil {
		t.Fatalf("ParseRef: %v", err)
	}
	return r
}

func fileRef(t *testing.T) Ref {
	t.Helper()
	r, err := ParseRef("https://github.com/user-attachments/files/30473702/notes.txt")
	if err != nil {
		t.Fatalf("ParseRef: %v", err)
	}
	return r
}

// redirectTo builds a handler that answers the attachment path with a 302 to
// location, then serves body once the request carries a signature. Keying on
// the signature rather than a fixed path lets callers vary the presigned path,
// which is where the derived extension comes from.
func redirectTo(location string, body []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("X-Amz-Signature") != "" {
			_, _ = w.Write(body)
			return
		}
		http.Redirect(w, r, location, http.StatusFound)
	})
}

func TestParseRef(t *testing.T) {
	t.Run("asset shape", func(t *testing.T) {
		r, err := ParseRef("https://github.com/user-attachments/assets/9F57198C-19d3-4ba0-a48d-ba4bcaccf9f0")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if r.Kind != KindAsset || r.ID != "9F57198C-19d3-4ba0-a48d-ba4bcaccf9f0" || r.Name != "" {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("file shape", func(t *testing.T) {
		r, err := ParseRef("https://github.com/user-attachments/files/30473702/notes.txt")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if r.Kind != KindFile || r.ID != "30473702" || r.Name != "notes.txt" {
			t.Fatalf("got %+v", r)
		}
	})

	rejected := map[string]string{
		"non-github host":     "https://example.com/user-attachments/assets/9f57198c-19d3-4ba0-a48d-ba4bcaccf9f0",
		"http scheme":         "http://github.com/user-attachments/assets/9f57198c-19d3-4ba0-a48d-ba4bcaccf9f0",
		"embedded credential": "https://user:pass@github.com/user-attachments/assets/9f57198c-19d3-4ba0-a48d-ba4bcaccf9f0",
		"malformed uuid":      "https://github.com/user-attachments/assets/not-a-uuid",
		"uuid too short":      "https://github.com/user-attachments/assets/9f57198c-19d3-4ba0-a48d-ba4bcaccf",
		"files without name":  "https://github.com/user-attachments/files/30473702",
		"files empty name":    "https://github.com/user-attachments/files/30473702/",
		"files multi segment": "https://github.com/user-attachments/files/30473702/a/b.txt",
		"files non-numeric":   "https://github.com/user-attachments/files/abc/notes.txt",
		"trailing slash":      "https://github.com/user-attachments/assets/9f57198c-19d3-4ba0-a48d-ba4bcaccf9f0/",
		"unrelated path":      "https://github.com/drogers0/gh-image",
		"wrong collection":    "https://github.com/user-attachments/other/9f57198c-19d3-4ba0-a48d-ba4bcaccf9f0",
	}
	for name, raw := range rejected {
		t.Run("rejects "+name, func(t *testing.T) {
			if _, err := ParseRef(raw); err == nil {
				t.Fatalf("expected an error for %s", raw)
			}
		})
	}
}

func TestResolveSendsBothCookies(t *testing.T) {
	var got []*http.Cookie
	c, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("X-Amz-Signature") != "" {
			_, _ = w.Write([]byte("ok"))
			return
		}
		got = r.Cookies()
		http.Redirect(w, r, "/presigned?X-Amz-Signature=abc", http.StatusFound)
	}))

	if _, err := c.Stream(assetRef(t), io.Discard); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	names := map[string]string{}
	for _, ck := range got {
		names[ck.Name] = ck.Value
	}
	for _, want := range []string{"user_session", "__Host-user_session_same_site"} {
		if names[want] != testToken {
			t.Errorf("cookie %s = %q, want %q (sending only user_session looks correct and fails to authenticate)", want, names[want], testToken)
		}
	}
}

func TestFetchLegSendsNoCredentials(t *testing.T) {
	var auth, cookie string
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/presigned?X-Amz-Signature=abc", http.StatusFound)
	})
	mux.HandleFunc("/presigned", func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		cookie = r.Header.Get("Cookie")
		_, _ = w.Write([]byte("payload"))
	})
	c, _ := testClient(t, mux)

	if _, err := c.Stream(assetRef(t), io.Discard); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	// An Authorization header on the S3 bucket is rejected with a 400, and that
	// failure is invisible on the other storage host — only this assertion keeps
	// the separation fixed.
	if auth != "" {
		t.Errorf("presigned leg sent Authorization: %q", auth)
	}
	if cookie != "" {
		t.Errorf("presigned leg sent Cookie: %q", cookie)
	}
}

func TestResolveClassifiesRedirects(t *testing.T) {
	t.Run("login redirect reports an expired session", func(t *testing.T) {
		c, _ := testClient(t, redirectTo("/login", nil))
		_, err := c.Stream(assetRef(t), io.Discard)
		if !errors.Is(err, errExpiredSession) {
			t.Fatalf("got %v, want errExpiredSession", err)
		}
	})

	t.Run("absolute login redirect on the base host also reports expiry", func(t *testing.T) {
		var c *Client
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, c.baseURL+"/login", http.StatusFound)
		})
		c, _ = testClient(t, mux)
		_, err := c.Stream(assetRef(t), io.Discard)
		if !errors.Is(err, errExpiredSession) {
			t.Fatalf("got %v, want errExpiredSession", err)
		}
	})

	t.Run("login redirect on another host is not an expiry signal", func(t *testing.T) {
		// Matching /login by path alone would let an unrelated host claim the
		// credential is stale.
		c, _ := testClient(t, redirectTo("https://attacker.example/login", nil))
		_, err := c.Stream(assetRef(t), io.Discard)
		if err == nil || errors.Is(err, errExpiredSession) {
			t.Fatalf("got %v, want a hard error", err)
		}
		if !strings.Contains(err.Error(), "not a presigned asset URL") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("redirect without a signature is refused", func(t *testing.T) {
		c, _ := testClient(t, redirectTo("/presigned", []byte("<html>sso</html>")))
		_, err := c.Stream(assetRef(t), io.Discard)
		if err == nil || !strings.Contains(err.Error(), "not a presigned asset URL") {
			t.Fatalf("got %v, want a refusal", err)
		}
	})

	t.Run("404 names both causes", func(t *testing.T) {
		c, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		_, err := c.Stream(assetRef(t), io.Discard)
		if !errors.Is(err, errNoAccess) {
			t.Fatalf("got %v, want errNoAccess", err)
		}
		for _, want := range []string{"does not exist", "no access"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q missing %q", err, want)
			}
		}
	})

	t.Run("unexpected status is reported", func(t *testing.T) {
		c, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		_, err := c.Stream(assetRef(t), io.Discard)
		if err == nil || !strings.Contains(err.Error(), "500") {
			t.Fatalf("got %v, want a 500 report", err)
		}
	})
}

func TestFetchLegDoesNotFollowRedirects(t *testing.T) {
	// A presigned URL that answers with another redirect must not carry the
	// download onward past the classification the resolve leg performed.
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/presigned?X-Amz-Signature=abc", http.StatusFound)
	})
	mux.HandleFunc("/presigned", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	})
	mux.HandleFunc("/elsewhere", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("attacker payload"))
	})
	c, _ := testClient(t, mux)

	var sb strings.Builder
	_, err := c.Stream(assetRef(t), &sb)
	if err == nil || !strings.Contains(err.Error(), "expected 200") {
		t.Fatalf("got %v, want a non-200 refusal", err)
	}
	if sb.Len() != 0 {
		t.Errorf("wrote %q despite the refusal", sb.String())
	}
}

func TestFetchLegRejectsBadResponses(t *testing.T) {
	t.Run("non-200 is an error", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/presigned?X-Amz-Signature=abc", http.StatusFound)
		})
		mux.HandleFunc("/presigned", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		})
		c, _ := testClient(t, mux)
		if _, err := c.Stream(assetRef(t), io.Discard); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("short read against Content-Length is an error", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/presigned?X-Amz-Signature=abc", http.StatusFound)
		})
		mux.HandleFunc("/presigned", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "100")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("too short"))
		})
		c, _ := testClient(t, mux)
		_, err := c.Stream(assetRef(t), io.Discard)
		if err == nil {
			t.Fatal("expected a short-read error")
		}
	})
}

func TestSaveDerivesNames(t *testing.T) {
	t.Run("file URL uses its own name segment", func(t *testing.T) {
		dir := t.TempDir()
		c, _ := testClient(t, redirectTo("/presigned.txt?X-Amz-Signature=abc", []byte("body")))
		got, err := c.Save(fileRef(t), Dest{Dir: dir})
		if err != nil {
			t.Fatalf("Save: %v", err)
		}
		if filepath.Base(got) != "notes.txt" {
			t.Fatalf("got %s, want notes.txt", got)
		}
	})

	t.Run("asset URL takes its extension from the presigned path", func(t *testing.T) {
		dir := t.TempDir()
		c, _ := testClient(t, redirectTo("/627995430-9f57198c.png?X-Amz-Signature=abc", []byte("png")))
		got, err := c.Save(assetRef(t), Dest{Dir: dir})
		if err != nil {
			t.Fatalf("Save: %v", err)
		}
		if want := "9f57198c-19d3-4ba0-a48d-ba4bcaccf9f0.png"; filepath.Base(got) != want {
			t.Fatalf("got %s, want %s", filepath.Base(got), want)
		}
	})

	t.Run("asset URL with no extension falls back to the bare uuid", func(t *testing.T) {
		dir := t.TempDir()
		c, _ := testClient(t, redirectTo("/presigned?X-Amz-Signature=abc", []byte("x")))
		got, err := c.Save(assetRef(t), Dest{Dir: dir})
		if err != nil {
			t.Fatalf("Save: %v", err)
		}
		if want := "9f57198c-19d3-4ba0-a48d-ba4bcaccf9f0"; filepath.Base(got) != want {
			t.Fatalf("got %s, want %s", filepath.Base(got), want)
		}
	})
}

func TestDerivedNameContainsTraversal(t *testing.T) {
	// The name segment reaches the filesystem, so a traversal-shaped one must
	// resolve inside the destination rather than above it.
	ref := Ref{Kind: KindFile, ID: "1", Name: "../../etc/passwd"}
	if got := derivedName(ref, ""); got != "passwd" {
		t.Fatalf("got %q, want passwd", got)
	}
	// A percent-encoded NUL survives URL decoding; it must fall back rather than
	// reach os.OpenFile, which would fail with a bare "invalid argument".
	for _, name := range []string{"..", ".", "", "/", "a\x00b.txt", "\x7f"} {
		ref := Ref{Kind: KindFile, ID: "42", Name: name}
		if got := derivedName(ref, ""); got != "42" {
			t.Errorf("name %q derived %q, want the id fallback 42", name, got)
		}
	}
}

func TestSaveOutputModes(t *testing.T) {
	t.Run("exact path is used verbatim", func(t *testing.T) {
		dir := t.TempDir()
		exact := filepath.Join(dir, "chosen.bin")
		c, _ := testClient(t, redirectTo("/presigned.png?X-Amz-Signature=abc", []byte("data")))
		got, err := c.Save(assetRef(t), Dest{Exact: exact})
		if err != nil {
			t.Fatalf("Save: %v", err)
		}
		if got != exact {
			t.Fatalf("got %s, want %s", got, exact)
		}
	})

	t.Run("overwrites by default", func(t *testing.T) {
		dir := t.TempDir()
		c, _ := testClient(t, redirectTo("/presigned.png?X-Amz-Signature=abc", []byte("new")))
		for i := 0; i < 2; i++ {
			if _, err := c.Save(assetRef(t), Dest{Dir: dir}); err != nil {
				t.Fatalf("Save %d: %v", i, err)
			}
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 {
			t.Fatalf("got %d files, want 1 overwritten file", len(entries))
		}
	})

	t.Run("no-clobber suffixes rather than overwriting", func(t *testing.T) {
		dir := t.TempDir()
		c, _ := testClient(t, redirectTo("/presigned.png?X-Amz-Signature=abc", []byte("data")))
		var paths []string
		for i := 0; i < 3; i++ {
			p, err := c.Save(assetRef(t), Dest{Dir: dir, NoClobber: true})
			if err != nil {
				t.Fatalf("Save %d: %v", i, err)
			}
			paths = append(paths, filepath.Base(p))
		}
		want := []string{
			"9f57198c-19d3-4ba0-a48d-ba4bcaccf9f0.png",
			"9f57198c-19d3-4ba0-a48d-ba4bcaccf9f0.png.1",
			"9f57198c-19d3-4ba0-a48d-ba4bcaccf9f0.png.2",
		}
		for i := range want {
			if paths[i] != want[i] {
				t.Errorf("download %d landed on %s, want %s", i, paths[i], want[i])
			}
		}
	})

	t.Run("no-clobber applies to an exact target too", func(t *testing.T) {
		dir := t.TempDir()
		exact := filepath.Join(dir, "chosen.bin")
		if err := os.WriteFile(exact, []byte("existing"), 0o644); err != nil {
			t.Fatal(err)
		}
		c, _ := testClient(t, redirectTo("/presigned.png?X-Amz-Signature=abc", []byte("data")))
		got, err := c.Save(assetRef(t), Dest{Exact: exact, NoClobber: true})
		if err != nil {
			t.Fatalf("Save: %v", err)
		}
		if got != exact+".1" {
			t.Fatalf("got %s, want %s", got, exact+".1")
		}
		body, err := os.ReadFile(exact)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != "existing" {
			t.Errorf("the named target was overwritten despite --no-clobber: %q", body)
		}
	})

	t.Run("no-clobber refuses to follow a symlink", func(t *testing.T) {
		dir, outside := t.TempDir(), t.TempDir()
		victim := filepath.Join(outside, "victim")
		if err := os.WriteFile(victim, []byte("PRECIOUS"), 0o644); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "9f57198c-19d3-4ba0-a48d-ba4bcaccf9f0.png")
		if err := os.Symlink(victim, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		c, _ := testClient(t, redirectTo("/presigned.png?X-Amz-Signature=abc", []byte("data")))
		got, err := c.Save(assetRef(t), Dest{Dir: dir, NoClobber: true})
		if err != nil {
			t.Fatalf("Save: %v", err)
		}
		if !strings.HasSuffix(got, ".1") {
			t.Errorf("wrote %s, expected the symlink to be skipped for a .1 suffix", got)
		}
		body, err := os.ReadFile(victim)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != "PRECIOUS" {
			t.Errorf("wrote through the symlink: victim is now %q", body)
		}
	})
}

func TestSaveLeavesNothingBehindOnFailure(t *testing.T) {
	t.Run("nothing is created when the resolve leg fails", func(t *testing.T) {
		dir := t.TempDir()
		c, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		if _, err := c.Save(assetRef(t), Dest{Dir: dir}); err == nil {
			t.Fatal("expected an error")
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("a failed resolve left %d files behind", len(entries))
		}
	})

	t.Run("a partial write is removed", func(t *testing.T) {
		dir := t.TempDir()
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/presigned.png?X-Amz-Signature=abc", http.StatusFound)
		})
		mux.HandleFunc("/presigned.png", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "50")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("partial"))
		})
		c, _ := testClient(t, mux)
		if _, err := c.Save(assetRef(t), Dest{Dir: dir}); err == nil {
			t.Fatal("expected a short-read error")
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("a truncated download was left in place: %v", entries[0].Name())
		}
	})
}

func TestStreamWritesBodyToWriter(t *testing.T) {
	c, _ := testClient(t, redirectTo("/presigned.png?X-Amz-Signature=abc", []byte("payload")))

	var sb strings.Builder
	n, err := c.Stream(assetRef(t), &sb)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if sb.String() != "payload" || n != int64(len("payload")) {
		t.Fatalf("got %q (%d bytes)", sb.String(), n)
	}
}

func TestRefPathRebuildsFromParts(t *testing.T) {
	if got := assetRef(t).path(); got != "/user-attachments/assets/9f57198c-19d3-4ba0-a48d-ba4bcaccf9f0" {
		t.Errorf("asset path = %s", got)
	}
	if got := fileRef(t).path(); got != "/user-attachments/files/30473702/notes.txt" {
		t.Errorf("file path = %s", got)
	}
}

func TestProductionClientDefaults(t *testing.T) {
	c := NewClient(nil, func() (*http.Cookie, error) {
		return &http.Cookie{Name: "user_session", Value: "x"}, nil
	}, nil)
	if c.baseURL != "https://github.com" {
		t.Errorf("baseURL = %s", c.baseURL)
	}
	// Neither leg may follow a redirect on its own.
	for name, cl := range map[string]*http.Client{"resolve": c.resolveClient, "fetch": c.fetchClient} {
		if cl.CheckRedirect == nil {
			t.Errorf("%s client follows redirects", name)
			continue
		}
		if err := cl.CheckRedirect(&http.Request{URL: &url.URL{}}, nil); !errors.Is(err, http.ErrUseLastResponse) {
			t.Errorf("%s client CheckRedirect = %v", name, err)
		}
	}
	if c.resolveClient.Timeout == c.fetchClient.Timeout {
		t.Error("expected the transfer leg to get a longer timeout than the header-only leg")
	}
}

func TestLoginRedirectHostVariants(t *testing.T) {
	for _, loc := range []string{"/login", "https://github.com/login", "https://GITHUB.COM/login", "https://github.com:443/login"} {
		c := NewClient(nil, func() (*http.Cookie, error) {
			return &http.Cookie{Name: "user_session", Value: "x"}, nil
		}, nil)
		_, err := c.classifyRedirect(loc)
		if !errors.Is(err, errExpiredSession) {
			t.Errorf("Location %q: got %v, want errExpiredSession", loc, err)
		}
	}
	for _, loc := range []string{
		"https://attacker.example/login",
		"//attacker.example/login",
		// Protocol-relative with a valid-looking signature: inherits our scheme,
		// so only an explicit rejection keeps it out of the fetch leg.
		"//attacker.example/assets?X-Amz-Signature=abc",
		"https://github.com/settings",
	} {
		c := NewClient(nil, func() (*http.Cookie, error) {
			return &http.Cookie{Name: "user_session", Value: "x"}, nil
		}, nil)
		_, err := c.classifyRedirect(loc)
		if err == nil || errors.Is(err, errExpiredSession) {
			t.Errorf("Location %q: got %v, want a hard error", loc, err)
		}
	}
}

// routeRecorder answers the resolve leg per credential, so a test can say which
// route GitHub would accept.
type routeRecorder struct {
	bearerCalls, cookieCalls int
	bearerOK, cookieOK       bool
}

func (rr *routeRecorder) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("X-Amz-Signature") != "" {
			_, _ = w.Write([]byte("payload"))
			return
		}
		ok := false
		switch {
		case r.Header.Get("Authorization") != "":
			rr.bearerCalls++
			ok = rr.bearerOK
		default:
			rr.cookieCalls++
			ok = rr.cookieOK
		}
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		http.Redirect(w, r, "/presigned.png?X-Amz-Signature=abc", http.StatusFound)
	})
}

func TestRouteBearerFirst(t *testing.T) {
	t.Run("bearer succeeds and the cookie is never resolved", func(t *testing.T) {
		rr := &routeRecorder{bearerOK: true}
		cookieResolved := false
		srv := httptest.NewTLSServer(rr.handler())
		t.Cleanup(srv.Close)
		c := NewClient(
			func() (string, error) { return "gho_test", nil },
			func() (*http.Cookie, error) {
				cookieResolved = true
				return &http.Cookie{Name: "user_session", Value: testToken}, nil
			}, func(string) {})
		c.baseURL = srv.URL
		c.resolveClient.Transport = srv.Client().Transport
		c.fetchClient.Transport = srv.Client().Transport

		if _, err := c.Stream(assetRef(t), io.Discard); err != nil {
			t.Fatalf("Stream: %v", err)
		}
		if rr.bearerCalls != 1 || rr.cookieCalls != 0 {
			t.Errorf("bearer=%d cookie=%d, want 1/0", rr.bearerCalls, rr.cookieCalls)
		}
		// The whole point of resolving lazily: a run on the fast path must never
		// touch the browser's cookie store, and so never prompt for it.
		if cookieResolved {
			t.Error("the session was resolved despite the bearer route succeeding")
		}
	})

	t.Run("a bearer 404 falls back to the cookie and notifies once", func(t *testing.T) {
		rr := &routeRecorder{bearerOK: false, cookieOK: true}
		var notes []string
		srv := httptest.NewTLSServer(rr.handler())
		t.Cleanup(srv.Close)
		c := NewClient(
			func() (string, error) { return "gho_test", nil },
			func() (*http.Cookie, error) { return &http.Cookie{Name: "user_session", Value: testToken}, nil },
			func(m string) { notes = append(notes, m) })
		c.baseURL = srv.URL
		c.resolveClient.Transport = srv.Client().Transport
		c.fetchClient.Transport = srv.Client().Transport

		for i := 0; i < 3; i++ {
			if _, err := c.Stream(assetRef(t), io.Discard); err != nil {
				t.Fatalf("Stream %d: %v", i, err)
			}
		}
		// The rejection is remembered, so three URLs cost one bearer attempt.
		if rr.bearerCalls != 1 {
			t.Errorf("bearer attempted %d times, want 1 (the rejection should be remembered)", rr.bearerCalls)
		}
		if rr.cookieCalls != 3 {
			t.Errorf("cookie used %d times, want 3", rr.cookieCalls)
		}
		if len(notes) != 1 {
			t.Errorf("notified %d times, want exactly 1: %v", len(notes), notes)
		}
	})

	t.Run("both routes failing reports the ambiguous 404", func(t *testing.T) {
		rr := &routeRecorder{}
		srv := httptest.NewTLSServer(rr.handler())
		t.Cleanup(srv.Close)
		c := NewClient(
			func() (string, error) { return "gho_test", nil },
			func() (*http.Cookie, error) { return &http.Cookie{Name: "user_session", Value: testToken}, nil },
			func(string) {})
		c.baseURL = srv.URL
		c.resolveClient.Transport = srv.Client().Transport

		_, err := c.Stream(assetRef(t), io.Discard)
		if !errors.Is(err, errNoAccess) {
			t.Fatalf("got %v, want errNoAccess", err)
		}
		if rr.bearerCalls != 1 || rr.cookieCalls != 1 {
			t.Errorf("bearer=%d cookie=%d, want each tried once", rr.bearerCalls, rr.cookieCalls)
		}
	})

	t.Run("an unavailable gh token falls through silently", func(t *testing.T) {
		rr := &routeRecorder{cookieOK: true}
		var notes []string
		srv := httptest.NewTLSServer(rr.handler())
		t.Cleanup(srv.Close)
		c := NewClient(
			func() (string, error) { return "", errors.New("gh auth token: not logged in") },
			func() (*http.Cookie, error) { return &http.Cookie{Name: "user_session", Value: testToken}, nil },
			func(m string) { notes = append(notes, m) })
		c.baseURL = srv.URL
		c.resolveClient.Transport = srv.Client().Transport
		c.fetchClient.Transport = srv.Client().Transport

		if _, err := c.Stream(assetRef(t), io.Discard); err != nil {
			t.Fatalf("Stream: %v", err)
		}
		if rr.bearerCalls != 0 || rr.cookieCalls != 1 {
			t.Errorf("bearer=%d cookie=%d, want 0/1", rr.bearerCalls, rr.cookieCalls)
		}
		// No message: the route was never usable, so there is no fallback to report.
		if len(notes) != 0 {
			t.Errorf("notified about a route that never ran: %v", notes)
		}
	})

	t.Run("a bearer login redirect falls back instead of blaming the session", func(t *testing.T) {
		// GitHub answering the bearer request with /login says the credential is
		// wrong, not that the asset is missing. Surfacing errExpiredSession here
		// would name a session token the bearer route never used, and would skip
		// the route that might actually work.
		var bearerCalls int
		var notes []string
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("X-Amz-Signature") != "" {
				_, _ = w.Write([]byte("payload"))
				return
			}
			if r.Header.Get("Authorization") != "" {
				bearerCalls++
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}
			http.Redirect(w, r, "/presigned.png?X-Amz-Signature=abc", http.StatusFound)
		}))
		t.Cleanup(srv.Close)
		c := NewClient(
			func() (string, error) { return "gho_test", nil },
			func() (*http.Cookie, error) { return &http.Cookie{Name: "user_session", Value: testToken}, nil },
			func(m string) { notes = append(notes, m) })
		c.baseURL = srv.URL
		c.resolveClient.Transport = srv.Client().Transport
		c.fetchClient.Transport = srv.Client().Transport

		for i := 0; i < 2; i++ {
			if _, err := c.Stream(assetRef(t), io.Discard); err != nil {
				t.Fatalf("Stream %d: %v", i, err)
			}
		}
		if bearerCalls != 1 {
			t.Errorf("bearer attempted %d times, want 1 (the rejection should be remembered)", bearerCalls)
		}
		if len(notes) != 1 {
			t.Errorf("notified %d times, want 1: %v", len(notes), notes)
		}
	})

	t.Run("a nil bearer constructor pins the run to the cookie", func(t *testing.T) {
		rr := &routeRecorder{cookieOK: true}
		c, _ := routedClient(t, rr.handler(), nil)
		if _, err := c.Stream(assetRef(t), io.Discard); err != nil {
			t.Fatalf("Stream: %v", err)
		}
		if rr.bearerCalls != 0 {
			t.Errorf("bearer attempted %d times despite being pinned off", rr.bearerCalls)
		}
		if !errors.Is(c.disabled, errExplicitSessionToken) {
			t.Errorf("disabled = %v, want errExplicitSessionToken", c.disabled)
		}
	})

	t.Run("a non-404 failure is not retried on the other route", func(t *testing.T) {
		var bearer, cookie int
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "" {
				bearer++
			} else {
				cookie++
			}
			w.WriteHeader(http.StatusInternalServerError)
		}))
		t.Cleanup(srv.Close)
		c := NewClient(
			func() (string, error) { return "gho_test", nil },
			func() (*http.Cookie, error) { return &http.Cookie{Name: "user_session", Value: testToken}, nil },
			func(string) {})
		c.baseURL = srv.URL
		c.resolveClient.Transport = srv.Client().Transport

		_, err := c.Stream(assetRef(t), io.Discard)
		if err == nil || !strings.Contains(err.Error(), "500") {
			t.Fatalf("got %v, want the 500 surfaced", err)
		}
		// A server error says nothing about the credential, so switching routes
		// would just repeat the failure and burn the fast path for the run.
		if cookie != 0 {
			t.Errorf("fell back to the cookie on a 500 (bearer=%d cookie=%d)", bearer, cookie)
		}
	})
}
