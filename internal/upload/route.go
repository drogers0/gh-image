package upload

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Router uploads through the bearer endpoint when it can and the
// browser-session flow when it cannot.
//
// Nothing about GitHub's allowlist or permission rule is encoded here: the
// bearer route is simply attempted, and the server's answer decides. What is
// remembered is only what that answer scopes to — a rejected content type, or a
// rejection that holds for the whole run — so a multi-file run never asks the
// same question twice. Transient failures are deliberately not remembered; the
// 100-continue handshake makes retrying them nearly free, while memoizing one
// would push every remaining file onto the cookie route, and its keychain
// prompt, over a blip.
type Router struct {
	newBearer func() (*BearerClient, error) // nil when an explicit session token pins the cookie route
	newCookie func() (*Client, error)
	notify    func(string)

	bearer   *BearerClient
	rejected map[string]error // content type -> why the endpoint refused it
	disabled error            // non-nil once the bearer route is off for the run

	cookie    *Client
	cookieErr error
	notified  bool
}

// errExplicitSessionToken disables the bearer route when the user named the
// account to upload as. It reports the source, never the token value.
var errExplicitSessionToken = errors.New("explicit session token supplied")

func NewRouter(newBearer func() (*BearerClient, error), newCookie func() (*Client, error), notify func(string)) *Router {
	r := &Router{
		newBearer: newBearer,
		newCookie: newCookie,
		notify:    notify,
		rejected:  map[string]error{},
	}
	if newBearer == nil {
		r.disabled = errExplicitSessionToken
	}
	return r
}

// Upload returns the asset reference for one file. run() uploads serially, so
// the memo needs no locking.
func (r *Router) Upload(owner, repo string, repoID int, path string) (*Result, error) {
	contentType := detectContentType(path)

	reason := r.disabled
	if reason == nil {
		reason = r.rejected[contentType]
	}
	if reason == nil {
		result, err := r.tryBearer(repoID, path)
		if err == nil {
			return result, nil
		}
		r.remember(contentType, err)
		reason = err
	}

	// Announce before resolving the session, so the explanation precedes any
	// keychain prompt rather than trailing it.
	if !r.notified {
		r.notified = true
		r.notify(fmt.Sprintf("Note: fast upload unavailable (%s); using browser session.", reason))
	}

	if r.cookie == nil && r.cookieErr == nil {
		r.cookie, r.cookieErr = r.newCookie()
	}
	if r.cookieErr != nil {
		return nil, composeError(reason, r.cookieErr)
	}

	// Only client construction is memoized: every file gets its own attempt, so
	// one flaky upload does not poison the rest of the run.
	result, err := r.cookie.Upload(owner, repo, repoID, path)
	if err != nil {
		return nil, composeError(reason, err)
	}
	return result, nil
}

// tryBearer builds the bearer client on first use and attempts the upload.
func (r *Router) tryBearer(repoID int, path string) (*Result, error) {
	if r.bearer == nil {
		bearer, err := r.newBearer()
		if err != nil {
			// gh auth token will not start succeeding mid-run.
			r.disabled = err
			return nil, err
		}
		r.bearer = bearer
	}
	return r.bearer.Upload(repoID, path)
}

// remember records a failure only when it says something durable. A 422 naming
// content_type is scoped to that type; 400/401/403/404 hold for the run; a 429,
// a 5xx, a transport error or an unusable 201 say nothing about the next file.
func (r *Router) remember(contentType string, err error) {
	var status *StatusError
	if !errors.As(err, &status) {
		return
	}
	switch status.Code {
	case http.StatusUnprocessableEntity:
		if strings.Contains(status.Body, "content_type") {
			r.rejected[contentType] = err
		}
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		r.disabled = err
	}
}

// composeError names both routes: the bearer failure is uninterpretable alone,
// and the cookie failure carries the actionable detail, so it is the one wrapped.
func composeError(bearerErr, cookieErr error) error {
	return fmt.Errorf("fast upload failed (%v); browser-session upload failed: %w", bearerErr, cookieErr)
}
