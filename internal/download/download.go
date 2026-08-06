// Package download fetches GitHub user-attachments assets.
//
// An attachment URL on github.com answers with a 302 to a presigned storage
// URL. The two legs have opposite credential requirements: the first needs the
// session cookies, the second must carry none at all — the presigned URL is
// its own capability, and an Authorization header on the S3 bucket is rejected
// with a 400. The legs therefore use separate clients, and neither follows
// redirects: the redirect is classified explicitly so a login interstitial can
// never be mistaken for asset bytes.
//
// The resolve leg has two routes, mirroring internal/upload: the gh CLI's bearer
// token first, the browser session as fallback. Unlike upload there is no
// content-type split — one credential reaches every attachment — so a single
// rejection disables the fast route for the rest of the run.
package download

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/drogers0/gh-image/internal/cookies"
	"github.com/drogers0/gh-image/internal/httputil"
)

// Kind distinguishes the two user-attachments URL shapes. GitHub routes images
// and videos to /assets/<uuid>, which carries no filename, and everything else
// to /files/<id>/<name>, which does.
type Kind int

const (
	KindAsset Kind = iota
	KindFile
)

// Ref is a validated attachment URL.
type Ref struct {
	URL  string
	Kind Kind
	ID   string // uuid for KindAsset, numeric id for KindFile
	Name string // filename segment for KindFile; empty for KindAsset
}

// path rebuilds the URL path from the validated parts, so a request can be
// issued against Client.baseURL instead of the raw URL. Only ParseRef-approved
// components reach it.
func (r Ref) path() string {
	if r.Kind == KindFile {
		return "/user-attachments/files/" + r.ID + "/" + r.Name
	}
	return "/user-attachments/assets/" + r.ID
}

// Dest says where a download goes. Exactly one of Dir or Exact is set: Dir for
// a derived name, Exact for a caller-supplied file path. Streaming to stdout is
// not a Dest — that path calls Stream.
type Dest struct {
	Dir       string
	Exact     string
	NoClobber bool
}

var (
	uuidRe  = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	digitRe = regexp.MustCompile(`^[0-9]+$`)
)

// ParseRef validates one github.com user-attachments URL.
func ParseRef(raw string) (Ref, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return Ref{}, fmt.Errorf("not a URL: %s", raw)
	}
	switch {
	case u.Scheme != "https":
		return Ref{}, fmt.Errorf("must be an https URL: %s", raw)
	case u.Host != "github.com":
		return Ref{}, fmt.Errorf("must be a github.com URL (got host %q): %s", u.Host, raw)
	case u.User != nil:
		return Ref{}, fmt.Errorf("URL must not contain credentials: %s", raw)
	}

	// Split the path rather than pattern-matching it, so a trailing slash or an
	// extra segment is rejected rather than silently tolerated.
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) < 3 || parts[0] != "user-attachments" {
		return Ref{}, fmt.Errorf("not a user-attachments URL: %s", raw)
	}
	switch parts[1] {
	case "assets":
		if len(parts) != 3 || !uuidRe.MatchString(parts[2]) {
			return Ref{}, fmt.Errorf("expected /user-attachments/assets/<uuid>: %s", raw)
		}
		return Ref{URL: u.String(), Kind: KindAsset, ID: parts[2]}, nil
	case "files":
		if len(parts) != 4 || !digitRe.MatchString(parts[2]) || parts[3] == "" {
			return Ref{}, fmt.Errorf("expected /user-attachments/files/<id>/<name>: %s", raw)
		}
		return Ref{URL: u.String(), Kind: KindFile, ID: parts[2], Name: parts[3]}, nil
	default:
		return Ref{}, fmt.Errorf("not a user-attachments asset or file URL: %s", raw)
	}
}

// credential applies one authentication scheme to the resolve leg.
type credential func(*http.Request)

// Client fetches attachments, preferring the gh CLI's bearer token and falling
// back to the browser session.
type Client struct {
	resolveClient *http.Client
	fetchClient   *http.Client
	// baseURL has no trailing slash; the production value is
	// "https://github.com". Tests point it at an httptest server, matching
	// upload.Client.
	baseURL string

	// Both constructors are memoized, so the gh token is fetched once and the
	// browser session resolved once however many URLs a run holds.
	bearer func() (credential, error) // nil when an explicit session token pins the cookie route
	cookie func() (credential, error)
	notify func(string)

	disabled error // non-nil once the bearer route is off for the run
	notified bool
}

// errExplicitSessionToken disables the bearer route when the user named the
// account to download as. It reports the source, never the token value.
var errExplicitSessionToken = errors.New("explicit session token supplied")

// errNoAccess is the resolve leg's 404: GitHub returns it both for an absent
// asset and for one the credential cannot read, so it is the signal to try the
// other route before giving up.
var errNoAccess = errors.New("not found: the asset does not exist, or the credential used has no access to the repository it belongs to")

