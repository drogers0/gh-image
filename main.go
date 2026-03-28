package main

import (
	"fmt"
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
  gh image check-token [--token <value>]`

func main() {
	var repoFlag string
	var repoSet bool
	var tokenFlag string
	var imagePaths []string

	// Manual arg parsing so flags can appear anywhere (before or after positional args).
	args := os.Args[1:]
	flagsDone := false
	for i := 0; i < len(args); i++ {
		arg := args[i]

		// After "--", everything is a positional arg
		if flagsDone {
			imagePaths = append(imagePaths, arg)
			continue
		}

		switch {
		case arg == "--":
			flagsDone = true
		case arg == "--repo":
			if repoSet {
				fmt.Fprintf(os.Stderr, "Error: --repo specified more than once\n")
				os.Exit(1)
			}
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --repo requires a value (owner/repo)\n%s\n", usage)
				os.Exit(1)
			}
			i++
			repoFlag = args[i]
			repoSet = true
		case strings.HasPrefix(arg, "--repo="):
			if repoSet {
				fmt.Fprintf(os.Stderr, "Error: --repo specified more than once\n")
				os.Exit(1)
			}
			repoFlag = strings.SplitN(arg, "=", 2)[1]
			repoSet = true
		case arg == "--token":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --token requires a value\n%s\n", usage)
				os.Exit(1)
			}
			i++
			tokenFlag = args[i]
		case strings.HasPrefix(arg, "--token="):
			tokenFlag = strings.SplitN(arg, "=", 2)[1]
		case arg == "--help" || arg == "-h":
			fmt.Printf("%s\n\n", usage)
			fmt.Println("Upload images to GitHub and print markdown references.")
			fmt.Println()
			fmt.Println("The --repo flag is optional. If omitted, the repository is")
			fmt.Println("inferred from the git remote in the current directory.")
			fmt.Println()
			fmt.Println("Flags:")
			fmt.Println("  --repo owner/repo   GitHub repository (optional)")
			fmt.Println("  --token <value>     GitHub session token (default: extracted from browser)")
			fmt.Println("                      Can also be set via GH_IMAGE_TOKEN environment variable")
			fmt.Println("                      WARNING: --token values are visible in process listings.")
			fmt.Println("                      Prefer GH_IMAGE_TOKEN on shared machines.")
			fmt.Println()
			fmt.Println("Subcommands:")
			fmt.Println("  extract-token       Extract session token from browser and print to stdout")
			fmt.Println("  check-token         Verify a session token is valid and print username to stdout")
			fmt.Println()
			fmt.Println("Use -- to separate flags from filenames starting with a dash:")
			fmt.Println("  gh image -- -screenshot.png")
			os.Exit(0)
		case strings.HasPrefix(arg, "-") && arg != "-":
			fmt.Fprintf(os.Stderr, "Error: unknown flag %s\n", arg)
			if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") {
				fmt.Fprintf(os.Stderr, "If this is a filename, use: gh image -- %s\n", arg)
			}
			fmt.Fprintf(os.Stderr, "Run 'gh image --help' for usage.\n")
			os.Exit(1)
		default:
			imagePaths = append(imagePaths, arg)
		}
	}

	// Dispatch subcommands before any other validation.
	if len(imagePaths) > 0 {
		switch imagePaths[0] {
		case "extract-token":
			if tokenFlag != "" {
				fmt.Fprintf(os.Stderr, "Error: --token cannot be combined with extract-token (extract-token always reads from browser)\n")
				os.Exit(1)
			}
			handleExtractToken()
		case "check-token":
			handleCheckToken(tokenFlag)
		}
	}

	if len(imagePaths) == 0 {
		fmt.Fprintf(os.Stderr, "%s\nRun 'gh image --help' for usage.\n", usage)
		os.Exit(1)
	}

	// Validate image paths early
	for _, p := range imagePaths {
		if p == "" {
			fmt.Fprintf(os.Stderr, "Error: empty image path\n")
			os.Exit(1)
		}
	}

	// Resolve repository
	var owner, name string
	if repoSet {
		if repoFlag == "" {
			fmt.Fprintf(os.Stderr, "Error: --repo value cannot be empty\n")
			os.Exit(1)
		}
		parts := strings.SplitN(repoFlag, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			fmt.Fprintf(os.Stderr, "Error: --repo must be in owner/repo format, got: %s\n", repoFlag)
			os.Exit(1)
		}
		owner, name = parts[0], parts[1]
	}

	repoInfo, err := repo.Resolve(owner, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving repository: %v\n", err)
		os.Exit(1)
	}

	// Get session cookie (flag > env var > browser)
	cookie, err := resolveSessionCookie(tokenFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	client := upload.NewClient(cookie)

	// Upload each image, continuing on error
	hasError := false
	for _, imagePath := range imagePaths {
		result, err := upload.Upload(client, repoInfo.Owner, repoInfo.Name, repoInfo.ID, imagePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error uploading %s: %v\n", imagePath, err)
			hasError = true
			continue
		}
		fmt.Println(result.Markdown)
	}
	if hasError {
		os.Exit(1)
	}
}

// resolveSessionCookie returns a GitHub session cookie using the first available
// source: --token flag, GH_IMAGE_TOKEN environment variable, or browser extraction.
func resolveSessionCookie(tokenFlag string) (*http.Cookie, error) {
	if tokenFlag != "" {
		return cookieFromValue(tokenFlag)
	}
	if env := os.Getenv("GH_IMAGE_TOKEN"); env != "" {
		return cookieFromValue(env)
	}
	cookie, err := cookies.GetGitHubSession()
	if err != nil {
		return nil, fmt.Errorf("no session token found (set --token flag or GH_IMAGE_TOKEN env var, or log into GitHub in a supported browser): %w", err)
	}
	return cookie, nil
}

// cookieFromValue constructs a GitHub user_session cookie from a raw token value.
func cookieFromValue(value string) (*http.Cookie, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, fmt.Errorf("session token is empty")
	}
	return &http.Cookie{
		Name:     "user_session",
		Value:    value,
		Domain:   "github.com",
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
	}, nil
}

// handleExtractToken extracts the session cookie from the browser and prints
// the raw token value to stdout. Source info is written to stderr.
func handleExtractToken() {
	cookie, err := cookies.GetGitHubSession()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "Extracted session token from browser cookies")
	fmt.Println(cookie.Value)
	os.Exit(0)
}

// handleCheckToken verifies the session cookie and prints the username to stdout.
// Token source is resolved via resolveSessionCookie (flag > env var > browser).
func handleCheckToken(tokenFlag string) {
	cookie, err := resolveSessionCookie(tokenFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	username, err := session.CheckValidity(cookie)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "Token is valid")
	fmt.Println(username)
	os.Exit(0)
}

