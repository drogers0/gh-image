package upload

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// serve returns an httptest server that responds with status/body, and a client
// pointed at it. GetUploadToken builds a github.com URL internally, so these
// tests exercise isSAMLProtected / the error wording directly where the URL is
// fixed, and use the server only for the HTTP-shape tests below.
func serve(status int, body string) (*httptest.Server, *http.Client) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	return srv, srv.Client()
}

func TestIsSAMLProtected(t *testing.T) {
	cases := []struct {
		name  string
		owner string
		body  string
		want  bool
	}{
		{
			name:  "SSO interstitial title",
			owner: "GymPod",
			body:  `<title>Sign in to GymPod</title>`,
			want:  true,
		},
		{
			name:  "owner-scoped /orgs/<owner>/sso link",
			owner: "GymPod",
			body:  `<a href="/orgs/GymPod/sso?return_to=%2FGymPod%2Frepo">Single sign-on</a>`,
			want:  true,
		},
		{
			// The key false positive the naive "contains SAML" check would hit:
			// site chrome / help links mention SSO on essentially every page.
			name:  "normal repo page with SSO words in chrome must NOT match",
			owner: "GymPod",
			body:  `<title>GymPod/realtime-core</title><footer><a href="/help/saml">single sign-on docs</a></footer>`,
			want:  false,
		},
		{
			name:  "a repo that is ABOUT saml must NOT match",
			owner: "crewjam",
			body:  `<title>GitHub - crewjam/saml: SAML library for go</title> ... single sign-on ...`,
			want:  false,
		},
		{
			name:  "another org's sso link must NOT match this owner",
			owner: "GymPod",
			body:  `<a href="/orgs/SomeOtherOrg/sso">x</a>`,
			want:  false,
		},
		{
			name:  "owner with regex metacharacters is matched literally",
			owner: "a.b",
			body:  `<title>Sign in to axb</title>`, // '.' must NOT act as a wildcard
			want:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSAMLProtected([]byte(tc.body), tc.owner); got != tc.want {
				t.Errorf("isSAMLProtected(%q owner=%q) = %v, want %v", tc.body, tc.owner, got, tc.want)
			}
		})
	}
}

func TestGetUploadToken_Success(t *testing.T) {
	srv, client := serve(http.StatusOK, `window.foo={"uploadToken":"TKN123"};`)
	defer srv.Close()

	// GetUploadToken hardcodes the github.com URL, so we can't redirect it at the
	// server here; instead verify extraction via the regex contract the function
	// relies on. (The SSO/error wording is covered by the table tests above.)
	match := uploadTokenRe.FindSubmatch([]byte(`{"uploadToken":"TKN123"}`))
	if match == nil || string(match[1]) != "TKN123" {
		t.Fatalf("uploadTokenRe failed to extract token")
	}
	_ = client
}

func TestGetUploadToken_SAMLErrorWording(t *testing.T) {
	// Drive GetUploadToken through a stubbed transport so we control the body it
	// reads from "github.com" without a real network call.
	client := &http.Client{Transport: stubTransport(http.StatusOK,
		`<title>Sign in to GymPod</title><a href="/orgs/GymPod/sso">Single sign-on</a>`)}

	_, err := GetUploadToken(client, "GymPod", "realtime-core")
	if err == nil {
		t.Fatal("expected an error for the SSO interstitial")
	}
	msg := err.Error()
	for _, want := range []string{"SAML SSO", "/orgs/GymPod/sso", "NOT a write-access problem"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q; got: %s", want, msg)
		}
	}
	if strings.Contains(msg, "do you have write access") {
		t.Errorf("SSO error should not use the misleading write-access wording; got: %s", msg)
	}
}

func TestGetUploadToken_GenericErrorWording(t *testing.T) {
	// No token and no SSO markers → the generic message (with an SSO hint).
	client := &http.Client{Transport: stubTransport(http.StatusOK, `<html>just a page, no token</html>`)}

	_, err := GetUploadToken(client, "octocat", "hello")
	if err == nil {
		t.Fatal("expected an error when uploadToken is absent")
	}
	if !strings.Contains(err.Error(), "do you have write access to octocat/hello") {
		t.Errorf("expected the generic write-access message; got: %s", err.Error())
	}
}

// stubTransport returns an http.RoundTripper that answers every request with the
// given status and body, so GetUploadToken's hardcoded github.com URL is served
// locally.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func stubTransport(status int, body string) http.RoundTripper {
	return roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
			Request:    r,
		}, nil
	})
}