func bearerCredential(token string) credential {
	return func(req *http.Request) { req.Header.Set("Authorization", "Bearer "+token) }
}

func cookieCredential(sessionCookie *http.Cookie) credential {
	return func(req *http.Request) {
		for _, ck := range cookies.SessionCookiePair(sessionCookie) {
			req.AddCookie(ck)
		}
	}
}

// noRedirect keeps both legs from following anything on their own: the resolve
// leg classifies its redirect (see resolveRef), and the fetch leg must not hop
// past that classification.
func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// NewClient builds both HTTP clients and both credential routes. Pass a nil
// newBearer to pin the run to the session cookie. The resolve leg only ever
// moves headers, so it takes the same 30s cap as the upload flow's GitHub
// calls; the fetch leg moves the file and takes the 120s that
// internal/upload/s3.go uses for the same payload in the other direction.
func NewClient(newBearer func() (string, error), newCookie func() (*http.Cookie, error), notify func(string)) *Client {
	c := &Client{
		resolveClient: &http.Client{Timeout: 30 * time.Second, CheckRedirect: noRedirect},
		fetchClient:   &http.Client{Timeout: 120 * time.Second, CheckRedirect: noRedirect},
		baseURL:       "https://github.com",
		notify:        notify,
		cookie: sync.OnceValues(func() (credential, error) {
			ck, err := newCookie()
			if err != nil {
				return nil, err
			}
			return cookieCredential(ck), nil
		}),
	}
	if newBearer == nil {
		c.disabled = errExplicitSessionToken
	} else {
		c.bearer = sync.OnceValues(func() (credential, error) {
			token, err := newBearer()
			if err != nil {
				return nil, err
			}
			return bearerCredential(token), nil
		})
	}
	return c
}

// errExpiredSession reports a credential GitHub has actively rejected, as
// opposed to an asset that may simply not exist.
var errExpiredSession = errors.New("session token is invalid or expired — re-run `gh image extract-token`")

// resolveRef asks GitHub where the asset lives and returns the presigned URL,
// trying the bearer route first and the session cookie second.
//
// Two outcomes trigger the fallback, because both say something about the
// credential rather than the asset. A 404 is deliberately ambiguous: GitHub
// answers the same way for an asset that does not exist and one the credential
// cannot read. A login redirect is unambiguous — that credential is not valid.
// Either way the session is tried once, and only then does the run report
// failure. The cost of a genuinely absent asset is one extra request.
func (c *Client) resolveRef(ref Ref) (string, error) {
	if c.disabled == nil {
		cred, err := c.bearer()
		if err != nil {
			// gh missing or unauthenticated: nothing to report, just use the
			// session for the rest of the run.
			c.disableBearer(err, false)
		} else {
			presigned, err := c.attempt(ref, cred)
			if err == nil {
				return presigned, nil
			}
			// Both of these say something about the credential rather than the
			// asset, so both mean "try the other route". errExpiredSession in
			// particular must not escape from here: it names a session token the
			// bearer route never used.
			if !errors.Is(err, errNoAccess) && !errors.Is(err, errExpiredSession) {
				return "", err
			}
			c.disableBearer(err, true)
		}
	}

	cred, err := c.cookie()
	if err != nil {
		return "", err
	}
	return c.attempt(ref, cred)
}

// disableBearer turns the fast route off for the rest of the run. announce is
// false when the route was never usable, so a machine without gh does not get a
// message about a fallback it never left.
func (c *Client) disableBearer(reason error, announce bool) {
	c.disabled = reason
	if announce && !c.notified && c.notify != nil {
		c.notified = true
		c.notify("Note: gh token cannot read this attachment; using browser session.")
	}
}

// attempt performs one resolve request with the given credential.
func (c *Client) attempt(ref Ref, cred credential) (string, error) {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+ref.path(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", httputil.UserAgent)
	cred(req)

	resp, err := c.resolveClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode >= 300 && resp.StatusCode <= 399:
		return c.classifyRedirect(resp.Header.Get("Location"))
	case resp.StatusCode == http.StatusNotFound:
		return "", errNoAccess
	default:
		return "", fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
}

// classifyRedirect decides what a redirect off github.com means. The order is
// load-bearing: a login redirect must be recognised before the presigned check,
// or an expired session reports as an unusable-target error instead.
func (c *Client) classifyRedirect(location string) (string, error) {
	if location == "" {
		return "", fmt.Errorf("redirect without a Location header")
	}
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return "", err
	}
	raw, err := url.Parse(location)
	if err != nil {
		return "", fmt.Errorf("unparseable redirect target %q", location)
	}
	// A protocol-relative target ("//host/path") carries no scheme of its own, so
	// resolving it would inherit ours and let it satisfy the presigned check below
	// while pointing at an arbitrary host. A real redirect here is either a
	// root-relative path or a fully absolute URL.
	if raw.Scheme == "" && raw.Host != "" {
		return "", fmt.Errorf("redirect target is not a presigned asset URL: %s", location)
	}
	target := base.ResolveReference(raw)

	// Only a /login on GitHub's own host means "your credential is stale".
	// Matching the path alone would let any host drive that conclusion.
	if strings.EqualFold(target.Hostname(), base.Hostname()) && target.Path == "/login" {
		return "", errExpiredSession
	}
	// Anything that is not a presigned storage URL is refused rather than
	// fetched: an SSO or error page would otherwise be written to disk as a
	// perfectly plausible attachment.
	if target.Scheme != base.Scheme || target.Query().Get("X-Amz-Signature") == "" {
		return "", fmt.Errorf("redirect target is not a presigned asset URL: %s", target.Redacted())
	}
	return target.String(), nil
}

