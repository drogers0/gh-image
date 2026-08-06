package upload

import (
	"fmt"
	"os/exec"
	"strings"
)

// tokenRunner runs an external command and returns its stdout. Injected so the
// gh subprocess boundary is stubbable in tests, matching internal/repo.
type tokenRunner func(name string, args ...string) ([]byte, error)

func execRun(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

// GHAuthToken returns the OAuth token the gh CLI would use. gh resolves
// GH_TOKEN and GITHUB_TOKEN before its stored credentials, so this is the whole
// token-source policy.
func GHAuthToken() (string, error) {
	return ghAuthToken(execRun)
}

func ghAuthToken(run tokenRunner) (string, error) {
	out, err := run("gh", "auth", "token")
	if err != nil {
		return "", fmt.Errorf("gh auth token: %w", err)
	}
	token := strings.TrimSpace(string(out))
	if token == "" {
		return "", fmt.Errorf("gh auth token returned no token")
	}
	return token, nil
}
