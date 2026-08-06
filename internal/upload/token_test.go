package upload

import (
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

func TestIsSignInInterstitial(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{
			name: "sign-in interstitial served for an invalid session",
			body: `<title>Sign in to GitHub · GitHub</title><form action="/session">`,
			want: true,
		},
		{
			// The real repo page always embeds currentUser, as null when the
			// request carries no session at all.
			name: "anonymous repo page must NOT match",
			body: `<title>GitHub - octocat/hello: hi</title>{"currentUser":null}`,
			want: false,
		},
		{
			name: "signed-in repo page must NOT match",
			body: `<title>GitHub - octocat/hello: hi</title>{"currentUser":{"login":"octocat"}}`,
			want: false,
		},
		{
			// A repo whose description mentions signing in: the real title starts
			// with "GitHub - <owner>/<repo>", so the anchored match cannot fire.
			name: "repo about signing in must NOT match",
			body: `<title>GitHub - acme/auth: Sign in to GitHub from the CLI</title>`,
			want: false,
		},
		{
			// The org SSO interstitial is a different failure with a different fix;
			// it must fall through to isSAMLProtected.
			name: "SAML interstitial must NOT match",
			body: `<title>Sign in to GymPod</title><a href="/orgs/GymPod/sso">x</a>`,
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSignInInterstitial([]byte(tc.body)); got != tc.want {
				t.Errorf("isSignInInterstitial(%q) = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}

func TestGetUploadToken(t *testing.T) {
	cases := []struct {
		name        string
		owner       string
		status      int      // response status (0 => 200)
		body        string   // response body
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
			name:        "SAML interstitial gives an actionable SSO error, not an access error",
			owner:       "GymPod",
			body:        `<title>Sign in to GymPod</title><a href="/orgs/GymPod/sso">Single sign-on</a>`,
			errContains: []string{"SAML SSO", "/orgs/GymPod/sso", "Repository access alone is not enough"},
			errExcludes: []string{"can you view GymPod"},
		},
		{
			name:        "expired session gives a re-extract error, not an access error",
			owner:       "octocat",
			body:        `<title>Sign in to GitHub · GitHub</title>`,
			errContains: []string{"invalid or expired", "gh image extract-token"},
			errExcludes: []string{"can you view octocat/hello", "SAML"},
		},
		{
			// Owner "github" makes the sign-in title satisfy isSAMLProtected too;
			// the stale-session branch must still win.
			name:        "expired session on a github-owned repo is not reported as SSO",
			owner:       "github",
			body:        `<title>Sign in to GitHub · GitHub</title>`,
			errContains: []string{"invalid or expired"},
			errExcludes: []string{"SAML"},
		},
		{
			name:        "no token and no interstitial markers gives the generic message",
			owner:       "octocat",
			body:        `<html>just a page, no token</html>`,
			errContains: []string{"can you view octocat/hello"},
		},
		{
			name:        "non-200 status reports the repo page status",
			owner:       "octocat",
			status:      http.StatusNotFound,
			body:        "not found",
			errContains: []string{"repo page returned 404"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newJSONServer(t, tc.status, tc.body)
			tok, err := testClient(srv).getUploadToken(tc.owner, "hello")

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
