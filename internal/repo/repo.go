package repo

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Info holds the owner, repo name, and numeric ID for a GitHub repository.
type Info struct {
	Owner string
	Name  string
	ID    int
}

// runner runs an external command and returns its stdout. Injected so the
// git/gh subprocess boundary is stubbable in tests.
type runner func(name string, args ...string) ([]byte, error)

// execRun is the production runner; it shells out via os/exec.
func execRun(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

var (
	sshRemoteRe   = regexp.MustCompile(`git@github\.com:([^/]+)/([^/]+?)(?:\.git)?$`)
	httpsRemoteRe = regexp.MustCompile(`https://github\.com/([^/]+)/([^/]+?)(?:\.git)?$`)
)

// fromRemote infers the GitHub owner/repo from the git remote in the current directory.
func fromRemote(run runner) (owner, name string, err error) {
	out, err := run("git", "remote", "get-url", "origin")
	if err != nil {
		return "", "", fmt.Errorf("not a git repository or no 'origin' remote configured")
	}
	remote := strings.TrimSpace(string(out))

	if m := sshRemoteRe.FindStringSubmatch(remote); m != nil {
		return m[1], m[2], nil
	}
	if m := httpsRemoteRe.FindStringSubmatch(remote); m != nil {
		return m[1], m[2], nil
	}

	return "", "", fmt.Errorf("could not parse GitHub owner/repo from remote URL: %s", remote)
}

// lookupID resolves the numeric repository ID via the gh CLI.
func lookupID(run runner, owner, name string) (int, error) {
	out, err := run("gh", "api", fmt.Sprintf("repos/%s/%s", owner, name), "--jq", ".id")
	if err != nil {
		return 0, fmt.Errorf("failed to look up repo ID for %s/%s (is gh CLI installed and authenticated?): %w", owner, name, err)
	}
	id, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("unexpected repo ID format: %s", strings.TrimSpace(string(out)))
	}
	return id, nil
}

// resolve returns full repo info, inferring owner/name from the git remote when empty.
func resolve(run runner, owner, name string) (*Info, error) {
	if owner == "" || name == "" {
		var err error
		owner, name, err = fromRemote(run)
		if err != nil {
			return nil, err
		}
	}

	id, err := lookupID(run, owner, name)
	if err != nil {
		return nil, err
	}

	return &Info{Owner: owner, Name: name, ID: id}, nil
}

// Resolve returns full repo info. If owner/name are empty, it infers from the git remote.
func Resolve(owner, name string) (*Info, error) {
	return resolve(execRun, owner, name)
}
