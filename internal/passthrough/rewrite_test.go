package passthrough

import (
	"strings"
	"testing"
)

func TestRewriteBody(t *testing.T) {
	tests := []struct {
		name string
		src  string
		path string
		url  string
		add  string
		want string
		err  string
	}{
		{"inline image", "![alt](./shot.png)", "./shot.png", "https://url", "", "![alt](https://url)", ""},
		{"inline link", "[doc](./report.pdf)", "./report.pdf", "https://url", "", "[doc](https://url)", ""},
		{"inline link with whitespace", "[doc](  ./report.pdf)", "./report.pdf", "https://url", "", "[doc](  https://url)", ""},
		{"nested inline link", "*see [doc](./report.pdf)*", "./report.pdf", "https://url", "", "*see [doc](https://url)*", ""},
		{"raw text before link", "raw ](./shot.png) [real](./shot.png)", "./shot.png", "https://url", "", "raw ](./shot.png) [real](https://url)", ""},
		{"escaped bracket in label", `[label \](./shot.png)](./shot.png)`, "./shot.png", "https://url", "", `[label \](./shot.png)](https://url)`, ""},
		{"multiple matches", "![a](./shot.png) and [b](./shot.png)", "./shot.png", "https://url", "", "![a](https://url) and [b](https://url)", ""},
		{"reference style", "![alt][1]\n\n[1]: ./shot.png", "./shot.png", "https://url", "", "", "reference-style"},
		{"unmatched image", "text", "./shot.png", "https://url", "![shot.png](https://url)", "text\n\n![shot.png](https://url)", ""},
		{"unmatched video", "text", "./clip.mp4", "https://url", "https://url", "text\n\nhttps://url", ""},
		{"unmatched other", "text", "./report.pdf", "https://url", "[report.pdf](https://url)", "text\n\n[report.pdf](https://url)", ""},
		{"partial match", "![a](./shot.png)", "./report.pdf", "https://url", "[report.pdf](https://url)", "![a](./shot.png)\n\n[report.pdf](https://url)", ""},
		{"code span", "`![alt](./shot.png)`", "./shot.png", "https://url", "![shot.png](https://url)", "`![alt](./shot.png)`\n\n![shot.png](https://url)", ""},
		{"code span with real link", "Look at `(./shot.png)` real: ![a](./shot.png)", "./shot.png", "https://url", "", "Look at `(./shot.png)` real: ![a](https://url)", ""},
		{"full link syntax in code span", "`![a](./path)` real: ![b](./path)", "./path", "https://url", "", "`![a](./path)` real: ![b](https://url)", ""},
		{"alt preserved", "![custom alt](./shot.png)", "./shot.png", "https://url", "", "![custom alt](https://url)", ""},
		{"title preserved", `[doc](./report.pdf "see PDF")`, "./report.pdf", "https://url", "", `[doc](https://url "see PDF")`, ""},
		{"escaped destination", `[doc](./report\_file.pdf)`, "./report_file.pdf", "https://url", "", `[doc](https://url)`, ""},
		{"escaped hyphen destination", `[doc](./report\-file.pdf)`, "./report-file.pdf", "https://url", "", `[doc](https://url)`, ""},
		{"escaped backticks around label and destination", "[la\\`bel](./foo\\`bar.pdf)", "./foo`bar.pdf", "https://url", "", "[la\\`bel](https://url)", ""},
		{"angle destination", "[doc](<./report.pdf>)", "./report.pdf", "https://url", "", "[doc](<https://url>)", ""},
		{"angle title", `![a](<./shot.png> "cap")`, "./shot.png", "https://url", "", `![a](<https://url> "cap")`, ""},
		{"mixed destinations", "[a](./path) [b](<./path>)", "./path", "https://url", "", "[a](https://url) [b](<https://url>)", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := RewriteBody(tt.src, []string{tt.path}, []string{tt.url}, []string{tt.add})
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("RewriteBody() error = %v, want substring %q", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("RewriteBody() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("RewriteBody() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRewriteBody_DuplicatePaths(t *testing.T) {
	got, err := RewriteBody("![one](./shot.png) ![two](./shot.png)",
		[]string{"./shot.png", "./shot.png"},
		[]string{"https://one", "https://two"},
		[]string{"![shot.png](https://one)", "![shot.png](https://two)"})
	if err != nil {
		t.Fatalf("RewriteBody() error = %v", err)
	}
	want := "![one](https://one) ![two](https://two)"
	if got != want {
		t.Errorf("RewriteBody() = %q, want %q", got, want)
	}
}

func TestRewriteBody_RejectsMismatchedInputs(t *testing.T) {
	if _, err := RewriteBody("", []string{"a"}, nil, []string{"b"}); err == nil {
		t.Fatal("RewriteBody() error = nil, want mismatched input error")
	}
}

func TestRewriteBody_FencedCode(t *testing.T) {
	src := "```\n![alt](./shot.png)\n```"
	got, err := RewriteBody(src, []string{"./shot.png"}, []string{"https://url"}, []string{"![shot.png](https://url)"})
	if err != nil {
		t.Fatalf("RewriteBody() error = %v", err)
	}
	if got != src+"\n\n![shot.png](https://url)" {
		t.Errorf("RewriteBody() = %q, want fenced block unchanged with append", got)
	}
}
