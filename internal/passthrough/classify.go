package passthrough

import (
	"fmt"
	"strings"
)

// Kind categorizes the gh subcommand for routing.
type Kind int

const (
	KindCreate Kind = iota
	KindEdit
	KindComment
)

// Result is the parsed classification of the gh argv right of --.
type Result struct {
	Kind       Kind
	Subcommand string
	BodyFlag   string
	BodyValue  string
	Repo       string
	TargetNum  string
}

var known = map[string]Kind{
	"pr create":     KindCreate,
	"pr new":        KindCreate,
	"pr edit":       KindEdit,
	"pr comment":    KindComment,
	"issue create":  KindCreate,
	"issue new":     KindCreate,
	"issue edit":    KindEdit,
	"issue comment": KindComment,
}

// Classify parses ghArgv (the args right of --) and returns its classification.
func Classify(ghArgv []string) (*Result, error) {
	if len(ghArgv) < 2 {
		return nil, fmt.Errorf("unrecognized command right of --: fewer than two arguments")
	}

	key := ghArgv[0] + " " + ghArgv[1]
	kind, ok := known[key]
	if !ok {
		return nil, fmt.Errorf("unrecognized command right of --: %q (expected one of: pr create, pr new, pr edit, pr comment, issue create, issue new, issue edit, issue comment; user-defined gh aliases are not expanded)", key)
	}

	result := &Result{Kind: kind, Subcommand: key}
	for i := 2; i < len(ghArgv); i++ {
		arg := ghArgv[i]
		switch {
		case arg == "--body" || arg == "-b" || arg == "--body-file" || arg == "-F":
			result.BodyFlag = arg
			if i+1 >= len(ghArgv) {
				return nil, fmt.Errorf("%s requires a value", arg)
			}
			i++
			result.BodyValue = ghArgv[i]
			if isBodyFileFlag(arg) && result.BodyValue == "" {
				return nil, fmt.Errorf("%s value cannot be empty", arg)
			}
		case strings.HasPrefix(arg, "--body=") || strings.HasPrefix(arg, "-b=") ||
			strings.HasPrefix(arg, "--body-file=") || strings.HasPrefix(arg, "-F="):
			result.BodyFlag, result.BodyValue = splitFlag(arg)
			if isBodyFileFlag(result.BodyFlag) && result.BodyValue == "" {
				return nil, fmt.Errorf("%s value cannot be empty", result.BodyFlag)
			}
		case strings.HasPrefix(arg, "-b") && !strings.HasPrefix(arg, "--"):
			result.BodyFlag, result.BodyValue = "-b", arg[2:]
		case strings.HasPrefix(arg, "-F") && !strings.HasPrefix(arg, "--"):
			result.BodyFlag, result.BodyValue = "-F", arg[2:]
			if result.BodyValue == "" {
				return nil, fmt.Errorf("%s value cannot be empty", result.BodyFlag)
			}
		case arg == "--repo" || arg == "-R":
			if i+1 >= len(ghArgv) {
				return nil, fmt.Errorf("%s requires a value", arg)
			}
			i++
			result.Repo = ghArgv[i]
			if looksLikeFlag(result.Repo) {
				return nil, fmt.Errorf("%s requires a value", arg)
			}
			if result.Repo == "" {
				return nil, fmt.Errorf("%s value cannot be empty", arg)
			}
		case strings.HasPrefix(arg, "--repo=") || strings.HasPrefix(arg, "-R="):
			flag, value := splitFlag(arg)
			if value == "" {
				return nil, fmt.Errorf("%s value cannot be empty", flag)
			}
			result.Repo = value
		case strings.HasPrefix(arg, "-R") && !strings.HasPrefix(arg, "--"):
			result.Repo = arg[2:]
			if result.Repo == "" {
				return nil, fmt.Errorf("-R value cannot be empty")
			}
		case arg == "--attach":
			if i+1 >= len(ghArgv) {
				return nil, fmt.Errorf("%s requires a value", arg)
			}
			i++
			if looksLikeFlag(ghArgv[i]) {
				return nil, fmt.Errorf("%s requires a value", arg)
			}
			if ghArgv[i] == "" {
				return nil, fmt.Errorf("%s value cannot be empty", arg)
			}
		case strings.HasPrefix(arg, "--attach="):
			if _, value := splitFlag(arg); value == "" {
				return nil, fmt.Errorf("--attach value cannot be empty")
			}
		case result.TargetNum == "" && !strings.HasPrefix(arg, "-"):
			result.TargetNum = arg
		}
	}
	return result, nil
}

func splitFlag(arg string) (string, string) {
	parts := strings.SplitN(arg, "=", 2)
	return parts[0], parts[1]
}

func isBodyFileFlag(flag string) bool {
	return flag == "--body-file" || flag == "-F"
}

func looksLikeFlag(value string) bool {
	if strings.HasPrefix(value, "--") {
		return true
	}
	switch value {
	case "-b", "-F", "-R":
		return true
	default:
		return false
	}
}
