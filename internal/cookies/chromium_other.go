//go:build !darwin

package cookies

import (
	"fmt"
	"net/http"
)

func directReadGitHubSession() (*http.Cookie, error) {
	return nil, fmt.Errorf("direct cookie reader not supported on this platform")
}
