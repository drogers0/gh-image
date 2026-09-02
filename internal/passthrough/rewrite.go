package passthrough

import (
	"bytes"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// RewriteBody replaces matching inline link and image destinations, rejects
// matching reference-style links, and appends references for unmatched paths.
func RewriteBody(src string, paths, urls, appendMarkdowns []string) (string, error) {
	if len(paths) != len(urls) || len(paths) != len(appendMarkdowns) {
		return "", fmt.Errorf("paths, urls, and appendMarkdowns must have equal lengths")
	}
	srcBytes := []byte(src)
	doc := goldmark.New().Parser().Parse(text.NewReader(srcBytes))

	type edit struct {
		start, end  int
		replacement []byte
	}
	var edits []edit
	matched := make([]bool, len(paths))
	pathIndexes := make(map[string][]int, len(paths))
	for i, path := range paths {
		key := absPath(path)
		pathIndexes[key] = append(pathIndexes[key], i)
	}
	pathUses := make(map[string]int, len(pathIndexes))
	var walkErr error

	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || walkErr != nil {
			return ast.WalkContinue, nil
		}
		var dest []byte
		var reference bool
		switch node := n.(type) {
		case *ast.Image:
			dest = node.Destination
			reference = node.Reference != nil
		case *ast.Link:
			dest = node.Destination
			reference = node.Reference != nil
		default:
			return ast.WalkContinue, nil
		}

		lookup, indexes := "", []int(nil)
		for _, candidate := range destinationKeys(dest) {
			if found := pathIndexes[candidate]; len(found) > 0 {
				lookup, indexes = candidate, found
				break
			}
		}
		if len(indexes) == 0 {
			return ast.WalkContinue, nil
		}
		use := pathUses[lookup]
		idx := indexes[use]
		if use < len(indexes)-1 {
			pathUses[lookup] = use + 1
		}
		matched[idx] = true
		if reference {
			walkErr = fmt.Errorf("reference-style link to %q is not supported; use an inline link: ![alt](%s)", dest, dest)
			return ast.WalkStop, nil
		}
		start, end, found := findDestination(srcBytes, n, dest)
		if !found {
			walkErr = fmt.Errorf("reference-style link to %q is not supported; use an inline link: ![alt](%s)", dest, dest)
			return ast.WalkStop, nil
		}
		edits = append(edits, edit{start: start, end: end, replacement: []byte(urls[idx])})
		return ast.WalkContinue, nil
	})
	if walkErr != nil {
		return "", walkErr
	}

	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	result := srcBytes
	for _, e := range edits {
		result = append(result[:e.start], append(e.replacement, result[e.end:]...)...)
	}

	out := string(result)
	for i, isMatched := range matched {
		if !isMatched {
			out = appendMarkdown(out, appendMarkdowns[i])
		}
	}
	return out, nil
}

// absPath keys a path for matching. Both sides resolve through it, so a body
// that writes shot.png matches a command line that wrote ./shot.png, the way
// upstream's own rewriter matches on the absolute path.
func absPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

// destinationKeys returns the match keys a markdown destination can produce:
// as written, and with markdown's backslash escapes removed. A destination
// naming somewhere other than the filesystem produces none.
func destinationKeys(dest []byte) []string {
	raw := string(dest)
	if raw == "" || strings.HasPrefix(raw, "#") || isRemoteDestination(raw) {
		return nil
	}
	keys := []string{absPath(raw)}
	if unescaped := unescapeDestination(dest); unescaped != raw {
		keys = append(keys, absPath(unescaped))
	}
	return keys
}

// isRemoteDestination reports whether dest carries a URL scheme. A one-letter
// scheme is a Windows drive rather than a protocol.
func isRemoteDestination(dest string) bool {
	parsed, err := url.Parse(dest)
	return err == nil && len(parsed.Scheme) > 1
}

func unescapeDestination(dest []byte) string {
	var out strings.Builder
	for i := 0; i < len(dest); i++ {
		if dest[i] == '\\' && i+1 < len(dest) && strings.ContainsRune("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", rune(dest[i+1])) {
			i++
		}
		out.WriteByte(dest[i])
	}
	return out.String()
}

func findDestination(src []byte, n ast.Node, dest []byte) (int, int, bool) {
	p := n.Parent()
	for p != nil && p.Type() != ast.TypeBlock {
		p = p.Parent()
	}
	if p == nil {
		return 0, 0, false
	}

	if p.Lines().Len() == 0 {
		return 0, 0, false
	}
	first := p.Lines().At(0)
	last := p.Lines().At(p.Lines().Len() - 1)
	blockStart, blockEnd := first.Start-first.Padding, last.Stop
	start := n.Pos()
	if start < blockStart || start >= blockEnd || start >= len(src) {
		return 0, 0, false
	}
	open := start
	if src[open] == '!' {
		open++
	}
	if open >= blockEnd || src[open] != '[' {
		return 0, 0, false
	}

	spans := codeSpanRanges(src[open:blockEnd])
	depth, close := 0, -1
	for i := open + 1; i < blockEnd; i++ {
		if inAnyRange(i-open, spans) {
			continue
		}
		if src[i] == '\\' {
			i++
			continue
		}
		switch src[i] {
		case '[':
			depth++
		case ']':
			if depth == 0 {
				close = i
				i = blockEnd
			} else {
				depth--
			}
		}
	}
	if close < 0 || close+1 >= blockEnd || src[close+1] != '(' {
		return 0, 0, false
	}
	return destinationRange(src, close+2, blockEnd, dest)
}

func destinationRange(src []byte, start, limit int, dest []byte) (int, int, bool) {
	for start < limit && (src[start] == ' ' || src[start] == '\t' || src[start] == '\n' || src[start] == '\r') {
		start++
	}
	if start < limit && src[start] == '<' {
		start++
		if !bytes.HasPrefix(src[start:limit], dest) || start+len(dest) >= limit || src[start+len(dest)] != '>' {
			return 0, 0, false
		}
		return start, start + len(dest), true
	}
	if !bytes.HasPrefix(src[start:limit], dest) {
		return 0, 0, false
	}
	end := start + len(dest)
	if end < limit && src[end] != ')' && src[end] != ' ' && src[end] != '\t' && src[end] != '\n' && src[end] != '\r' {
		return 0, 0, false
	}
	return start, end, true
}

func appendMarkdown(body, markdown string) string {
	if body == "" {
		return markdown
	}
	if strings.HasSuffix(body, "\n\n") {
		return body + markdown
	}
	if strings.HasSuffix(body, "\n") {
		return body + "\n" + markdown
	}
	return body + "\n\n" + markdown
}

func codeSpanRanges(line []byte) [][2]int {
	var ranges [][2]int
	for i := 0; i < len(line); {
		if line[i] != '`' || escapedAt(line, i) {
			i++
			continue
		}
		start := i
		for i < len(line) && line[i] == '`' {
			i++
		}
		ticks := i - start
		for j := i; j < len(line); j++ {
			if line[j] != '`' || escapedAt(line, j) {
				continue
			}
			end := j
			for end < len(line) && line[end] == '`' {
				end++
			}
			if end-j == ticks {
				ranges = append(ranges, [2]int{start, end})
				i = end
				break
			}
			j = end - 1
		}
	}
	return ranges
}

func escapedAt(line []byte, pos int) bool {
	backslashes := 0
	for pos > backslashes && line[pos-backslashes-1] == '\\' {
		backslashes++
	}
	return backslashes%2 == 1
}

func inAnyRange(pos int, ranges [][2]int) bool {
	for _, r := range ranges {
		if pos >= r[0] && pos < r[1] {
			return true
		}
	}
	return false
}
