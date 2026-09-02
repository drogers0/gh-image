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
	// BodyIndexes are the ghArgv positions the body flags occupy, flags and
	// their values alike. Rebuilding the argv drops exactly these, so a value
	// that merely looks like a flag is never mistaken for one.
	BodyIndexes []int
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
		// --title is the one upstream flag whose value plausibly starts with a
		// dash ("-Fix the crash"). Consuming it here keeps that value from
		// reading as one of the flags below, the way gh's own parser treats it.
		case arg == "--title" || arg == "-t":
			i++
		case arg == "--body" || arg == "-b" || arg == "--body-file" || arg == "-F":
			result.BodyFlag = arg
			if i+1 >= len(ghArgv) {
				return nil, fmt.Errorf("%s requires a value", arg)
			}
			result.BodyIndexes = append(result.BodyIndexes, i, i+1)
			i++
			result.BodyValue = ghArgv[i]
			if isBodyFileFlag(arg) && result.BodyValue == "" {
				return nil, fmt.Errorf("%s value cannot be empty", arg)
			}
		case strings.HasPrefix(arg, "--body=") || strings.HasPrefix(arg, "-b=") ||
			strings.HasPrefix(arg, "--body-file=") || strings.HasPrefix(arg, "-F="):
			result.BodyFlag, result.BodyValue = splitFlag(arg)
			result.BodyIndexes = append(result.BodyIndexes, i)
			if isBodyFileFlag(result.BodyFlag) && result.BodyValue == "" {
				return nil, fmt.Errorf("%s value cannot be empty", result.BodyFlag)
			}
		case strings.HasPrefix(arg, "-b") && !strings.HasPrefix(arg, "--"):
			result.BodyFlag, result.BodyValue = "-b", arg[2:]
			result.BodyIndexes = append(result.BodyIndexes, i)
		case strings.HasPrefix(arg, "-F") && !strings.HasPrefix(arg, "--"):
			result.BodyFlag, result.BodyValue = "-F", arg[2:]
			result.BodyIndexes = append(result.BodyIndexes, i)
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
		// Files come from the left of --, which both routes honour. Silently
		// dropping an --attach here would lose a file on the gh-image route.
		case arg == "--attach" || strings.HasPrefix(arg, "--attach="):
			return nil, fmt.Errorf("--attach is not supported right of --; list the files left of it instead: gh image <file>... -- %s", key)
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
