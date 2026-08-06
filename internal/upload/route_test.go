package upload

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// bearerStub serves the bearer endpoint, answering each request from responses
// in order (the last entry repeats), and counts what it received.
type bearerStub struct {
	server *httptest.Server
	calls  atomic.Int32
}

type bearerReply struct {
	code int
	body string
}

func newBearerStub(t *testing.T, replies ...bearerReply) *bearerStub {
	t.Helper()
	stub := &bearerStub{}
	stub.server = newServer(t, func(w http.ResponseWriter, r *http.Request) {
		n := int(stub.calls.Add(1)) - 1
		if n >= len(replies) {
			n = len(replies) - 1
		}
		w.WriteHeader(replies[n].code)
		_, _ = w.Write([]byte(replies[n].body))
	})
	return stub
}

func (s *bearerStub) client() *BearerClient {
	c := NewBearerClient("gho_test")
	c.baseURL = s.server.URL
	return c
}

func created(url string) bearerReply {
	return bearerReply{http.StatusCreated, fmt.Sprintf(`{"url":%q}`, url)}
}

var refusedContentType = bearerReply{
	http.StatusUnprocessableEntity,
	`{"errors":[{"field":"content_type","message":"content_type is not included in the list of allowed content types"}]}`,
}

// cookieStub serves the full four-step browser-session flow and counts uploads.
type cookieStub struct {
	server *httptest.Server
	calls  atomic.Int32
	fail   func(call int) bool // when true, the S3 leg fails for that call
}

func newCookieStub(t *testing.T) *cookieStub {
	t.Helper()
	stub := &cookieStub{}
	mux := http.NewServeMux()
	mux.HandleFunc("/octo/hello", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`x={"uploadToken":"TKN"}`))
	})
	mux.HandleFunc("/upload/policies/assets", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(validPolicy(stub.server.URL + "/s3")))
	})
	mux.HandleFunc("/s3", func(w http.ResponseWriter, r *http.Request) {
		call := int(stub.calls.Add(1))
		if stub.fail != nil && stub.fail(call) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/upload/assets/99", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"href":"https://gh/assets/cookie","name":"pic.png"}`))
	})
	stub.server = httptest.NewServer(mux)
	t.Cleanup(stub.server.Close)
	return stub
}

// routerFor wires a Router onto the two stubs, recording notices and the order
// in which the two lazy constructors ran.
type harness struct {
	router       *Router
	notices      []string
	events       []string
	bearerBuilds int
	cookieBuilds int
}

func routerFor(t *testing.T, bearer *bearerStub, cookie *cookieStub) *harness {
	t.Helper()
	h := &harness{}
	var newBearer func() (*BearerClient, error)
	if bearer != nil {
		newBearer = func() (*BearerClient, error) {
			h.bearerBuilds++
			return bearer.client(), nil
		}
	}
	h.router = NewRouter(newBearer, func() (*Client, error) {
		h.cookieBuilds++
		h.events = append(h.events, "cookie")
		return &Client{http: cookie.server.Client(), baseURL: cookie.server.URL}, nil
	}, func(msg string) {
		h.notices = append(h.notices, msg)
		h.events = append(h.events, "notify")
	})
	return h
}

func (h *harness) upload(t *testing.T, path string) (*Result, error) {
	t.Helper()
	return h.router.Upload("octo", "hello", 42, path)
}

