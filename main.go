package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/drogers0/gh-image/internal/cookies"
	"github.com/drogers0/gh-image/internal/download"
	"github.com/drogers0/gh-image/internal/repo"
	"github.com/drogers0/gh-image/internal/session"
	"github.com/drogers0/gh-image/internal/upload"
)

const usage = `Usage:
  gh image [--repo owner/repo] [--token <value>] <file-path>...
  gh image download [--output <file>|-] [--output-dir <dir>] [--no-clobber] [--token <value>] <url>...
  gh image extract-token
  gh image check-token [--token <value>]
  gh image --version`

// version is set via -ldflags "-X main.version=..." at release build time.
var version = "dev"

// sessionTokenEnvVar supplies the user_session cookie value without a flag.
const sessionTokenEnvVar = "GH_SESSION_TOKEN"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, productionDeps()))
}

// uploadFunc uploads one file and returns its markdown reference.
type uploadFunc func(info *repo.Info, path string) (string, error)

// deps are the I/O boundaries run() depends on; productionDeps wires the real ones,
// tests inject stubs so the orchestration spine runs without network/subprocess/exit.
type deps struct {
	resolveRepo func(owner, name string) (*repo.Info, error)
	// newUploader builds an uploader for the run. It is called once so both the
	// bearer client and the session-cookie client (and its cookie jar) are shared
	// across all files. The session is resolved lazily, on the first file that
	// needs the browser-session flow, so runs that stay on the fast path never
	// touch the browser's cookie store.
	newUploader  func(tokenFlag string, stderr io.Writer) uploadFunc
	extractToken func() (string, error)
	checkToken   func(tokenFlag string) (username, source string, err error)
	// newDownloader builds a downloader for the run. Like newUploader it takes
	// both routes and resolves each lazily, so a run that stays on the bearer
	// token never touches the browser's cookie store.
	newDownloader func(tokenFlag string, stderr io.Writer) downloader
}

// downloader is the injected download boundary. Save writes to disk, Stream
// writes to a caller-supplied writer; no resolve metadata crosses this seam.
type downloader interface {
	Save(ref download.Ref, dest download.Dest) (string, error)
	Stream(ref download.Ref, w io.Writer) (int64, error)
}

