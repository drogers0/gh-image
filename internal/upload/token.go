package upload

import (
	"fmt"
	"io"
	"net/http"
	"regexp"

	"github.com/drogers0/gh-image/internal/httputil"
)

var uploadTokenRe = regexp.MustCompile(`"uploadToken":"([^"]+)"`)

// GetUploadToken fetches the repo page and extracts the uploadToken
// from the JS payload. Requires authenticated cookies in the client.
func GetUploadToken(client *http.Client, owner, repo string) (string, error) {
	url := fmt.Sprintf("https://github.com/%s/%s", owner, repo)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", httputil.UserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching repo page: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("repo page returned %d — do you have access to %s/%s?", resp.StatusCode, owner, repo)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading repo page: %w", err)
	}

	match := uploadTokenRe.FindSubmatch(body)
	if match == nil {
		return "", fmt.Errorf("uploadToken not found on repo page — do you have write access to %s/%s?", owner, repo)
	}

	return string(match[1]), nil
}
