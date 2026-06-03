package upload

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

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
			// The key false positive a naive "contains SAML" check would hit:
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
		{
			// GitHub owners are case-insensitive and the page may render a
			// different case than the user typed; the link must still match.
			name:  "lowercase owner matches a canonical-case sso link",
			owner: "gympod",
			body:  `<a href="/orgs/GymPod/sso">Single sign-on</a>`,
			want:  true,
		},
		{
			// Substring of another org's name must not match.
			name:  "owner that is a substring of another org must NOT match",
			owner: "pod",
			body:  `<a href="/orgs/GymPod/sso">x</a>`,
			want:  false,
		},
		{
			// Defensive: an empty owner must never match (and must not panic).
			name:  "empty owner never matches",
			owner: "",
			body:  `<a href="/orgs/Anything/sso"><title>Sign in to </title>`,
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

func TestGetUploadToken(t *testing.T) {
	cases := []struct {
		name        string
		owner       string
		body        string
		wantToken   string   // non-empty => expect success
		errContains []string // substrings the error must include
		errExcludes []string // substrings the error must NOT include
	}{
		{
			name:      "success extracts the token",
			owner:     "octocat",
			body:      `window.x={"uploadToken":"TKN123"};`,
			wantToken: "TKN123",
		},
		{
			name:        "SAML interstitial gives an actionable SSO error, not write-access",
			owner:       "GymPod",
			body:        `<title>Sign in to GymPod</title><a href="/orgs/GymPod/sso">Single sign-on</a>`,
			errContains: []string{"SAML SSO", "/orgs/GymPod/sso", "Write access alone is not enough"},
			errExcludes: []string{"do you have write access to GymPod"},
		},
		{
			name:        "no token and no SSO markers gives the generic message",
			owner:       "octocat",
			body:        `<html>just a page, no token</html>`,
			errContains: []string{"do you have write access to octocat/hello"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: stubTransport(http.StatusOK, tc.body)}
			tok, err := GetUploadToken(client, tc.owner, "hello")

			if tc.wantToken != "" {
				if err != nil {
					t.Fatalf("expected success, got error: %v", err)
				}
				if tok != tc.wantToken {
					t.Errorf("token = %q, want %q", tok, tc.wantToken)
				}
				return
			}

			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			for _, s := range tc.errContains {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("error missing %q; got: %s", s, err.Error())
				}
			}
			for _, s := range tc.errExcludes {
				if strings.Contains(err.Error(), s) {
					t.Errorf("error should not contain %q; got: %s", s, err.Error())
				}
			}
		})
	}
}

// stubTransport answers every request with the given status and body, so
// GetUploadToken's hardcoded github.com URL is served locally without a network
// call.
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
