package upload

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/drogers0/gh-image/internal/cookies"
)

const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36"

// Result holds the output of a successful image upload.
type Result struct {
	URL      string // https://github.com/user-attachments/assets/<uuid>
	Name     string // sanitized filename
	Markdown string // ![name](url)
}

// policyResponse represents the JSON response from /upload/policies/assets.
type policyResponse struct {
	UploadURL string `json:"upload_url"`
	Asset     struct {
		ID          int    `json:"id"`
		Name        string `json:"name"`
		Size        int64  `json:"size"`
		ContentType string `json:"content_type"`
		Href        string `json:"href"`
	} `json:"asset"`
	Form                         map[string]string `json:"form"`
	AssetUploadAuthenticityToken string            `json:"asset_upload_authenticity_token"`
}

// NewClient creates an http.Client with the GitHub session cookies set.
// GitHub requires both user_session and __Host-user_session_same_site
// for CSRF validation on the upload endpoint.
func NewClient(sessionCookie *http.Cookie) *http.Client {
	return &http.Client{
		Jar:     cookies.NewGitHubCookieJar(sessionCookie),
		Timeout: 30 * time.Second,
	}
}

// Upload uploads an image file to GitHub and returns the asset URL.
// owner/repo identifies the target repository, repoID is its numeric ID.
func Upload(client *http.Client, owner, repo string, repoID int, imagePath string) (*Result, error) {
	info, err := os.Stat(imagePath)
	if err != nil {
		return nil, fmt.Errorf("image file: %w", err)
	}
	contentType := detectContentType(imagePath)
	fileName := filepath.Base(imagePath)

	// Step 0: Get uploadToken from repo page
	uploadToken, err := GetUploadToken(client, owner, repo)
	if err != nil {
		return nil, fmt.Errorf("step 0 (get upload token): %w", err)
	}

	// Step 1: Request upload policy
	policy, err := requestPolicy(client, owner, repo, uploadToken, repoID, fileName, info.Size(), contentType)
	if err != nil {
		return nil, fmt.Errorf("step 1 (request policy): %w", err)
	}

	// Step 2: Upload file to S3
	err = uploadToS3(policy, imagePath, fileName, contentType)
	if err != nil {
		return nil, fmt.Errorf("step 2 (S3 upload): %w", err)
	}

	// Step 3: Finalize the upload
	result, err := finalizeUpload(client, owner, repo, policy)
	if err != nil {
		return nil, fmt.Errorf("step 3 (finalize): %w", err)
	}

	return result, nil
}

// requestPolicy calls POST /upload/policies/assets to get the S3 presigned form.
func requestPolicy(client *http.Client, owner, repo, uploadToken string, repoID int, fileName string, fileSize int64, contentType string) (*policyResponse, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	for _, f := range []struct{ k, v string }{
		{"name", fileName},
		{"size", strconv.FormatInt(fileSize, 10)},
		{"content_type", contentType},
		{"authenticity_token", uploadToken},
		{"repository_id", strconv.Itoa(repoID)},
	} {
		if err := writer.WriteField(f.k, f.v); err != nil {
			return nil, fmt.Errorf("writing form field %s: %w", f.k, err)
		}
	}
	writer.Close()

	req, err := http.NewRequest("POST", "https://github.com/upload/policies/assets", body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Origin", "https://github.com")
	req.Header.Set("Referer", fmt.Sprintf("https://github.com/%s/%s", owner, repo))
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("expected 201, got %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}

	var policy policyResponse
	if err := json.NewDecoder(resp.Body).Decode(&policy); err != nil {
		return nil, fmt.Errorf("decoding policy response: %w", err)
	}

	if policy.UploadURL == "" {
		return nil, fmt.Errorf("policy response missing upload_url")
	}
	if policy.AssetUploadAuthenticityToken == "" {
		return nil, fmt.Errorf("policy response missing asset_upload_authenticity_token")
	}
	if len(policy.Form) == 0 {
		return nil, fmt.Errorf("policy response missing form fields")
	}
	if policy.Asset.ID == 0 {
		return nil, fmt.Errorf("policy response missing asset ID")
	}

	return &policy, nil
}

// finalizeUpload calls PUT /upload/assets/{id} to mark the asset as ready.
func finalizeUpload(client *http.Client, owner, repo string, policy *policyResponse) (*Result, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("authenticity_token", policy.AssetUploadAuthenticityToken); err != nil {
		return nil, fmt.Errorf("writing form field authenticity_token: %w", err)
	}
	writer.Close()

	req, err := http.NewRequest("PUT", fmt.Sprintf("https://github.com/upload/assets/%d", policy.Asset.ID), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Origin", "https://github.com")
	req.Header.Set("Referer", fmt.Sprintf("https://github.com/%s/%s", owner, repo))
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("expected 200, got %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}

	var result struct {
		Href string `json:"href"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding finalize response: %w", err)
	}

	return &Result{
		URL:      result.Href,
		Name:     result.Name,
		Markdown: fmt.Sprintf("![%s](%s)", result.Name, result.Href),
	}, nil
}

func detectContentType(path string) string {
	ext := filepath.Ext(path)
	ct := mime.TypeByExtension(ext)
	if ct != "" {
		return ct
	}
	return "application/octet-stream"
}

func truncate(s string, maxLen int) string {
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	return string(r[:maxLen]) + "..."
}
