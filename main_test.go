package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drogers0/gh-image/internal/download"
	"github.com/drogers0/gh-image/internal/repo"
	"github.com/drogers0/gh-image/internal/upload"
)

// okDeps returns deps whose boundaries all succeed; tests override individual
// fields to exercise specific paths.
func okDeps() deps {
	return deps{
		resolveRepo: func(owner, name string) (*repo.Info, error) {
			return &repo.Info{Owner: "octo", Name: "hello", ID: 1}, nil
		},
		newUploader: func(tokenFlag string, stderr io.Writer) uploadFunc {
			// The stub returns image-embed markdown for any path; run()'s job is the
			// orchestration spine, not the embed-vs-link decision (that lives in
			// upload.renderMarkdown and is covered by TestRenderMarkdown).
			return func(info *repo.Info, path string) (string, error) {
				return "![" + path + "](url)", nil
			}
		},
		extractToken: func() (string, error) { return "extracted-token", nil },
		checkToken:   func(tokenFlag string) (string, string, error) { return "octouser", "stub", nil },
		newDownloader: func(tokenFlag string, stderr io.Writer) downloader {
			return &stubDownloader{}
		},
	}
}

// stubDownloader records what run() asked for. It writes nothing to disk, so the
// CLI spine is exercised without touching the filesystem or the network.
type stubDownloader struct {
	saved   []download.Ref
	dests   []download.Dest
	streams []download.Ref
	body    string
	failOn  string
}

func (s *stubDownloader) Save(ref download.Ref, dest download.Dest) (string, error) {
	s.saved = append(s.saved, ref)
	s.dests = append(s.dests, dest)
	if s.failOn != "" && strings.Contains(ref.URL, s.failOn) {
		return "", fmt.Errorf("save failed")
	}
	return filepath.Join(dest.Dir, ref.ID), nil
}

func (s *stubDownloader) Stream(ref download.Ref, w io.Writer) (int64, error) {
	s.streams = append(s.streams, ref)
	if s.failOn != "" && strings.Contains(ref.URL, s.failOn) {
		return 0, fmt.Errorf("stream failed")
	}
	n, err := io.WriteString(w, s.body)
	return int64(n), err
}

// depsWith returns okDeps plus a handle on the download stub it will hand out.
func depsWith(dl *stubDownloader) deps {
	d := okDeps()
	d.newDownloader = func(tokenFlag string, stderr io.Writer) downloader { return dl }
	return d
}

const assetURL = "https://github.com/user-attachments/assets/9f57198c-19d3-4ba0-a48d-ba4bcaccf9f0"
const asset2URL = "https://github.com/user-attachments/assets/92463e67-b897-4212-91b4-a4f9b80ec4d4"
const fileURL = "https://github.com/user-attachments/files/30473702/notes.txt"

// runWith executes run() with buffered streams and returns the exit code + output.
func runWith(t *testing.T, args []string, d deps) (code int, stdout, stderr string) {
	t.Helper()
	var so, se bytes.Buffer
	code = run(args, &so, &se, d)
	return code, so.String(), se.String()
}

// TestCookieFromValue_BasicAttributes verifies the cookie has the expected fields.
func TestCookieFromValue_BasicAttributes(t *testing.T) {
	cookie, err := cookieFromValue("mytoken123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cookie.Name != "user_session" {
		t.Errorf("expected Name 'user_session', got %q", cookie.Name)
	}
	if cookie.Value != "mytoken123" {
		t.Errorf("expected Value 'mytoken123', got %q", cookie.Value)
	}
	if cookie.Domain != "github.com" {
		t.Errorf("expected Domain 'github.com', got %q", cookie.Domain)
	}
	if cookie.Path != "/" {
		t.Errorf("expected Path '/', got %q", cookie.Path)
	}
	if !cookie.Secure {
		t.Error("expected Secure to be true")
	}
	if !cookie.HttpOnly {
		t.Error("expected HttpOnly to be true")
	}
}

// TestCookieFromValue_TrimsWhitespace verifies leading/trailing whitespace is stripped.
func TestCookieFromValue_TrimsWhitespace(t *testing.T) {
	cookie, err := cookieFromValue("  token_with_spaces  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cookie.Value != "token_with_spaces" {
		t.Errorf("expected whitespace trimmed, got %q", cookie.Value)
	}
}

// TestCookieFromValue_RejectsEmpty verifies that empty/whitespace-only values error.
func TestCookieFromValue_RejectsEmpty(t *testing.T) {
	tests := []string{"", "   ", "\t\n"}
	for _, v := range tests {
		_, err := cookieFromValue(v)
		if err == nil {
			t.Errorf("expected error for empty token %q, got nil", v)
		}
		if !strings.Contains(err.Error(), "empty") {
			t.Errorf("unexpected error message for %q: %v", v, err)
		}
	}
}