// fetchAsset retrieves the presigned URL, deliberately sending no credentials of
// any kind. The caller closes the body. Returning the response rather than
// writing it lets Save open its destination only once a 200 is in hand.
func (c *Client) fetchAsset(presigned string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, presigned, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", httputil.UserAgent)

	resp, err := c.fetchClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("fetching asset: expected 200, got %d", resp.StatusCode)
	}
	return resp, nil
}

// copyBody streams the response and rejects a short read, so a truncated
// transfer never passes for a complete file.
func copyBody(dst io.Writer, resp *http.Response) (int64, error) {
	n, err := io.Copy(dst, resp.Body)
	if err != nil {
		return n, err
	}
	if resp.ContentLength >= 0 && n != resp.ContentLength {
		return n, fmt.Errorf("short read: got %d bytes, expected %d", n, resp.ContentLength)
	}
	return n, nil
}

// derivedName picks the filename for a ref. /files/ URLs carry theirs and
// GitHub validates it, so no response header is consulted; /assets/ URLs carry
// only a uuid, and the extension has to come out of the presigned path.
func derivedName(ref Ref, presigned string) string {
	if ref.Kind == KindFile {
		if base := filepath.Base(ref.Name); usableName(base) {
			return base
		}
		return ref.ID
	}
	name := ref.ID
	if u, err := url.Parse(presigned); err == nil {
		name += path.Ext(u.Path)
	}
	return name
}

// usableName rejects the results of filepath.Base that would resolve somewhere
// other than a file inside the destination directory, or that the OS cannot
// open. A percent-encoded NUL survives URL decoding, and os.OpenFile would
// otherwise fail with a bare "invalid argument" instead of falling back to the
// asset id.
func usableName(name string) bool {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return false
	}
	return !strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// open creates the destination. A derived name is joined under dest.Dir through
// filepath.Base so a traversal-shaped name cannot escape it. Without
// NoClobber the file is truncated, matching curl -O; with it, candidates gain
// a .1, .2 suffix until an exclusive create succeeds, matching curl's
// --no-clobber.
func open(dest Dest, name string) (*os.File, string, error) {
	target := dest.Exact
	if target == "" {
		target = filepath.Join(dest.Dir, filepath.Base(name))
	}
	if !dest.NoClobber {
		// O_TRUNC follows a symlink at the target, matching curl -O. That is the
		// intended overwrite semantic: --no-clobber is the flag for "never replace
		// what is already there", and it refuses symlinks via O_EXCL below.
		f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, "", err
		}
		return f, target, nil
	}
	// O_EXCL is what makes this safe as well as non-clobbering: it also
	// refuses to follow a symlink sitting at the candidate path.
	for i := 0; ; i++ {
		candidate := target
		if i > 0 {
			candidate = fmt.Sprintf("%s.%d", target, i)
		}
		f, err := os.OpenFile(candidate, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			return f, candidate, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, "", err
		}
	}
}

// Save resolves ref and writes it, returning the path written. The destination
// is created only after the fetch returns 200, so a failed request leaves no
// empty file behind, and a partial write is removed rather than left in place.
func (c *Client) Save(ref Ref, dest Dest) (string, error) {
	presigned, err := c.resolveRef(ref)
	if err != nil {
		return "", err
	}
	resp, err := c.fetchAsset(presigned)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	f, written, err := open(dest, derivedName(ref, presigned))
	if err != nil {
		return "", err
	}
	if _, err := copyBody(f, resp); err != nil {
		_ = f.Close()
		_ = os.Remove(written)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(written)
		return "", fmt.Errorf("closing %s: %w", written, err)
	}
	return written, nil
}

// Stream resolves ref and writes its bytes to w. Nothing is created on disk.
func (c *Client) Stream(ref Ref, w io.Writer) (int64, error) {
	presigned, err := c.resolveRef(ref)
	if err != nil {
		return 0, err
	}
	resp, err := c.fetchAsset(presigned)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	return copyBody(w, resp)
}
