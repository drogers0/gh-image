package upload

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"time"

	"github.com/drogers0/gh-image/internal/httputil"
)

// uploadToS3 uploads the file to S3 using the presigned form fields from the policy.
func uploadToS3(policy *policyResponse, filePath, fileName, contentType string) error {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Write policy form fields in a deterministic order.
	// Go maps have nondeterministic iteration order, and S3 presigned uploads
	// can be sensitive to field ordering. We iterate through the known fields
	// first, then write any unexpected fields that may appear in the future.
	s3FieldOrder := []string{
		"key",
		"acl",
		"policy",
		"X-Amz-Algorithm",
		"X-Amz-Credential",
		"X-Amz-Date",
		"X-Amz-Signature",
		"Content-Type",
		"Cache-Control",
		"x-amz-meta-Surrogate-Control",
	}

	written := make(map[string]bool, len(s3FieldOrder))
	for _, key := range s3FieldOrder {
		val, ok := policy.Form[key]
		if !ok {
			continue
		}
		if err := writer.WriteField(key, val); err != nil {
			return fmt.Errorf("writing form field %s: %w", key, err)
		}
		written[key] = true
	}

	// Write any remaining fields not in the known order (future-proofing).
	for key, val := range policy.Form {
		if written[key] {
			continue
		}
		if err := writer.WriteField(key, val); err != nil {
			return fmt.Errorf("writing form field %s: %w", key, err)
		}
	}

	// File must be the last field in the multipart form.
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		return fmt.Errorf("creating file field: %w", err)
	}

	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("opening file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(part, f); err != nil {
		return fmt.Errorf("writing file data: %w", err)
	}

	if err := writer.Close(); err != nil {
		return fmt.Errorf("closing multipart writer: %w", err)
	}

	req, err := http.NewRequest("POST", policy.UploadURL, body)
	if err != nil {
		return fmt.Errorf("creating S3 request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Origin", "https://github.com")
	req.Header.Set("User-Agent", httputil.UserAgent)

	// S3 upload uses no GitHub cookies — the presigned policy handles auth.
	resp, err := (&http.Client{Timeout: 120 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("S3 upload request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("S3 returned %d: %s", resp.StatusCode, truncate(string(respBody), 300))
	}

	return nil
}