// TestResolveSessionCookie_FlagPriority verifies --token flag takes highest priority.
func TestResolveSessionCookie_FlagPriority(t *testing.T) {
	cookie, source, err := resolveSessionCookieWithGetter("flag_token", "env_token", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cookie.Value != "flag_token" {
		t.Errorf("expected flag_token to win, got %q", cookie.Value)
	}
	if source != "--token flag" {
		t.Errorf("expected source %q, got %q", "--token flag", source)
	}
}

// TestResolveSessionCookie_EnvFallback verifies GH_SESSION_TOKEN is used when no flag.
func TestResolveSessionCookie_EnvFallback(t *testing.T) {
	cookie, source, err := resolveSessionCookieWithGetter("", "env_token_value", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cookie.Value != "env_token_value" {
		t.Errorf("expected env_token_value, got %q", cookie.Value)
	}
	if source != "GH_SESSION_TOKEN" {
		t.Errorf("expected source %q, got %q", "GH_SESSION_TOKEN", source)
	}
}

// TestResolveSessionCookie_BrowserFallbackError verifies browser error is wrapped correctly.
func TestResolveSessionCookie_BrowserFallbackError(t *testing.T) {
	_, _, err := resolveSessionCookieWithGetter("", "", func() (*http.Cookie, error) {
		return nil, fmt.Errorf("no browser cookies available")
	})
	if err == nil {
		t.Fatal("expected error when browser getter fails, got nil")
	}
	if !strings.Contains(err.Error(), "resolving session cookie") {
		t.Errorf("expected 'resolving session cookie' in error, got: %v", err)
	}
}

func TestResolveSessionCookie_BrowserFallbackSuccess(t *testing.T) {
	cookie, source, err := resolveSessionCookieWithGetter("", "", func() (*http.Cookie, error) {
		return &http.Cookie{
			Name:  "user_session",
			Value: "browser_token",
		}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cookie == nil {
		t.Fatal("expected non-nil cookie")
	}
	if cookie.Value != "browser_token" {
		t.Fatalf("expected browser token, got %q", cookie.Value)
	}
	if source != "browser cookies" {
		t.Errorf("expected source %q, got %q", "browser cookies", source)
	}
}

// TestCookieFromValue_UsableByNewClient verifies the cookie produced by
// cookieFromValue can be passed to upload.NewClient.
func TestCookieFromValue_UsableByNewClient(t *testing.T) {
	cookie, err := cookieFromValue("testtoken")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	client := upload.NewClient(cookie)
	if client == nil {
		t.Fatal("expected upload.NewClient to return a non-nil client")
	}
}

func TestExtractToken_Success(t *testing.T) {
	value, err := extractToken(func() (*http.Cookie, error) {
		return &http.Cookie{Name: "user_session", Value: "browser_abc"}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if value != "browser_abc" {
		t.Errorf("expected 'browser_abc', got %q", value)
	}
}

func TestExtractToken_Error(t *testing.T) {
	_, err := extractToken(func() (*http.Cookie, error) {
		return nil, fmt.Errorf("no cookies")
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestCheckToken_Success(t *testing.T) {
	username, source, err := checkToken("sometoken",
		func(token string) (*http.Cookie, string, error) {
			cookie, err := cookieFromValue(token)
			return cookie, "--token flag", err
		},
		func(c *http.Cookie) (string, error) {
			return "testuser", nil
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if username != "testuser" {
		t.Errorf("expected 'testuser', got %q", username)
	}
	if source != "--token flag" {
		t.Errorf("expected source %q, got %q", "--token flag", source)
	}
}

func TestCheckToken_ResolverError(t *testing.T) {
	_, _, err := checkToken("",
		func(token string) (*http.Cookie, string, error) {
			return nil, "", fmt.Errorf("no token")
		},
		func(c *http.Cookie) (string, error) {
			return "unused", nil
		},
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestCheckToken_ValidatorError(t *testing.T) {
	_, _, err := checkToken("sometoken",
		func(token string) (*http.Cookie, string, error) {
			cookie, err := cookieFromValue(token)
			return cookie, "--token flag", err
		},
		func(c *http.Cookie) (string, error) {
			return "", fmt.Errorf("token expired")
		},
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestResolveSessionCookie_WhitespaceEnvVar(t *testing.T) {
	browserCalled := false
	_, _, err := resolveSessionCookieWithGetter("", "   ", func() (*http.Cookie, error) {
		browserCalled = true
		return &http.Cookie{Name: "user_session", Value: "browser_token"}, nil
	})
	if err == nil {
		t.Fatal("expected error for whitespace-only env token, got nil")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("expected error containing 'empty', got: %v", err)
	}
	if !strings.Contains(err.Error(), "GH_SESSION_TOKEN") {
		t.Errorf("expected error to identify source 'GH_SESSION_TOKEN', got: %v", err)
	}
	if browserCalled {
		t.Error("whitespace-only env token should not fall through to browser getter")
	}
}

func TestResolveSessionCookie_WhitespaceFlag(t *testing.T) {
	_, _, err := resolveSessionCookieWithGetter("   ", "env_token", nil)
	if err == nil {
		t.Fatal("expected error for whitespace-only flag token, got nil")
	}
	if !strings.Contains(err.Error(), "--token flag") {
		t.Errorf("expected error to identify source '--token flag', got: %v", err)
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("expected error containing 'empty', got: %v", err)
	}
}

func TestRun_VersionAndHelp(t *testing.T) {
	t.Run("version prints to stdout and exits 0", func(t *testing.T) {
		code, out, _ := runWith(t, []string{"--version"}, okDeps())
		if code != 0 {
			t.Fatalf("code = %d, want 0", code)
		}
		if !strings.Contains(out, "gh-image dev") {
			t.Errorf("stdout = %q, want version string", out)
		}
	})
	for _, flag := range []string{"--help", "-h"} {
		t.Run(flag+" prints usage to stdout and exits 0", func(t *testing.T) {
			code, out, _ := runWith(t, []string{flag}, okDeps())
			if code != 0 {
				t.Fatalf("code = %d, want 0", code)
			}
			if !strings.Contains(out, "Usage:") || !strings.Contains(out, "extract-token") {
				t.Errorf("stdout missing usage content: %q", out)
			}
		})
	}
}

func TestRun_FlagErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string // substring expected on stderr
	}{
		{"unknown long flag", []string{"--bogus"}, "unknown flag --bogus"},
		{"unknown short flag hints --", []string{"-x"}, "use: gh image -- -x"},
		{"repo twice", []string{"--repo", "a/b", "--repo", "c/d"}, "specified more than once"},
		{"repo missing value", []string{"--repo"}, "requires a value"},
		{"repo empty via =", []string{"--repo=", "img.png"}, "--repo value cannot be empty"},
		{"repo bad format", []string{"--repo", "noslash", "img.png"}, "must be in owner/repo format"},
		{"token twice", []string{"--token", "a", "--token", "b"}, "specified more than once"},
		{"token missing value", []string{"--token"}, "requires a value"},
		{"token empty value", []string{"--token", "   "}, "cannot be empty"},
		{"no args shows usage", nil, "Usage:"},
		{"empty file path", []string{""}, "empty file path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, _, errOut := runWith(t, tc.args, okDeps())
			if code != 1 {
				t.Fatalf("code = %d, want 1", code)
			}
			if !strings.Contains(errOut, tc.want) {
				t.Errorf("stderr = %q, want substring %q", errOut, tc.want)
			}
		})
	}
}

func TestRun_Subcommands(t *testing.T) {
	t.Run("extract-token success", func(t *testing.T) {
		code, out, errOut := runWith(t, []string{"extract-token"}, okDeps())
		if code != 0 || strings.TrimSpace(out) != "extracted-token" {
			t.Fatalf("code=%d out=%q", code, out)
		}
		if !strings.Contains(errOut, "Extracted session token") {
			t.Errorf("stderr missing status: %q", errOut)
		}
	})
	t.Run("extract-token error", func(t *testing.T) {
		d := okDeps()
		d.extractToken = func() (string, error) { return "", fmt.Errorf("no browser") }
		code, _, errOut := runWith(t, []string{"extract-token"}, d)
		if code != 1 || !strings.Contains(errOut, "no browser") {
			t.Fatalf("code=%d stderr=%q", code, errOut)
		}
	})
	t.Run("check-token success prints username", func(t *testing.T) {
		code, out, errOut := runWith(t, []string{"check-token"}, okDeps())
		if code != 0 || strings.TrimSpace(out) != "octouser" {
			t.Fatalf("code=%d out=%q", code, out)
		}
		if !strings.Contains(errOut, "Token is valid (source: stub)") {
			t.Errorf("stderr = %q", errOut)
		}
	})
	t.Run("check-token empty username prints nothing to stdout", func(t *testing.T) {
		d := okDeps()
		d.checkToken = func(string) (string, string, error) { return "", "stub", nil }
		code, out, _ := runWith(t, []string{"check-token"}, d)
		if code != 0 || strings.TrimSpace(out) != "" {
			t.Fatalf("code=%d out=%q", code, out)
		}
	})
	t.Run("check-token error", func(t *testing.T) {
		d := okDeps()
		d.checkToken = func(string) (string, string, error) { return "", "", fmt.Errorf("expired") }
		code, _, errOut := runWith(t, []string{"check-token"}, d)
		if code != 1 || !strings.Contains(errOut, "expired") {
			t.Fatalf("code=%d stderr=%q", code, errOut)
		}
	})
	t.Run("extract-token with --token is a conflict error", func(t *testing.T) {
		code, _, errOut := runWith(t, []string{"extract-token", "--token", "x"}, okDeps())
		if code != 1 || !strings.Contains(errOut, "--token cannot be combined") {
			t.Fatalf("code=%d stderr=%q", code, errOut)
		}
	})
}

func TestRun_Upload(t *testing.T) {
	t.Run("single file prints markdown, exits 0", func(t *testing.T) {
		code, out, _ := runWith(t, []string{"a.png"}, okDeps())
		if code != 0 || strings.TrimSpace(out) != "![a.png](url)" {
			t.Fatalf("code=%d out=%q", code, out)
		}
	})
	t.Run("multiple files print one line each", func(t *testing.T) {
		code, out, _ := runWith(t, []string{"a.png", "b.png"}, okDeps())
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if code != 0 || len(lines) != 2 {
			t.Fatalf("code=%d out=%q", code, out)
		}
	})
	t.Run("partial failure exits 1 but prints successes", func(t *testing.T) {
		d := okDeps()
		d.newUploader = func(string, io.Writer) uploadFunc {
			return func(info *repo.Info, p string) (string, error) {
				if p == "bad.png" {
					return "", fmt.Errorf("upload failed")
				}
				return "![" + p + "](url)", nil
			}
		}
		code, out, errOut := runWith(t, []string{"good.png", "bad.png"}, d)
		if code != 1 {
			t.Fatalf("code = %d, want 1", code)
		}
		if !strings.Contains(out, "![good.png](url)") {
			t.Errorf("stdout missing success line: %q", out)
		}
		if !strings.Contains(errOut, "Error uploading bad.png") {
			t.Errorf("stderr missing failure: %q", errOut)
		}
	})
	t.Run("resolveRepo error exits 1", func(t *testing.T) {
		d := okDeps()
		d.resolveRepo = func(string, string) (*repo.Info, error) { return nil, fmt.Errorf("no remote") }
		code, _, errOut := runWith(t, []string{"a.png"}, d)
		if code != 1 || !strings.Contains(errOut, "Error resolving repository") {
			t.Fatalf("code=%d stderr=%q", code, errOut)
		}
	})
	// Session resolution is now lazy, so a missing session surfaces per file
	// through the upload path rather than as a pre-flight error.
	t.Run("session resolution failure exits 1", func(t *testing.T) {
		d := okDeps()
		d.newUploader = func(string, io.Writer) uploadFunc {
			return func(*repo.Info, string) (string, error) { return "", fmt.Errorf("no token") }
		}
		code, _, errOut := runWith(t, []string{"a.png"}, d)
		if code != 1 || !strings.Contains(errOut, "Error uploading a.png: no token") {
			t.Fatalf("code=%d stderr=%q", code, errOut)
		}
	})
	t.Run("fallback notice goes to stderr only, once per run", func(t *testing.T) {
		d := okDeps()
		d.newUploader = func(tokenFlag string, stderr io.Writer) uploadFunc {
			notified := false
			return func(info *repo.Info, p string) (string, error) {
				if !notified {
					notified = true
					fmt.Fprintln(stderr, "Note: fast upload unavailable (HTTP 404); using browser session.")
				}
				return "![" + p + "](url)", nil
			}
		}
		code, out, errOut := runWith(t, []string{"a.png", "b.png"}, d)
		if code != 0 {
			t.Fatalf("code = %d, want 0", code)
		}
		// stdout is piped straight into `gh issue comment`, so it must carry
		// markdown and nothing else.
		if strings.Contains(out, "Note:") {
			t.Errorf("notice leaked into stdout: %q", out)
		}
		if n := strings.Count(errOut, "Note: fast upload unavailable"); n != 1 {
			t.Errorf("notice printed %d times, want 1: %q", n, errOut)
		}
	})
	t.Run("explicit --repo is parsed and passed to resolveRepo", func(t *testing.T) {
		var gotOwner, gotName string
		d := okDeps()
		d.resolveRepo = func(owner, name string) (*repo.Info, error) {
			gotOwner, gotName = owner, name
			return &repo.Info{Owner: owner, Name: name, ID: 9}, nil
		}
		runWith(t, []string{"--repo", "acme/widgets", "a.png"}, d)
		if gotOwner != "acme" || gotName != "widgets" {
			t.Errorf("resolveRepo got %q/%q, want acme/widgets", gotOwner, gotName)
		}
	})
	t.Run("-- terminator treats dash-file as a path and infers repo", func(t *testing.T) {
		var gotOwner, gotName string
		d := okDeps()
		d.resolveRepo = func(owner, name string) (*repo.Info, error) {
			gotOwner, gotName = owner, name
			return &repo.Info{Owner: "octo", Name: "hello", ID: 1}, nil
		}
		code, out, _ := runWith(t, []string{"--", "-shot.png"}, d)
		if code != 0 {
			t.Fatalf("code = %d, want 0", code)
		}
		if gotOwner != "" || gotName != "" {
			t.Errorf("expected inference path (empty owner/name), got %q/%q", gotOwner, gotName)
		}
		if !strings.Contains(out, "![-shot.png](url)") {
			t.Errorf("stdout = %q", out)
		}
	})
}

func TestRun_UsageErrorDispatchShowsUsage(t *testing.T) {
	// A usageError from classifySubcommand prints the usage block alongside the error.
	code, _, errOut := runWith(t, []string{"extract-token", "extra"}, okDeps())
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(errOut, "does not take positional arguments") || !strings.Contains(errOut, "Usage:") {
		t.Errorf("stderr = %q, want error + usage", errOut)
	}
}

func TestResolveSessionCookie_EnvPath(t *testing.T) {
	// Exercises the production resolveSessionCookie via the env var, so it never
	// touches the browser: the env value wins before the browser getter is built.
	t.Setenv("GH_SESSION_TOKEN", "env-token-value")
	cookie, source, err := resolveSessionCookie("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cookie.Value != "env-token-value" || source != "GH_SESSION_TOKEN" {
		t.Errorf("got value=%q source=%q", cookie.Value, source)
	}
}

func TestResolveSessionCookie_NilGetter(t *testing.T) {
	_, _, err := resolveSessionCookieWithGetter("", "", nil)
	if err == nil || !strings.Contains(err.Error(), "browser session getter is unavailable") {
		t.Fatalf("expected nil-getter error, got %v", err)
	}
}

// TestUseBearerRoute covers the rule that keeps an explicitly named account
// from being silently overridden by the gh identity.
func TestUseBearerRoute(t *testing.T) {
	tests := []struct {
		name      string
		tokenFlag string
		envToken  string
		want      bool
	}{
		{"no session token supplied", "", "", true},
		{"--token flag pins the session flow", "tok", "", false},
		{"GH_SESSION_TOKEN pins the session flow", "", "envtok", false},
		{"both supplied", "tok", "envtok", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := useBearerRoute(tc.tokenFlag, tc.envToken); got != tc.want {
				t.Errorf("useBearerRoute(%q, %q) = %v, want %v", tc.tokenFlag, tc.envToken, got, tc.want)
			}
		})
	}
}

func TestProductionDeps_WiringComplete(t *testing.T) {
	d := productionDeps()
	if d.resolveRepo == nil || d.newUploader == nil || d.extractToken == nil || d.checkToken == nil || d.newDownloader == nil {
		t.Fatal("productionDeps left a boundary unwired")
	}
}

func TestClassifySubcommand(t *testing.T) {
	tests := []struct {
		name            string
		paths           []string
		opts            cliOptions
		wantSubcommand  string
		wantErrContains string
		wantUsageError  bool
	}{
		{
			name:           "extract-token selected",
			paths:          []string{"extract-token"},
			wantSubcommand: "extract-token",
		},
		{
			name:           "check-token selected",
			paths:          []string{"check-token"},
			wantSubcommand: "check-token",
		},
		{
			name:           "double-dash treats check-token as filename",
			paths:          []string{"check-token"},
			opts:           cliOptions{firstPosAfterDoubleDash: true},
			wantSubcommand: "",
		},
		{
			name:           "double-dash treats extract-token as filename",
			paths:          []string{"extract-token"},
			opts:           cliOptions{firstPosAfterDoubleDash: true},
			wantSubcommand: "",
		},
		{
			name:            "extract-token with extra args errors",
			paths:           []string{"extract-token", "extra"},
			wantErrContains: "does not take positional arguments",
			wantUsageError:  true,
		},
		{
			name:            "check-token with extra args errors",
			paths:           []string{"check-token", "extra"},
			wantErrContains: "does not take positional arguments",
			wantUsageError:  true,
		},
		{
			name:            "extract-token with token flag errors",
			paths:           []string{"extract-token"},
			opts:            cliOptions{tokenFlag: "abc123"},
			wantErrContains: "--token cannot be combined",
		},
		{
			name:           "non-subcommand remains upload mode",
			paths:          []string{"image.png"},
			wantSubcommand: "",
		},
		{
			name:            "extract-token with repo flag errors",
			paths:           []string{"extract-token"},
			opts:            cliOptions{repoSet: true},
			wantErrContains: "--repo cannot be combined with extract-token",
		},
		{
			name:            "check-token with repo flag errors",
			paths:           []string{"check-token"},
			opts:            cliOptions{repoSet: true},
			wantErrContains: "--repo cannot be combined with check-token",
		},
		{
			name:           "download selected",
			paths:          []string{"download", "https://github.com/user-attachments/assets/x"},
			wantSubcommand: "download",
		},
		{
			name:           "double-dash treats download as filename",
			paths:          []string{"download"},
			opts:           cliOptions{firstPosAfterDoubleDash: true},
			wantSubcommand: "",
		},
		{
			name:            "download with repo flag errors",
			paths:           []string{"download", "url"},
			opts:            cliOptions{repoSet: true},
			wantErrContains: "--repo cannot be combined with download",
		},
		{
			name:            "--output outside download errors",
			paths:           []string{"image.png"},
			opts:            cliOptions{outputSet: true},
			wantErrContains: "--output can only be used with download",
		},
		{
			name:            "--output-dir outside download errors",
			paths:           []string{"image.png"},
			opts:            cliOptions{outputDirSet: true},
			wantErrContains: "--output-dir can only be used with download",
		},
		{
			name:            "--no-clobber outside download errors",
			paths:           []string{"image.png"},
			opts:            cliOptions{noClobber: true},
			wantErrContains: "--no-clobber can only be used with download",
		},
		{
			name:            "--output with check-token errors",
			paths:           []string{"check-token"},
			opts:            cliOptions{outputSet: true},
			wantErrContains: "--output can only be used with download",
		},
		{
			name:            "--no-clobber with no positionals errors",
			paths:           nil,
			opts:            cliOptions{noClobber: true},
			wantErrContains: "--no-clobber can only be used with download",
		},
		{
			name:           "download flags allowed with download",
			paths:          []string{"download", "url"},
			opts:           cliOptions{outputSet: true, noClobber: true},
			wantSubcommand: "download",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotSubcommand, err := classifySubcommand(tc.paths, tc.opts)
			if tc.wantErrContains != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErrContains)
				}
				if !strings.Contains(err.Error(), tc.wantErrContains) {
					t.Fatalf("expected error containing %q, got %q", tc.wantErrContains, err.Error())
				}
				var ue *usageError
				if tc.wantUsageError && !errors.As(err, &ue) {
					t.Error("expected usageError, but errors.As did not match")
				}
				if !tc.wantUsageError && errors.As(err, &ue) {
					t.Error("unexpected usageError")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotSubcommand != tc.wantSubcommand {
				t.Fatalf("expected subcommand %q, got %q", tc.wantSubcommand, gotSubcommand)
			}
		})
	}
}

func TestRun_Download(t *testing.T) {
	t.Run("single URL prints the written path", func(t *testing.T) {
		dl := &stubDownloader{}
		code, out, _ := runWith(t, []string{"download", assetURL}, depsWith(dl))
		if code != 0 {
			t.Fatalf("code=%d", code)
		}
		if len(dl.saved) != 1 || dl.saved[0].URL != assetURL {
			t.Fatalf("saved=%+v", dl.saved)
		}
		if strings.TrimSpace(out) == "" {
			t.Error("expected the written path on stdout")
		}
	})

	t.Run("the download positional is not treated as a URL", func(t *testing.T) {
		dl := &stubDownloader{}
		runWith(t, []string{"download", assetURL}, depsWith(dl))
		for _, ref := range dl.saved {
			if ref.URL == "download" {
				t.Fatal("the literal \"download\" reached ParseRef")
			}
		}
	})

	t.Run("several URLs each download", func(t *testing.T) {
		dl := &stubDownloader{}
		code, out, _ := runWith(t, []string{"download", assetURL, fileURL}, depsWith(dl))
		if code != 0 || len(dl.saved) != 2 {
			t.Fatalf("code=%d saved=%d", code, len(dl.saved))
		}
		if lines := strings.Split(strings.TrimSpace(out), "\n"); len(lines) != 2 {
			t.Fatalf("expected one path per URL, got %q", out)
		}
	})

	t.Run("one failure still writes the others and exits 1", func(t *testing.T) {
		dl := &stubDownloader{failOn: "92463e67"}
		code, out, errOut := runWith(t, []string{"download", assetURL, asset2URL}, depsWith(dl))
		if code != 1 {
			t.Fatalf("code=%d, want 1", code)
		}
		if lines := strings.Split(strings.TrimSpace(out), "\n"); len(lines) != 1 {
			t.Fatalf("expected the surviving path on stdout, got %q", out)
		}
		if !strings.Contains(errOut, "Error downloading") {
			t.Errorf("stderr missing the failure: %q", errOut)
		}
	})

	t.Run("no URLs is a usage error", func(t *testing.T) {
		code, _, errOut := runWith(t, []string{"download"}, okDeps())
		if code != 1 || !strings.Contains(errOut, "at least one") {
			t.Fatalf("code=%d err=%q", code, errOut)
		}
	})

	t.Run("a non-github URL is rejected before any request", func(t *testing.T) {
		dl := &stubDownloader{}
		code, _, errOut := runWith(t, []string{"download", "https://example.com/user-attachments/assets/x"}, depsWith(dl))
		if code != 1 || len(dl.saved) != 0 {
			t.Fatalf("code=%d saved=%d", code, len(dl.saved))
		}
		if !strings.Contains(errOut, "github.com") {
			t.Errorf("stderr=%q", errOut)
		}
	})

	t.Run("--repo is rejected", func(t *testing.T) {
		code, _, errOut := runWith(t, []string{"download", "--repo", "o/r", assetURL}, okDeps())
		if code != 1 || !strings.Contains(errOut, "--repo cannot be combined with download") {
			t.Fatalf("code=%d err=%q", code, errOut)
		}
	})
}

func TestRun_DownloadOutputModes(t *testing.T) {
	t.Run("--output - streams to run's stdout and writes no file", func(t *testing.T) {
		dl := &stubDownloader{body: "raw-bytes"}
		code, out, _ := runWith(t, []string{"download", "--output", "-", assetURL}, depsWith(dl))
		if code != 0 {
			t.Fatalf("code=%d", code)
		}
		if out != "raw-bytes" {
			t.Fatalf("stdout=%q, want the asset body", out)
		}
		if len(dl.saved) != 0 {
			t.Error("Save was called in stdout mode")
		}
		if len(dl.streams) != 1 {
			t.Error("Stream was not called")
		}
	})

	t.Run("--output sets an exact destination", func(t *testing.T) {
		dir := t.TempDir()
		exact := filepath.Join(dir, "chosen.png")
		dl := &stubDownloader{}
		if code, _, errOut := runWith(t, []string{"download", "--output", exact, assetURL}, depsWith(dl)); code != 0 {
			t.Fatalf("code=%d err=%q", code, errOut)
		}
		if len(dl.dests) != 1 || dl.dests[0].Exact != exact || dl.dests[0].Dir != "" {
			t.Fatalf("dest=%+v", dl.dests)
		}
	})

	t.Run("--output-dir sets the directory and creates it", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "nested", "out")
		dl := &stubDownloader{}
		if code, _, errOut := runWith(t, []string{"download", "--output-dir", dir, assetURL}, depsWith(dl)); code != 0 {
			t.Fatalf("code=%d err=%q", code, errOut)
		}
		if len(dl.dests) != 1 || dl.dests[0].Dir != dir {
			t.Fatalf("dest=%+v", dl.dests)
		}
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("--output-dir was not created: %v", err)
		}
	})

	t.Run("no output flag defaults to the current directory", func(t *testing.T) {
		dl := &stubDownloader{}
		runWith(t, []string{"download", assetURL}, depsWith(dl))
		if len(dl.dests) != 1 || dl.dests[0].Dir != "." || dl.dests[0].Exact != "" {
			t.Fatalf("dest=%+v", dl.dests)
		}
	})

	t.Run("--no-clobber reaches the downloader", func(t *testing.T) {
		dl := &stubDownloader{}
		runWith(t, []string{"download", "--no-clobber", assetURL}, depsWith(dl))
		if len(dl.dests) != 1 || !dl.dests[0].NoClobber {
			t.Fatalf("dest=%+v", dl.dests)
		}
	})

	t.Run("--output with several URLs is an error", func(t *testing.T) {
		for _, value := range []string{"out.png", "-"} {
			dl := &stubDownloader{}
			code, _, errOut := runWith(t, []string{"download", "--output", value, assetURL, asset2URL}, depsWith(dl))
			if code != 1 || !strings.Contains(errOut, "single URL") {
				t.Errorf("--output %s: code=%d err=%q", value, code, errOut)
			}
			if len(dl.saved) != 0 || len(dl.streams) != 0 {
				t.Errorf("--output %s: downloaded despite the usage error", value)
			}
		}
	})

	t.Run("--output and --output-dir cannot be combined", func(t *testing.T) {
		code, _, errOut := runWith(t, []string{"download", "--output", "a", "--output-dir", "b", assetURL}, okDeps())
		if code != 1 || !strings.Contains(errOut, "cannot be combined") {
			t.Fatalf("code=%d err=%q", code, errOut)
		}
	})
}

func TestRun_DownloadFlagParsing(t *testing.T) {
	dl := &stubDownloader{}
	t.Run("--output=value form works", func(t *testing.T) {
		dir := t.TempDir()
		exact := filepath.Join(dir, "x.png")
		d := &stubDownloader{}
		if code, _, e := runWith(t, []string{"download", "--output=" + exact, assetURL}, depsWith(d)); code != 0 {
			t.Fatalf("code=%d err=%q", code, e)
		}
		if d.dests[0].Exact != exact {
			t.Fatalf("dest=%+v", d.dests)
		}
	})

	t.Run("--output-dir=value form works", func(t *testing.T) {
		dir := t.TempDir()
		d := &stubDownloader{}
		if code, _, e := runWith(t, []string{"download", "--output-dir=" + dir, assetURL}, depsWith(d)); code != 0 {
			t.Fatalf("code=%d err=%q", code, e)
		}
		if d.dests[0].Dir != dir {
			t.Fatalf("dest=%+v", d.dests)
		}
	})

	rejected := map[string][]string{
		"duplicate --output across forms": {"download", "--output", "a", "--output=b", assetURL},
		"duplicate --output-dir":          {"download", "--output-dir", "a", "--output-dir", "b", assetURL},
		"duplicate --no-clobber":          {"download", "--no-clobber", "--no-clobber", assetURL},
		"--output missing value":          {"download", assetURL, "--output"},
		"--output-dir missing value":      {"download", assetURL, "--output-dir"},
		"--output empty value":            {"download", "--output=", assetURL},
		"--output-dir empty value":        {"download", "--output-dir=", assetURL},
		"--output in upload mode":         {"--output", "x", "a.png"},
		"--output-dir in upload mode":     {"--output-dir", "x", "a.png"},
		"--no-clobber in upload mode":     {"--no-clobber", "a.png"},
		"--no-clobber with extract-token": {"--no-clobber", "extract-token"},
	}
	for name, args := range rejected {
		t.Run(name, func(t *testing.T) {
			if code, _, errOut := runWith(t, args, depsWith(dl)); code != 1 {
				t.Fatalf("expected exit 1, got %d (stderr=%q)", code, errOut)
			}
		})
	}

	t.Run("a missing flag value prints the usage block", func(t *testing.T) {
		_, _, errOut := runWith(t, []string{"download", assetURL, "--output"}, depsWith(&stubDownloader{}))
		if !strings.Contains(errOut, "requires a value") || !strings.Contains(errOut, "Usage:") {
			t.Fatalf("stderr lacks the usage block: %q", errOut)
		}
	})

	t.Run("-- makes download a filename, not the subcommand", func(t *testing.T) {
		dl := &stubDownloader{}
		code, out, _ := runWith(t, []string{"--", "download"}, depsWith(dl))
		if code != 0 || !strings.Contains(out, "![download](url)") {
			t.Fatalf("code=%d out=%q — expected an upload of a file named download", code, out)
		}
		if len(dl.saved) != 0 {
			t.Error("download ran despite the -- terminator")
		}
	})

	t.Run("--token reaches the credential resolver", func(t *testing.T) {
		var gotToken string
		dl := &stubDownloader{}
		d := okDeps()
		d.newDownloader = func(tokenFlag string, stderr io.Writer) downloader {
			gotToken = tokenFlag
			return dl
		}
		runWith(t, []string{"download", "--token", "abc123", assetURL}, d)
		if gotToken != "abc123" {
			t.Fatalf("newDownloader saw %q", gotToken)
		}
	})

	t.Run("credential failure surfaces per URL and exits 1", func(t *testing.T) {
		// Credentials are resolved lazily inside the client now, so the failure
		// arrives from Save rather than from building the downloader.
		dl := &stubDownloader{failOn: "9f57198c"}
		code, _, errOut := runWith(t, []string{"download", assetURL}, depsWith(dl))
		if code != 1 || !strings.Contains(errOut, "Error downloading") {
			t.Fatalf("code=%d err=%q", code, errOut)
		}
	})
}

func TestRun_HelpDocumentsDownload(t *testing.T) {
	_, out, _ := runWith(t, []string{"--help"}, okDeps())
	for _, want := range []string{"download", "--output", "--output-dir", "--no-clobber", "session token"} {
		if !strings.Contains(out, want) {
			t.Errorf("--help does not mention %q", want)
		}
	}
}
