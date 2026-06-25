package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/drogers0/gh-image/internal/cookies"
	"github.com/drogers0/gh-image/internal/repo"
	"github.com/drogers0/gh-image/internal/session"
	"github.com/drogers0/gh-image/internal/upload"
)

const usage = `Usage:
  gh image [--repo owner/repo] [--token <value>] <image-path>...
  gh image extract-token
  gh image check-token [--token <value>]
  gh image --version`

// version is set via -ldflags "-X main.version=..." at release build time.
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, productionDeps()))
}

// uploadFunc uploads one image and returns its markdown reference.
type uploadFunc func(info *repo.Info, imagePath string) (string, error)

// deps are the I/O boundaries run() depends on; productionDeps wires the real ones,
// tests inject stubs so the orchestration spine runs without network/subprocess/exit.
type deps struct {
	resolveRepo   func(owner, name string) (*repo.Info, error)
	resolveCookie func(tokenFlag string) (*http.Cookie, error)
	// newUploader builds an uploader from a session cookie. It is called once per
	// run so the underlying HTTP client (and its cookie jar) is shared across all
	// images, matching the single-client behavior of the original implementation.
	newUploader  func(cookie *http.Cookie) uploadFunc
	extractToken func() (string, error)
	checkToken   func(tokenFlag string) (username, source string, err error)
}

func productionDeps() deps {
	return deps{
		resolveRepo: repo.Resolve,
		resolveCookie: func(tokenFlag string) (*http.Cookie, error) {
			cookie, _, err := resolveSessionCookie(tokenFlag)
			return cookie, err
		},
		newUploader: func(cookie *http.Cookie) uploadFunc {
			client := upload.NewClient(cookie)
			return func(info *repo.Info, imagePath string) (string, error) {
				res, err := client.Upload(info.Owner, info.Name, info.ID, imagePath)
				if err != nil {
					return "", err
				}
				return res.Markdown, nil
			}
		},
		// extract-token stays offline: pass nil so selection skips network validation.
		extractToken: func() (string, error) {
			return extractToken(func() (*http.Cookie, error) { return cookies.GetGitHubSession(nil) })
		},
		checkToken: func(tokenFlag string) (string, string, error) {
			return checkToken(tokenFlag, resolveSessionCookie, session.CheckValidity)
		},
	}
}

