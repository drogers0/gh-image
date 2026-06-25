package repo

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// fakeRunner answers `git` and `gh` invocations from canned output/errors and
// records the args each was called with.
type fakeRunner struct {
	gitOut, ghOut string
	gitErr, ghErr error
	gotGit, gotGh [][]string
}

func (f *fakeRunner) run(name string, args ...string) ([]byte, error) {
	switch name {
	case "git":
		f.gotGit = append(f.gotGit, args)
		return []byte(f.gitOut), f.gitErr
	case "gh":
		f.gotGh = append(f.gotGh, args)
		return []byte(f.ghOut), f.ghErr
	default:
		return nil, fmt.Errorf("unexpected command %q", name)
	}
}

func TestFromRemote(t *testing.T) {
	cases := []struct {
		name        string
		gitOut      string
		gitErr      error
		wantOwner   string
		wantName    string
		errContains string
	}{
		{name: "ssh with .git", gitOut: "git@github.com:octocat/hello.git\n", wantOwner: "octocat", wantName: "hello"},
		{name: "ssh without .git", gitOut: "git@github.com:octocat/hello", wantOwner: "octocat", wantName: "hello"},
		{name: "https with .git", gitOut: "https://github.com/octocat/hello.git\n", wantOwner: "octocat", wantName: "hello"},
		{name: "https without .git", gitOut: "https://github.com/octocat/hello", wantOwner: "octocat", wantName: "hello"},
		{name: "git command error", gitErr: fmt.Errorf("exit 128"), errContains: "not a git repository"},
		{name: "non-github remote", gitOut: "https://gitlab.com/x/y.git", errContains: "could not parse GitHub owner/repo from remote URL: https://gitlab.com/x/y.git"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeRunner{gitOut: tc.gitOut, gitErr: tc.gitErr}
			owner, name, err := fromRemote(f.run)
			if tc.errContains != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.errContains)
				}
				if got := err.Error(); !strings.Contains(got, tc.errContains) {
					t.Fatalf("error = %q, want substring %q", got, tc.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if owner != tc.wantOwner || name != tc.wantName {
				t.Fatalf("got %s/%s, want %s/%s", owner, name, tc.wantOwner, tc.wantName)
			}
		})
	}
}

func TestLookupID(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		f := &fakeRunner{ghOut: "12345\n"}
		id, err := lookupID(f.run, "octocat", "hello")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != 12345 {
			t.Errorf("id = %d, want 12345", id)
		}
		want := []string{"api", "repos/octocat/hello", "--jq", ".id"}
		if len(f.gotGh) != 1 || !reflect.DeepEqual(f.gotGh[0], want) {
			t.Errorf("gh args = %v, want %v", f.gotGh, want)
		}
	})

	t.Run("gh command error", func(t *testing.T) {
		f := &fakeRunner{ghErr: fmt.Errorf("not authenticated")}
		_, err := lookupID(f.run, "octocat", "hello")
		if err == nil || !strings.Contains(err.Error(), "failed to look up repo ID for octocat/hello") {
			t.Fatalf("expected lookup error, got %v", err)
		}
	})

	t.Run("non-numeric output", func(t *testing.T) {
		f := &fakeRunner{ghOut: "not-a-number\n"}
		_, err := lookupID(f.run, "octocat", "hello")
		if err == nil || !strings.Contains(err.Error(), "unexpected repo ID format: not-a-number") {
			t.Fatalf("expected format error, got %v", err)
		}
	})
}

func TestResolve(t *testing.T) {
	t.Run("explicit owner/name skips git", func(t *testing.T) {
		f := &fakeRunner{ghOut: "42"}
		info, err := resolve(f.run, "octocat", "hello")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if *info != (Info{Owner: "octocat", Name: "hello", ID: 42}) {
			t.Errorf("info = %+v", info)
		}
		if len(f.gotGit) != 0 {
			t.Errorf("git should not be called when owner/name are explicit, got %v", f.gotGit)
		}
	})

	t.Run("empty owner infers from remote", func(t *testing.T) {
		f := &fakeRunner{gitOut: "git@github.com:octocat/hello.git\n", ghOut: "7"}
		info, err := resolve(f.run, "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if *info != (Info{Owner: "octocat", Name: "hello", ID: 7}) {
			t.Errorf("info = %+v", info)
		}
	})

	t.Run("fromRemote error propagates", func(t *testing.T) {
		f := &fakeRunner{gitErr: fmt.Errorf("no remote")}
		if _, err := resolve(f.run, "", ""); err == nil {
			t.Fatal("expected error from fromRemote, got nil")
		}
	})

	t.Run("lookupID error propagates", func(t *testing.T) {
		f := &fakeRunner{ghErr: fmt.Errorf("boom")}
		if _, err := resolve(f.run, "octocat", "hello"); err == nil {
			t.Fatal("expected error from lookupID, got nil")
		}
	})
}