func productionDeps() deps {
	return deps{
		resolveRepo: repo.Resolve,
		newUploader: func(tokenFlag string, stderr io.Writer) uploadFunc {
			var newBearer func() (*upload.BearerClient, error)
			if useBearerRoute(tokenFlag, os.Getenv(sessionTokenEnvVar)) {
				newBearer = func() (*upload.BearerClient, error) {
					token, err := upload.GHAuthToken()
					if err != nil {
						return nil, err
					}
					return upload.NewBearerClient(token), nil
				}
			}
			router := upload.NewRouter(
				newBearer,
				func() (*upload.Client, error) {
					cookie, _, err := resolveSessionCookie(tokenFlag)
					if err != nil {
						return nil, err
					}
					return upload.NewClient(cookie), nil
				},
				func(msg string) { fmt.Fprintln(stderr, msg) },
			)
			return func(info *repo.Info, path string) (string, error) {
				res, err := router.Upload(info.Owner, info.Name, info.ID, path)
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
		newDownloader: func(tokenFlag string, stderr io.Writer) downloader {
			var newBearer func() (string, error)
			if useBearerRoute(tokenFlag, os.Getenv(sessionTokenEnvVar)) {
				newBearer = upload.GHAuthToken
			}
			return download.NewClient(
				newBearer,
				func() (*http.Cookie, error) {
					cookie, _, err := resolveSessionCookie(tokenFlag)
					return cookie, err
				},
				func(msg string) { fmt.Fprintln(stderr, msg) },
			)
		},
	}
}

func run(args []string, stdout, stderr io.Writer, d deps) int {
	var repoFlag string
	var repoSet bool
	var tokenFlag string
	var tokenSet bool
	var paths []string
	var firstPosAfterDoubleDash bool
	var output, outputDir string
	var outputSet, outputDirSet, noClobber bool

	// Manual arg parsing so flags can appear anywhere (before or after positional args).
	flagsDone := false
	for i := 0; i < len(args); i++ {
		arg := args[i]

		// After "--", everything is a positional arg
		if flagsDone {
			if len(paths) == 0 {
				firstPosAfterDoubleDash = true
			}
			paths = append(paths, arg)
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
		case arg == "--output" || strings.HasPrefix(arg, "--output="):
			if err := parseValueFlag(arg, args, &i, "--output", &output, &outputSet); err != nil {
				return reportFlagError(stderr, err)
			}
		case arg == "--output-dir" || strings.HasPrefix(arg, "--output-dir="):
			if err := parseValueFlag(arg, args, &i, "--output-dir", &outputDir, &outputDirSet); err != nil {
				return reportFlagError(stderr, err)
			}
		case arg == "--no-clobber":
			if noClobber {
				fmt.Fprintf(stderr, "Error: --no-clobber specified more than once\n")
				return 1
			}
			noClobber = true
		case arg == "--version":
			fmt.Fprintf(stdout, "gh-image %s\n", version)
			return 0
		case arg == "--help" || arg == "-h":
			fmt.Fprintf(stdout, "%s\n\n", usage)
			fmt.Fprintln(stdout, "Upload images and files to GitHub and print markdown references.")
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
			fmt.Fprintln(stdout, "  download            Download user-attachments URLs to files or stdout")
			fmt.Fprintln(stdout, "  extract-token       Extract session token from browser and print to stdout")
			fmt.Fprintln(stdout, "  check-token         Verify a session token is valid and print username to stdout")
			fmt.Fprintln(stdout)
			fmt.Fprintln(stdout, "Download flags:")
			fmt.Fprintln(stdout, "  --output <file>     Write a single URL to this exact path (overwrites)")
			fmt.Fprintln(stdout, "  --output -          Stream a single URL to stdout")
			fmt.Fprintln(stdout, "  --output-dir <dir>  Write derived filenames into this directory")
			fmt.Fprintln(stdout, "  --no-clobber        Never overwrite; add a .1, .2 suffix instead")
			fmt.Fprintln(stdout)
			fmt.Fprintln(stdout, "With no --output flag, files land in the current directory under a name")
			fmt.Fprintln(stdout, "derived from the URL. Downloads authenticate with the same session token")
			fmt.Fprintln(stdout, "as uploads (--token, GH_SESSION_TOKEN, or the browser cookie).")
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
			paths = append(paths, arg)
		}
	}

	// Dispatch subcommands before any other validation.
	opts := cliOptions{
		firstPosAfterDoubleDash: firstPosAfterDoubleDash,
		tokenFlag:               tokenFlag,
		repoSet:                 repoSet,
		outputSet:               outputSet,
		outputDirSet:            outputDirSet,
		noClobber:               noClobber,
	}
	subcommand, dispatchErr := classifySubcommand(paths, opts)
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
	case "download":
		return runDownload(paths[1:], downloadOptions{
			output:    output,
			outputSet: outputSet,
			outputDir: outputDir,
			noClobber: noClobber,
			tokenFlag: tokenFlag,
		}, stdout, stderr, d)
	}

	if len(paths) == 0 {
		fmt.Fprintf(stderr, "%s\nRun 'gh image --help' for usage.\n", usage)
		return 1
	}

	// Validate file paths early
	for _, p := range paths {
		if p == "" {
			fmt.Fprintf(stderr, "Error: empty file path\n")
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

	// Build the uploader once so its HTTP clients are shared across files. The
	// session cookie behind it is resolved only if a file needs the
	// browser-session flow.
	uploadFile := d.newUploader(tokenFlag, stderr)

	// Upload each file, continuing on error
	hasError := false
	for _, path := range paths {
		markdown, err := uploadFile(repoInfo, path)
		if err != nil {
			fmt.Fprintf(stderr, "Error uploading %s: %v\n", path, err)
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
func classifySubcommand(paths []string, opts cliOptions) (string, error) {
	// download-only flags are meaningless everywhere else, so reject them rather
	// than letting an upload silently ignore an --output the user meant.
	notDownload := func(mode string) error {
		if flag := opts.downloadOnly(); flag != "" {
			return fmt.Errorf("%s can only be used with download, not %s", flag, mode)
		}
		return nil
	}
	if len(paths) == 0 || opts.firstPosAfterDoubleDash {
		return "", notDownload("upload")
	}
	switch paths[0] {
	case "extract-token":
		if len(paths) > 1 {
			return "", &usageError{fmt.Errorf("extract-token does not take positional arguments")}
		}
		if opts.tokenFlag != "" {
			return "", fmt.Errorf("--token cannot be combined with extract-token (extract-token always reads from browser)")
		}
		if opts.repoSet {
			return "", fmt.Errorf("--repo cannot be combined with extract-token")
		}
		return "extract-token", notDownload("extract-token")
	case "check-token":
		if len(paths) > 1 {
			return "", &usageError{fmt.Errorf("check-token does not take positional arguments")}
		}
		if opts.repoSet {
			return "", fmt.Errorf("--repo cannot be combined with check-token")
		}
		return "check-token", notDownload("check-token")
	case "download":
		// Attachment URLs carry no repository, so --repo has nothing to act on.
		if opts.repoSet {
			return "", fmt.Errorf("--repo cannot be combined with download (attachment URLs carry no repository)")
		}
		return "download", nil
	default:
		return "", notDownload("upload")
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
	return resolveSessionCookieWithGetter(tokenFlag, os.Getenv(sessionTokenEnvVar), get)
}

// useBearerRoute reports whether an upload or download may take the gh-token
// fast path. An explicitly supplied session token names the account to act as,
// and the bearer route authenticates as whoever gh is, so honoring that choice
// means staying on the browser-session flow for every file. The inputs mirror
// resolveSessionCookieWithGetter's first two, so the two decisions read from
// the same sources.
func useBearerRoute(tokenFlag, envToken string) bool {
	return tokenFlag == "" && envToken == ""
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
		return nil, "", fmt.Errorf("resolving session cookie: %w", err)
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

// cliOptions carries parsed flag state into subcommand classification, so a new
// flag does not mean a new positional parameter on classifySubcommand.
type cliOptions struct {
	firstPosAfterDoubleDash bool
	tokenFlag               string
	repoSet                 bool
	outputSet               bool
	outputDirSet            bool
	noClobber               bool
}

// downloadOnly reports whether any download-only flag was given. Those flags are
// rejected outside the download subcommand rather than silently ignored.
func (o cliOptions) downloadOnly() string {
	switch {
	case o.outputSet:
		return "--output"
	case o.outputDirSet:
		return "--output-dir"
	case o.noClobber:
		return "--no-clobber"
	}
	return ""
}

// parseValueFlag matches a value-taking long flag in either "--name value" or
// "--name=value" form, applying the single-use, missing-value and empty-value
// rules. It exists because those rules are otherwise ten near-identical lines
// per flag per form; the older --repo and --token cases still spell them out.
func parseValueFlag(arg string, args []string, i *int, name string, dst *string, set *bool) error {
	if *set {
		return fmt.Errorf("%s specified more than once", name)
	}
	if arg == name {
		if *i+1 >= len(args) {
			return &usageError{fmt.Errorf("%s requires a value", name)}
		}
		*i++
		*dst = strings.TrimSpace(args[*i])
	} else {
		*dst = strings.TrimSpace(strings.SplitN(arg, "=", 2)[1])
	}
	if *dst == "" {
		return &usageError{fmt.Errorf("%s value cannot be empty", name)}
	}
	*set = true
	return nil
}

// reportFlagError prints a flag error, adding the usage block when the error is
// a usageError — the same treatment subcommand dispatch gives its errors.
func reportFlagError(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "Error: %v\n", err)
	var ue *usageError
	if errors.As(err, &ue) {
		fmt.Fprintf(stderr, "%s\nRun 'gh image --help' for usage.\n", usage)
	}
	return 1
}

// downloadOptions is the download subcommand's slice of the parsed flags.
type downloadOptions struct {
	output    string
	outputSet bool
	outputDir string
	noClobber bool
	tokenFlag string
}

// runDownload fetches each URL. Everything that can be checked without the
// network is checked first, so a bad flag combination fails immediately.
func runDownload(urls []string, o downloadOptions, stdout, stderr io.Writer, d deps) int {
	if len(urls) == 0 {
		fmt.Fprintf(stderr, "Error: download requires at least one user-attachments URL\n%s\n", usage)
		return 1
	}
	stdoutMode := o.outputSet && o.output == "-"
	switch {
	case o.outputSet && o.outputDir != "":
		fmt.Fprintf(stderr, "Error: --output and --output-dir cannot be combined\n")
		return 1
	case o.outputSet && len(urls) > 1:
		fmt.Fprintf(stderr, "Error: --output takes a single URL; use --output-dir for several\n")
		return 1
	}

	refs := make([]download.Ref, 0, len(urls))
	for _, raw := range urls {
		ref, err := download.ParseRef(raw)
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		refs = append(refs, ref)
	}

	// Build the destination up front so a missing directory is created once, and
	// so an unusable --output fails before any request is made.
	var dest download.Dest
	if !stdoutMode {
		dest = download.Dest{Dir: ".", NoClobber: o.noClobber}
		switch {
		case o.outputSet:
			dest.Exact = o.output
			dest.Dir = ""
		case o.outputDir != "":
			dest.Dir = o.outputDir
		}
		dir := dest.Dir
		if dest.Exact != "" {
			dir = filepath.Dir(dest.Exact)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(stderr, "Error: creating %s: %v\n", dir, err)
			return 1
		}
	}

	dl := d.newDownloader(o.tokenFlag, stderr)

	hasError := false
	for _, ref := range refs {
		if stdoutMode {
			if _, err := dl.Stream(ref, stdout); err != nil {
				fmt.Fprintf(stderr, "Error downloading %s: %v\n", ref.URL, err)
				hasError = true
			}
			continue
		}
		written, err := dl.Save(ref, dest)
		if err != nil {
			fmt.Fprintf(stderr, "Error downloading %s: %v\n", ref.URL, err)
			hasError = true
			continue
		}
		fmt.Fprintln(stdout, written)
	}
	if hasError {
		return 1
	}
	return 0
}
