package upload

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/drogers0/gh-image/internal/httputil"
)

// bearerUploadURL is GitHub's undocumented single-request attachment endpoint.
// It accepts a `gh` OAuth token as a bearer credential, but only for images and
// video, and only on repositories the token can push to. Router treats every
// other case as a fallback to the browser-session flow.
const bearerUploadURL = "https://uploads.github.com"

// StatusError reports a non-2xx response from the bearer upload endpoint.
// Router classifies Code to decide how far a failure generalizes: a 404 is
// scoped to the token and repository and so holds for the whole run, while a
// 422 is scoped to one content type.
type StatusError struct {
	Code int
	Body string
}

func (e *StatusError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("HTTP %d", e.Code)
	}
	return fmt.Sprintf("HTTP %d: %s", e.Code, e.Body)
}

// BearerClient uploads through the bearer endpoint in a single request.
// baseURL has no trailing slash; production is bearerUploadURL and tests point
// it at an httptest server.
type BearerClient struct {
	http    *http.Client
	baseURL string
	token   string
}

// NewBearerClient creates a client authenticating with a `gh` OAuth token.
//
// The transport is built from scratch rather than cloned from
// http.DefaultTransport: Transport.Clone copies an already-initialized
// TLSNextProto map, and the bundled HTTP/2 transport reads ExpectContinueTimeout
// through a back-pointer to the transport it was bound to — the original, not
// the clone. A clone would therefore ignore the timeout set below whenever
// anything else in the process had already used the default transport, silently
// dropping the 100-continue handshake that keeps a rejected upload from sending
// its body.
func NewBearerClient(token string) *BearerClient {
	return &BearerClient{
		http: &http.Client{
			Timeout: 120 * time.Second,
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment,
				DialContext: (&net.Dialer{
					Timeout:   30 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				ForceAttemptHTTP2:     true,
				MaxIdleConns:          100,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ExpectContinueTimeout: 2 * time.Second,
			},
		},
		baseURL: bearerUploadURL,
		token:   token,
	}
}

// Upload sends the file in one request and returns the asset reference.
// The response carries only the URL, so the name is the local basename.
func (c *BearerClient) Upload(repoID int, path string) (*Result, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("file: %w", err)
	}
	contentType := detectContentType(path)
	fileName := filepath.Base(path)

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening file: %w", err)
	}
	defer func() { _ = f.Close() }()

	query := url.Values{
		"name":          {fileName},
		"content_type":  {contentType},
		"repository_id": {strconv.Itoa(repoID)},
	}
	req, err := http.NewRequest("POST", c.baseURL+"/user-attachments/assets?"+query.Encode(), f)
	if err != nil {
		return nil, err
	}
	// ContentLength is what lets the transport offer Expect: 100-continue, which
	// keeps a rejected upload from sending the file body at all.
	req.ContentLength = info.Size()
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	// Omitting Content-Type makes the endpoint answer 400 before it looks at
	// anything else.
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Expect", "100-continue")
	req.Header.Set("User-Agent", httputil.UserAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, &StatusError{Code: resp.StatusCode, Body: truncate(string(respBody), 200)}
	}

	var result struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding upload response: %w", err)
	}
	if result.URL == "" {
		return nil, fmt.Errorf("upload response missing url")
	}

	return &Result{
		URL:      result.URL,
		Name:     fileName,
		Markdown: renderMarkdown(fileName, result.URL, contentType),
	}, nil
}
