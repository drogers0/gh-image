package upload

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"regexp"

	"github.com/drogers0/gh-image/internal/httputil"
)

var uploadTokenRe = regexp.MustCompile(`"uploadToken":"([^"]+)"`)

var signInTitleRe = regexp.MustCompile(`(?i)<title>\s*Sign in to GitHub\b`)

// isSignInInterstitial reports whether the page is GitHub's generic "Sign in to
// GitHub" interstitial rather than the real repo page. When the user_session
// cookie is present but invalid or expired, GitHub neither redirects nor errors:
// it serves this page with HTTP 200 and no uploadToken. A request carrying no
// cookie at all still gets the real repo page, so this specifically identifies a
// stale session — the fix is to re-extract the token, not to change permissions.
//
// The title match is anchored at the start of the <title> element: a real repo
// page's title begins "GitHub - <owner>/<repo>: <description>", so a repo whose
// name or description mentions signing in cannot match. We additionally require
// "currentUser" to be absent — the repo page embeds it in its JS payload whether
// or not the request is authenticated (as "currentUser":null when it is not),
// while the sign-in page has no such payload.
func isSignInInterstitial(body []byte) bool {
	return signInTitleRe.Match(body) && !bytes.Contains(body, []byte(`"currentUser"`))
}

// isSAMLProtected reports whether the repo page is a SAML SSO "Sign in to
// <owner>" interstitial rather than the real repo page. When an organization
// enforces SAML SSO and the browser session is authenticated but not
// SSO-authorized for that org, GitHub serves this interstitial with HTTP 200 —
// so the uploadToken is absent even though the user can access the repo.
//
// The SSO authorization is server-side state (it lasts ~24h and is granted only
// by completing the identity-provider handshake in a browser), so it is NOT a
// cookie that can be copied; the fix is to re-authorize at /orgs/<owner>/sso.
//
// We require signals SPECIFIC to the interstitial and scoped to THIS owner. We
// deliberately do NOT match the words "SAML"/"single sign-on" anywhere on the
// page: those appear in GitHub's site chrome/help links on virtually every page
// (and in any repo that is simply about SAML), which would be a false positive.
func isSAMLProtected(body []byte, owner string) bool {
	if owner == "" {
		return false
	}
	// Owners are case-insensitive on GitHub, and the page may render a different
	// case than the user typed, so both checks are case-insensitive.
	o := regexp.QuoteMeta(owner)
	orgSSOLink := regexp.MustCompile(`(?i)/orgs/` + o + `/sso`).Match(body)
	ssoTitle := regexp.MustCompile(`(?i)<title>\s*Sign in to ` + o + `\b`).Match(body)
	return orgSSOLink || ssoTitle
}

// getUploadToken fetches the repo page and extracts the uploadToken
// from the JS payload. Requires authenticated cookies in the client.
func (c *Client) getUploadToken(owner, repo string) (string, error) {
	url := fmt.Sprintf("%s/%s/%s", c.baseURL, owner, repo)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", httputil.UserAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching repo page: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("repo page returned %d — do you have access to %s/%s?", resp.StatusCode, owner, repo)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading repo page: %w", err)
	}

	match := uploadTokenRe.FindSubmatch(body)
	if match == nil {
		// Distinguish an expired session and the SAML-SSO case from a genuine lack
		// of access, so the user isn't wrongly told to check their permissions.
		// The stale-session check runs first: for owner "github", the sign-in
		// interstitial's title would also satisfy isSAMLProtected.
		if isSignInInterstitial(body) {
			return "", fmt.Errorf("GitHub served its sign-in page — your session token is invalid or expired. " +
				"Re-run `gh image extract-token` (or refresh GH_SESSION_TOKEN in CI), then retry")
		}
		if isSAMLProtected(body, owner) {
			return "", fmt.Errorf("%s enforces SAML SSO and your session is not authorized for it — "+
				"authorize in a browser at https://github.com/orgs/%s/sso (lasts ~24h), then retry. "+
				"Repository access alone is not enough", owner, owner)
		}
		return "", fmt.Errorf("uploadToken not found on repo page — can you view %s/%s? "+
			"(or, if %s enforces SAML SSO, authorize at https://github.com/orgs/%s/sso)",
			owner, repo, owner, owner)
	}

	return string(match[1]), nil
}
