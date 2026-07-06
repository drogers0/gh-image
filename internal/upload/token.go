package upload

import (
	"fmt"
	"io"
	"net/http"
	"regexp"

	"github.com/drogers0/gh-image/internal/httputil"
)

var uploadTokenRe = regexp.MustCompile(`"uploadToken":"([^"]+)"`)

// isSAMLProtected reports whether the repo page is a SAML SSO "Sign in to
// <owner>" interstitial rather than the real repo page. When an organization
// enforces SAML SSO and the browser session is authenticated but not
// SSO-authorized for that org, GitHub serves this interstitial with HTTP 200 —
// so the uploadToken is absent even though the user has write access.
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
		// Distinguish the common SAML-SSO case from a genuine lack of access, so
		// the user isn't wrongly told to check their permissions.
		if isSAMLProtected(body, owner) {
			return "", fmt.Errorf("%s enforces SAML SSO and your session is not authorized for it — "+
				"authorize in a browser at https://github.com/orgs/%s/sso (lasts ~24h), then retry. "+
				"Write access alone is not enough", owner, owner)
		}
		return "", fmt.Errorf("uploadToken not found on repo page — do you have write access to %s/%s? "+
			"(or, if %s enforces SAML SSO, authorize at https://github.com/orgs/%s/sso)",
			owner, repo, owner, owner)
	}

	return string(match[1]), nil
}