func run(args []string, stdout, stderr io.Writer, d deps) int {
	var repoFlag string
	var repoSet bool
	var tokenFlag string
	var tokenSet bool
	var imagePaths []string
	var firstPosAfterDoubleDash bool

	// Manual arg parsing so flags can appear anywhere (before or after positional args).
	flagsDone := false
	for i := 0; i < len(args); i++ {
		arg := args[i]

		// After "--", everything is a positional arg
		if flagsDone {
			if len(imagePaths) == 0 {
				firstPosAfterDoubleDash = true
			}
			imagePaths = append(imagePaths, arg)
			continue
		}

		switch {
		case arg == "--":
			flagsDone = true
		case arg == "--repo":
			if repoSet {
				fmt.Fprintf(stderr, "Error: --repo specified more than once\n")
				return 1
			}
			if i+1 >= len(args) {
				fmt.Fprintf(stderr, "Error: --repo requires a value (owner/repo)\n%s\n", usage)
				return 1
			}
			i++
			repoFlag = args[i]
			repoSet = true
		case strings.HasPrefix(arg, "--repo="):
			if repoSet {
				fmt.Fprintf(stderr, "Error: --repo specified more than once\n")
				return 1
			}
			repoFlag = strings.SplitN(arg, "=", 2)[1]
			repoSet = true
		case arg == "--token":
			if tokenSet {
				fmt.Fprintf(stderr, "Error: --token specified more than once\n")
				return 1
			}
			if i+1 >= len(args) {
				fmt.Fprintf(stderr, "Error: --token requires a value\n%s\n", usage)
				return 1
			}
			i++
			tokenFlag = strings.TrimSpace(args[i])
			if tokenFlag == "" {
				fmt.Fprintf(stderr, "Error: --token value cannot be empty\n%s\n", usage)
				return 1
			}
			tokenSet = true
		case strings.HasPrefix(arg, "--token="):
			if tokenSet {
				fmt.Fprintf(stderr, "Error: --token specified more than once\n")
				return 1
			}
			tokenFlag = strings.TrimSpace(strings.SplitN(arg, "=", 2)[1])
			if tokenFlag == "" {
				fmt.Fprintf(stderr, "Error: --token value cannot be empty\n%s\n", usage)
				return 1
			}
			tokenSet = true
		case arg == "--version":
			fmt.Fprintf(stdout, "gh-image %s\n", version)
			return 0
		case arg == "--help" || arg == "-h":
			fmt.Fprintf(stdout, "%s\n\n", usage)
			fmt.Fprintln(stdout, "Upload images to GitHub and print markdown references.")
			fmt.Fprintln(stdout)
			fmt.Fprintln(stdout, "The --repo flag is optional. If omitted, the repository is")
			fmt.Fprintln(stdout, "inferred from the git remote in the current directory.")
			fmt.Fprintln(stdout)
			fmt.Fprintln(stdout, "Flags:")
			fmt.Fprintln(stdout, "  --repo owner/repo   GitHub repository (optional)")
			fmt.Fprintln(stdout, "  --token <value>     GitHub session token (default: extracted from browser)")
			fmt.Fprintln(stdout, "                      Can also be set via GH_SESSION_TOKEN environment variable")
			fmt.Fprintln(stdout, "                      WARNING: --token values are visible in process listings.")
			fmt.Fprintln(stdout, "                      Prefer GH_SESSION_TOKEN on shared machines.")
			fmt.Fprintln(stdout, "  --version           Print version and exit")
			fmt.Fprintln(stdout)
			fmt.Fprintln(stdout, "Subcommands:")
			fmt.Fprintln(stdout, "  extract-token       Extract session token from browser and print to stdout")
			fmt.Fprintln(stdout, "  check-token         Verify a session token is valid and print username to stdout")
			fmt.Fprintln(stdout)
			fmt.Fprintln(stdout, "Use -- to separate flags from filenames starting with a dash:")
			fmt.Fprintln(stdout, "  gh image -- -screenshot.png")
			return 0
		case strings.HasPrefix(arg, "-") && arg != "-":
			fmt.Fprintf(stderr, "Error: unknown flag %s\n", arg)
			if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") {
				fmt.Fprintf(stderr, "If this is a filename, use: gh image -- %s\n", arg)
			}
			fmt.Fprintf(stderr, "Run 'gh image --help' for usage.\n")
			return 1
		default:
			imagePaths = append(imagePaths, arg)
		}
	}

	// Dispatch subcommands before any other validation.
	subcommand, dispatchErr := classifySubcommand(imagePaths, firstPosAfterDoubleDash, tokenFlag, repoSet)
	if dispatchErr != nil {
		fmt.Fprintf(stderr, "Error: %v\n", dispatchErr)
		var ue *usageError
		if errors.As(dispatchErr, &ue) {
			fmt.Fprintf(stderr, "%s\nRun 'gh image --help' for usage.\n", usage)
		}
		return 1
	}
	switch subcommand {
	case "extract-token":
		value, err := d.extractToken()
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		fmt.Fprintln(stderr, "Extracted session token from browser cookies")
		fmt.Fprintln(stdout, value)
		return 0
	case "check-token":
		username, source, err := d.checkToken(tokenFlag)
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		fmt.Fprintf(stderr, "Token is valid (source: %s)\n", source)
		if username != "" {
			fmt.Fprintln(stdout, username)
		}
		return 0
	}

	if len(imagePaths) == 0 {
		fmt.Fprintf(stderr, "%s\nRun 'gh image --help' for usage.\n", usage)
		return 1
	}

	// Validate image paths early
	for _, p := range imagePaths {
		if p == "" {
			fmt.Fprintf(stderr, "Error: empty image path\n")
			return 1
		}
	}

	// Resolve repository
	var owner, name string
	if repoSet {
		if repoFlag == "" {
			fmt.Fprintf(stderr, "Error: --repo value cannot be empty\n")
			return 1
		}
		parts := strings.SplitN(repoFlag, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			fmt.Fprintf(stderr, "Error: --repo must be in owner/repo format, got: %s\n", repoFlag)
			return 1
		}
		owner, name = parts[0], parts[1]
	}

	repoInfo, err := d.resolveRepo(owner, name)
	if err != nil {
		fmt.Fprintf(stderr, "Error resolving repository: %v\n", err)
		return 1
	}

	// Get session cookie (flag > env var > browser)
	cookie, err := d.resolveCookie(tokenFlag)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}

	// Build the uploader once so its HTTP client/cookie jar is shared across images.
	uploadImage := d.newUploader(cookie)

	// Upload each image, continuing on error
	hasError := false
	for _, imagePath := range imagePaths {
		markdown, err := uploadImage(repoInfo, imagePath)
		if err != nil {
			fmt.Fprintf(stderr, "Error uploading %s: %v\n", imagePath, err)
			hasError = true
			continue
		}
		fmt.Fprintln(stdout, markdown)
	}
	if hasError {
		return 1
	}
	return 0
}