// TestRouter_BearerSuccessNeverTouchesSession is the lazy-session guarantee: a
// run that stays on the fast path must not resolve a browser session at all.
func TestRouter_BearerSuccessNeverTouchesSession(t *testing.T) {
	bearer := newBearerStub(t, created("https://gh/assets/fast"))
	cookie := newCookieStub(t)
	h := routerFor(t, bearer, cookie)

	res, err := h.upload(t, writeTempFile(t, "shot.png", "PNG"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.URL != "https://gh/assets/fast" {
		t.Errorf("URL = %q, want the bearer asset", res.URL)
	}
	if h.cookieBuilds != 0 || cookie.calls.Load() != 0 {
		t.Errorf("session was resolved (%d builds, %d uploads) on a successful fast path", h.cookieBuilds, cookie.calls.Load())
	}
	if len(h.notices) != 0 {
		t.Errorf("unexpected notice: %v", h.notices)
	}
}

// TestRouter_NoPushAccessDisablesRun covers the stable-failure memo: a 404 is
// scoped to the token and repository, so later files must not retry.
func TestRouter_NoPushAccessDisablesRun(t *testing.T) {
	bearer := newBearerStub(t, bearerReply{http.StatusNotFound, `{"message":"Not Found"}`})
	cookie := newCookieStub(t)
	h := routerFor(t, bearer, cookie)

	for _, name := range []string{"one.png", "two.png"} {
		if _, err := h.upload(t, writeTempFile(t, name, "PNG")); err != nil {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
	}
	if got := bearer.calls.Load(); got != 1 {
		t.Errorf("bearer attempted %d times, want 1", got)
	}
	if got := cookie.calls.Load(); got != 2 {
		t.Errorf("cookie uploads = %d, want 2", got)
	}
	if h.bearerBuilds != 1 || h.cookieBuilds != 1 {
		t.Errorf("builds: bearer=%d cookie=%d, want 1 and 1", h.bearerBuilds, h.cookieBuilds)
	}
}

// TestRouter_RefusedContentTypeIsScopedToThatType is the reason the memo is
// keyed by content type: a refused PDF must not cost the PNG its fast path.
func TestRouter_RefusedContentTypeIsScopedToThatType(t *testing.T) {
	bearer := newBearerStub(t, refusedContentType, created("https://gh/assets/png"))
	cookie := newCookieStub(t)
	h := routerFor(t, bearer, cookie)

	if _, err := h.upload(t, writeTempFile(t, "report.pdf", "PDF")); err != nil {
		t.Fatalf("pdf: unexpected error: %v", err)
	}
	res, err := h.upload(t, writeTempFile(t, "shot.png", "PNG"))
	if err != nil {
		t.Fatalf("png: unexpected error: %v", err)
	}
	if res.URL != "https://gh/assets/png" {
		t.Errorf("png took the cookie route (URL = %q)", res.URL)
	}
	if got := bearer.calls.Load(); got != 2 {
		t.Errorf("bearer attempted %d times, want 2", got)
	}
	if got := cookie.calls.Load(); got != 1 {
		t.Errorf("cookie uploads = %d, want 1 (the pdf only)", got)
	}
}

func TestRouter_SameContentTypeAskedOnce(t *testing.T) {
	bearer := newBearerStub(t, refusedContentType)
	cookie := newCookieStub(t)
	h := routerFor(t, bearer, cookie)

	for _, name := range []string{"a.pdf", "b.pdf"} {
		if _, err := h.upload(t, writeTempFile(t, name, "PDF")); err != nil {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
	}
	if got := bearer.calls.Load(); got != 1 {
		t.Errorf("bearer attempted %d times, want 1", got)
	}
}

// TestRouter_TransientFailuresAreRetried is the counterpart to the stable memo:
// a blip says nothing about the next file, so the fast path must survive it.
func TestRouter_TransientFailuresAreRetried(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply bearerReply
	}{
		{"server error", bearerReply{http.StatusInternalServerError, `{"message":"oops"}`}},
		{"rate limited", bearerReply{http.StatusTooManyRequests, `{"message":"slow down"}`}},
		{"422 unrelated to content type", bearerReply{http.StatusUnprocessableEntity, `{"errors":[{"field":"size"}]}`}},
		{"unusable success", bearerReply{http.StatusCreated, `{}`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bearer := newBearerStub(t, tc.reply, created("https://gh/assets/second"))
			cookie := newCookieStub(t)
			h := routerFor(t, bearer, cookie)

			if _, err := h.upload(t, writeTempFile(t, "one.png", "PNG")); err != nil {
				t.Fatalf("first: unexpected error: %v", err)
			}
			res, err := h.upload(t, writeTempFile(t, "two.png", "PNG"))
			if err != nil {
				t.Fatalf("second: unexpected error: %v", err)
			}
			if res.URL != "https://gh/assets/second" {
				t.Errorf("second file did not retry the fast path (URL = %q)", res.URL)
			}
			if got := bearer.calls.Load(); got != 2 {
				t.Errorf("bearer attempted %d times, want 2", got)
			}
		})
	}
}

// TestRouter_BearerBuildFailure covers a missing gh token: no attempt is made,
// nothing panics on a nil client, and gh is not re-invoked per file.
func TestRouter_BearerBuildFailure(t *testing.T) {
	bearer := newBearerStub(t, created("https://gh/assets/unused"))
	cookie := newCookieStub(t)
	builds := 0
	var notices []string
	router := NewRouter(
		func() (*BearerClient, error) {
			builds++
			return nil, fmt.Errorf("gh auth token: not logged in")
		},
		func() (*Client, error) {
			return &Client{http: cookie.server.Client(), baseURL: cookie.server.URL}, nil
		},
		func(msg string) { notices = append(notices, msg) },
	)

	for _, name := range []string{"one.png", "two.png"} {
		if _, err := router.Upload("octo", "hello", 42, writeTempFile(t, name, "PNG")); err != nil {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
	}
	if builds != 1 {
		t.Errorf("gh invoked %d times, want 1", builds)
	}
	if got := bearer.calls.Load(); got != 0 {
		t.Errorf("bearer attempted %d times, want 0", got)
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "not logged in") {
		t.Errorf("notices = %v, want one naming the gh failure", notices)
	}
}

// TestRouter_ExplicitSessionTokenPinsCookieRoute covers decision 5: naming the
// uploading account must not be silently overridden by the gh identity. The
// notice names the source, never the token value.
func TestRouter_ExplicitSessionTokenPinsCookieRoute(t *testing.T) {
	bearer := newBearerStub(t, created("https://gh/assets/unused"))
	cookie := newCookieStub(t)
	h := routerFor(t, nil, cookie)

	if _, err := h.upload(t, writeTempFile(t, "shot.png", "PNG")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := bearer.calls.Load(); got != 0 {
		t.Errorf("bearer attempted %d times, want 0", got)
	}
	if len(h.notices) != 1 || !strings.Contains(h.notices[0], "explicit session token supplied") {
		t.Fatalf("notices = %v, want one naming the explicit session token", h.notices)
	}
}

// TestRouter_NoticePrecedesSessionResolution locks the ordering the notice
// exists for: it must land before the keychain prompt that cookie resolution
// can trigger, not after it.
func TestRouter_NoticePrecedesSessionResolution(t *testing.T) {
	bearer := newBearerStub(t, bearerReply{http.StatusNotFound, `{"message":"Not Found"}`})
	cookie := newCookieStub(t)
	h := routerFor(t, bearer, cookie)

	for _, name := range []string{"one.png", "two.png"} {
		if _, err := h.upload(t, writeTempFile(t, name, "PNG")); err != nil {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
	}
	if len(h.events) < 2 || h.events[0] != "notify" || h.events[1] != "cookie" {
		t.Errorf("events = %v, want notify before cookie", h.events)
	}
	if len(h.notices) != 1 {
		t.Errorf("notice printed %d times, want 1", len(h.notices))
	}
	if !strings.Contains(h.notices[0], "HTTP 404") {
		t.Errorf("notice = %q, want it to name the bearer status", h.notices[0])
	}
}

// TestRouter_NoticeOmitsResponseBody keeps the one-line notice readable: the
// endpoint's rejection bodies run to hundreds of characters, and they belong in
// the error returned when the fallback also fails, not on a success path.
func TestRouter_NoticeOmitsResponseBody(t *testing.T) {
	bearer := newBearerStub(t, refusedContentType)
	cookie := newCookieStub(t)
	h := routerFor(t, bearer, cookie)

	if _, err := h.upload(t, writeTempFile(t, "report.pdf", "PDF")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(h.notices) != 1 {
		t.Fatalf("notices = %v, want one", h.notices)
	}
	if want := "Note: fast upload unavailable (HTTP 422); using browser session."; h.notices[0] != want {
		t.Errorf("notice = %q, want %q", h.notices[0], want)
	}
}

// TestRouter_BothRoutesFailNamesBoth covers the diagnosability cost of removing
// the predicate — including for a file that never attempted the fast path
// because an earlier file already tripped the memo.
func TestRouter_BothRoutesFailNamesBoth(t *testing.T) {
	bearer := newBearerStub(t, bearerReply{http.StatusNotFound, `{"message":"Not Found"}`})
	cookie := newCookieStub(t)
	cookie.fail = func(int) bool { return true }
	h := routerFor(t, bearer, cookie)

	for i, name := range []string{"one.png", "two.png"} {
		_, err := h.upload(t, writeTempFile(t, name, "PNG"))
		if err == nil {
			t.Fatalf("%s: expected an error", name)
		}
		if !strings.Contains(err.Error(), "HTTP 404") {
			t.Errorf("file %d error %q does not name the fast-path failure", i, err)
		}
		if !strings.Contains(err.Error(), "step 2 (S3 upload)") {
			t.Errorf("file %d error %q does not name the browser-session failure", i, err)
		}
	}
}

// TestRouter_CookieUploadFailureIsNotSticky separates client construction, which
// is memoized, from a per-file upload failure, which must not be.
func TestRouter_CookieUploadFailureIsNotSticky(t *testing.T) {
	bearer := newBearerStub(t, bearerReply{http.StatusNotFound, `{"message":"Not Found"}`})
	cookie := newCookieStub(t)
	cookie.fail = func(call int) bool { return call == 1 }
	h := routerFor(t, bearer, cookie)

	if _, err := h.upload(t, writeTempFile(t, "one.png", "PNG")); err == nil {
		t.Fatal("first upload: expected an error")
	}
	res, err := h.upload(t, writeTempFile(t, "two.png", "PNG"))
	if err != nil {
		t.Fatalf("second upload: unexpected error: %v", err)
	}
	if res.URL != "https://gh/assets/cookie" {
		t.Errorf("URL = %q, want the cookie-route asset", res.URL)
	}
	if h.cookieBuilds != 1 {
		t.Errorf("cookie client built %d times, want 1", h.cookieBuilds)
	}
}

// TestRouter_CookieBuildFailure covers the memoized construction failure: the
// session is resolved once, and every file reports both routes.
func TestRouter_CookieBuildFailure(t *testing.T) {
	bearer := newBearerStub(t, bearerReply{http.StatusNotFound, `{"message":"Not Found"}`})
	builds := 0
	router := NewRouter(
		func() (*BearerClient, error) { return bearer.client(), nil },
		func() (*Client, error) {
			builds++
			return nil, fmt.Errorf("no session token found")
		},
		func(string) {},
	)

	for _, name := range []string{"one.png", "two.png"} {
		_, err := router.Upload("octo", "hello", 42, writeTempFile(t, name, "PNG"))
		if err == nil || !strings.Contains(err.Error(), "no session token found") {
			t.Fatalf("%s: error = %v, want the session failure", name, err)
		}
	}
	if builds != 1 {
		t.Errorf("session resolved %d times, want 1", builds)
	}
}
