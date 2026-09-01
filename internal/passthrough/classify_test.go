package passthrough

import (
	"strings"
	"testing"
)

func TestClassifyCommands(t *testing.T) {
	for command, wantKind := range known {
		t.Run(command, func(t *testing.T) {
			got, err := Classify(strings.Fields(command))
			if err != nil {
				t.Fatalf("Classify() error = %v", err)
			}
			if got.Kind != wantKind || got.Subcommand != command {
				t.Fatalf("Classify() = %#v, want kind %d and command %q", got, wantKind, command)
			}
		})
	}
}

func TestClassifyFlags(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		bodyFlag  string
		bodyValue string
		repo      string
		target    string
	}{
		{"body space", []string{"pr", "create", "--body", "text"}, "--body", "text", "", ""},
		{"body equals", []string{"pr", "create", "--body=text"}, "--body", "text", "", ""},
		{"short body space", []string{"pr", "create", "-b", "text"}, "-b", "text", "", ""},
		{"short body equals", []string{"pr", "create", "-b=text"}, "-b", "text", "", ""},
		{"short body attached", []string{"pr", "create", "-btext"}, "-b", "text", "", ""},
		{"dash-prefixed body", []string{"pr", "create", "--body", "- see attached"}, "--body", "- see attached", "", ""},
		{"flag-prefixed body", []string{"pr", "create", "--body", "--fill"}, "--body", "--fill", "", ""},
		{"dash-prefixed body file", []string{"pr", "create", "--body-file", "-draft.md"}, "--body-file", "-draft.md", "", ""},
		{"body file space", []string{"pr", "create", "--body-file", "body.md"}, "--body-file", "body.md", "", ""},
		{"body file equals", []string{"pr", "create", "--body-file=body.md"}, "--body-file", "body.md", "", ""},
		{"short body file space", []string{"pr", "create", "-F", "body.md"}, "-F", "body.md", "", ""},
		{"short body file equals", []string{"pr", "create", "-F=body.md"}, "-F", "body.md", "", ""},
		{"short body file attached", []string{"pr", "create", "-Fbody.md"}, "-F", "body.md", "", ""},
		{"attach space", []string{"pr", "create", "--attach", "file.png"}, "", "", "", ""},
		{"attach equals", []string{"pr", "create", "--attach=file.png"}, "", "", "", ""},
		{"repo long space", []string{"pr", "create", "--repo", "owner/name"}, "", "", "owner/name", ""},
		{"repo short space", []string{"pr", "create", "-R", "owner/name"}, "", "", "owner/name", ""},
		{"repo long equals", []string{"pr", "create", "--repo=owner/name"}, "", "", "owner/name", ""},
		{"repo short equals", []string{"pr", "create", "-R=owner/name"}, "", "", "owner/name", ""},
		{"repo short attached", []string{"pr", "create", "-Rowner/name"}, "", "", "owner/name", ""},
		{"target after flags", []string{"issue", "comment", "--repo", "owner/name", "87"}, "", "", "owner/name", "87"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Classify(tt.args)
			if err != nil {
				t.Fatalf("Classify() error = %v", err)
			}
			if got.BodyFlag != tt.bodyFlag || got.BodyValue != tt.bodyValue || got.Repo != tt.repo || got.TargetNum != tt.target {
				t.Fatalf("Classify() = %#v", got)
			}
		})
	}
}

func TestClassifyTargetAndErrors(t *testing.T) {
	for _, args := range [][]string{{"pr", "create"}, {"pr", "edit"}} {
		got, err := Classify(args)
		if err != nil {
			t.Fatalf("Classify(%v) error = %v", args, err)
		}
		if got.TargetNum != "" {
			t.Errorf("Classify(%v).TargetNum = %q, want empty", args, got.TargetNum)
		}
	}

	for _, args := range [][]string{nil, {"pr"}} {
		if _, err := Classify(args); err == nil {
			t.Errorf("Classify(%v) error = nil", args)
		}
	}
	if _, err := Classify([]string{"pr", "merge"}); err == nil || !strings.Contains(err.Error(), "unrecognized command right of --") {
		t.Fatalf("Classify() error = %v, want unrecognized command error", err)
	}
	for _, args := range [][]string{
		{"pr", "create", "--body"},
		{"pr", "create", "--body-file="},
		{"pr", "create", "--repo"},
		{"pr", "create", "--repo="},
		{"pr", "create", "--attach"},
		{"pr", "create", "--attach", "--title"},
		{"pr", "create", "--attach", ""},
		{"pr", "create", "--attach="},
	} {
		if _, err := Classify(args); err == nil {
			t.Errorf("Classify(%v) error = nil", args)
		}
	}
}