// usageError wraps an error to signal that usage text should be shown alongside the message.
type usageError struct{ err error }

func (e *usageError) Error() string { return e.err.Error() }

// classifySubcommand identifies whether the parsed positional args represent a
// supported subcommand invocation and validates subcommand-specific constraints.
func classifySubcommand(imagePaths []string, firstPosAfterDoubleDash bool, tokenFlag string, repoSet bool) (string, error) {
	if len(imagePaths) == 0 || firstPosAfterDoubleDash {
		return "", nil
	}
	switch imagePaths[0] {
	case "extract-token":
		if len(imagePaths) > 1 {
			return "", &usageError{fmt.Errorf("extract-token does not take positional arguments")}
		}
		if tokenFlag != "" {
			return "", fmt.Errorf("--token cannot be combined with extract-token (extract-token always reads from browser)")
		}
		if repoSet {
			return "", fmt.Errorf("--repo cannot be combined with extract-token")
		}
		return "extract-token", nil
	case "check-token":
		if len(imagePaths) > 1 {
			return "", &usageError{fmt.Errorf("check-token does not take positional arguments")}
		}
		if repoSet {
			return "", fmt.Errorf("--repo cannot be combined with check-token")
		}
		return "check-token", nil
	default:
		return "", nil
	}
}

// resolveSessionCookie returns a GitHub session cookie using the first available
// source: --token flag, GH_SESSION_TOKEN environment variable, or browser extraction.
// The browser getter validates candidates against GitHub when more than one
// logged-in session exists, so a stale/logged-out cookie isn't picked over a live one.
func resolveSessionCookie(tokenFlag string) (*http.Cookie, string, error) {
	get := func() (*http.Cookie, error) {
		return cookies.GetGitHubSession(func(c *http.Cookie) error {
			_, err := session.CheckValidity(c)
			return err
		})
	}
	return resolveSessionCookieWithGetter(tokenFlag, os.Getenv("GH_SESSION_TOKEN"), get)
}

// resolveSessionCookieWithGetter is a testable variant of resolveSessionCookie
// that accepts explicit env value and browser cookie getter dependencies.
// Returns the cookie, a human-readable source label, and any error.
func resolveSessionCookieWithGetter(tokenFlag, envToken string, getBrowserCookie func() (*http.Cookie, error)) (*http.Cookie, string, error) {
	if tokenFlag != "" {
		cookie, err := cookieFromValue(tokenFlag)
		if err != nil {
			return nil, "", fmt.Errorf("--token flag: %w", err)
		}
		return cookie, "--token flag", nil
	}
	if envToken != "" {
		cookie, err := cookieFromValue(envToken)
		if err != nil {
			return nil, "", fmt.Errorf("GH_SESSION_TOKEN: %w", err)
		}
		return cookie, "GH_SESSION_TOKEN", nil
	}
	if getBrowserCookie == nil {
		return nil, "", fmt.Errorf("no session token found (set --token flag or GH_SESSION_TOKEN env var, or log into GitHub in a supported browser): browser session getter is unavailable")
	}
	cookie, err := getBrowserCookie()
	if err != nil {
		return nil, "", fmt.Errorf("no session token found (set --token flag or GH_SESSION_TOKEN env var, or log into GitHub in a supported browser): %w", err)
	}
	return cookie, "browser cookies", nil
}

// cookieFromValue constructs a GitHub user_session cookie from a raw token value.
func cookieFromValue(value string) (*http.Cookie, error) {
	value = strings.TrimSpace(value) // defensive: env vars arrive untrimmed; flag path trims earlier
	if value == "" {
		return nil, fmt.Errorf("session token is empty")
	}
	return cookies.NewSessionCookie(value), nil
}

// extractToken extracts a session token from the browser and returns the raw value.
func extractToken(getBrowserCookie func() (*http.Cookie, error)) (string, error) {
	cookie, err := getBrowserCookie()
	if err != nil {
		return "", err
	}
	return cookie.Value, nil
}

// checkToken resolves and validates a session token, returning the authenticated username and source.
func checkToken(tokenFlag string, resolver func(string) (*http.Cookie, string, error), validator func(*http.Cookie) (string, error)) (string, string, error) {
	cookie, source, err := resolver(tokenFlag)
	if err != nil {
		return "", "", err
	}
	username, err := validator(cookie)
	if err != nil {
		return "", "", err
	}
	return username, source, nil
}
