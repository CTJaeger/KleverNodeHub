package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Config diff kinds reported to the dashboard.
const (
	// diffKindChanged: the value differs between the local file and the new download.
	diffKindChanged = "changed"
	// diffKindLocalOnly: an entry exists only in the local file (user addition or
	// removed upstream). Keeping it re-inserts the entry into the new file.
	diffKindLocalOnly = "local_only"
	// diffKindFile: formats we don't parse entry by entry (JSON); the whole file
	// is offered as one choice.
	diffKindFile = "file"

	wholeFileKey = "(entire file)"
	maxMineLines = 20
)

// configIgnoreKeys are values the config upgrade already carries over on its
// own (see readUserValues), so they never show up as a choice.
var configIgnoreKeys = map[string]bool{"NodeDisplayName": true}

// ConfigDiffItem is one difference between the node's current config and the
// freshly downloaded one that the operator can decide on.
type ConfigDiffItem struct {
	ID   string `json:"id"` // "<file>::<key>", stable between preview and upgrade
	File string `json:"file"`
	Key  string `json:"key"`
	Kind string `json:"kind"`
	Mine string `json:"mine"`
	New  string `json:"new"`
}

// ConfigPreview is the result of comparing current configs with the new download.
type ConfigPreview struct {
	Items          []ConfigDiffItem `json:"items"`
	AddedByFile    map[string]int   `json:"added_by_file,omitempty"`
	DownloadSource string           `json:"download_source"`
}

// cfgNode is one entry of a parsed YAML/TOML config (a key, a list item or a
// TOML section) with the line range it occupies, so it can be patched in place
// without re-serialising — and thereby reformatting — the whole file.
type cfgNode struct {
	key      string
	value    string
	path     string
	line     int // first line index
	end      int // exclusive end line index of the whole subtree
	col      int // column of the key (of the dash for list items)
	indent   int // indentation of the first line
	isItem   bool
	section  bool // TOML [section]
	dashLine bool // YAML key that shares its line with the list dash
	parent   *cfgNode
	children []*cfgNode
}

func (n *cfgNode) isLeaf() bool { return len(n.children) == 0 && !n.section }

type diffEntry struct {
	item   ConfigDiffItem
	oldN   *cfgNode
	newN   *cfgNode // changed: the node to replace
	parent *cfgNode // local_only: node of the new tree to insert into
}

func configFormat(file string) string {
	switch strings.ToLower(filepath.Ext(file)) {
	case ".yaml", ".yml":
		return "yaml"
	case ".toml":
		return "toml"
	default:
		return "file"
	}
}

// diffConfigFile compares the local file with the new download and returns the
// decidable differences plus the number of entries that are new in the download
// (those are simply taken).
func diffConfigFile(file, oldText, newText string) ([]diffEntry, int) {
	switch configFormat(file) {
	case "yaml":
		return diffTrees(file, parseYAMLConfig(oldText), parseYAMLConfig(newText), strings.Split(oldText, "\n"))
	case "toml":
		return diffTrees(file, parseTOMLConfig(oldText), parseTOMLConfig(newText), strings.Split(oldText, "\n"))
	default:
		if compactJSON(oldText) == compactJSON(newText) {
			return nil, 0
		}
		return []diffEntry{{item: ConfigDiffItem{
			ID: file + "::" + wholeFileKey, File: file, Key: wholeFileKey, Kind: diffKindFile,
			Mine: describeFile(oldText), New: describeFile(newText),
		}}}, 0
	}
}

func diffTrees(file string, oldRoot, newRoot *cfgNode, oldLines []string) ([]diffEntry, int) {
	oldAll, oldBy := flattenConfig(oldRoot)
	newAll, newBy := flattenConfig(newRoot)

	var entries []diffEntry
	added := 0

	for _, n := range newAll {
		if !n.isLeaf() || configIgnoreKeys[n.key] {
			continue
		}
		o, ok := oldBy[n.path]
		if !ok {
			added++
			continue
		}
		// Different shapes (scalar vs. mapping, or a key moved onto the dash
		// line) can't be swapped line-for-line; the new definition wins.
		if !o.isLeaf() || o.dashLine != n.dashLine {
			continue
		}
		if normalizeScalar(o.value) == normalizeScalar(n.value) {
			continue
		}
		key := displayKey(n.path)
		entries = append(entries, diffEntry{
			item: ConfigDiffItem{ID: file + "::" + key, File: file, Key: key, Kind: diffKindChanged, Mine: o.value, New: n.value},
			oldN: o, newN: n,
		})
	}

	for _, o := range oldAll {
		if _, ok := newBy[o.path]; ok || configIgnoreKeys[o.key] {
			continue
		}
		// Only the topmost missing entry is offered; its subtree comes with it.
		parent := newRoot
		if o.parent != oldRoot {
			p, ok := newBy[o.parent.path]
			if !ok || p.isLeaf() {
				continue
			}
			parent = p
		}
		// A key on the dash line can't be re-inserted without its list item.
		if !o.isItem && o.dashLine {
			continue
		}
		key := displayKey(o.path)
		entries = append(entries, diffEntry{
			item: ConfigDiffItem{ID: file + "::" + key, File: file, Key: key, Kind: diffKindLocalOnly, Mine: renderSubtree(oldLines, o)},
			oldN: o, parent: parent,
		})
	}
	return entries, added
}

type lineOp struct {
	at, del int
	ins     []string
	insert  bool
	seq     int
}

// applyConfigKeeps returns the new config text with the operator's chosen local
// values written back. Entries whose id is not in keep stay as downloaded. The
// returned lists name the kept ids and the requested ids that no longer match
// anything (e.g. upstream changed between preview and upgrade).
func applyConfigKeeps(file, oldText, newText string, keep map[string]bool) (patched string, applied, missed []string) {
	prefix := file + "::"
	requested := map[string]bool{}
	for id := range keep {
		if keep[id] && strings.HasPrefix(id, prefix) {
			requested[id] = true
		}
	}
	if len(requested) == 0 {
		return newText, nil, nil
	}

	entries, _ := diffConfigFile(file, oldText, newText)
	if configFormat(file) == "file" {
		if requested[file+"::"+wholeFileKey] && len(entries) > 0 {
			return oldText, []string{file + "::" + wholeFileKey}, nil
		}
		return newText, nil, sortedKeys(requested)
	}

	lines := strings.Split(newText, "\n")
	oldLines := strings.Split(oldText, "\n")
	var ops []lineOp
	for _, e := range entries {
		if !requested[e.item.ID] {
			continue
		}
		delete(requested, e.item.ID)
		applied = append(applied, e.item.ID)
		src := oldLines[e.oldN.line:e.oldN.end]

		if e.item.Kind == diffKindChanged {
			delta := e.newN.indent - e.oldN.indent
			ops = append(ops, lineOp{at: e.newN.line, del: e.newN.end - e.newN.line, ins: reindentLines(src, delta)})
			continue
		}

		delta := insertionCol(e.parent) - e.oldN.col
		ins := reindentLines(src, delta)
		at := e.parent.end
		if configFormat(file) == "toml" && e.parent.col < 0 {
			if e.oldN.section {
				ins = append([]string{""}, ins...)
			} else if first := firstSectionLine(e.parent); first >= 0 {
				// Root-level TOML keys must stay above the first [section].
				at = first
			}
		}
		ops = append(ops, lineOp{at: at, ins: ins, insert: true, seq: len(ops)})
	}

	// Apply bottom-up so earlier line indexes stay valid; at the same index a
	// replacement goes first so an insertion ends up in front of it, and
	// insertions at the same index keep their original order.
	sort.SliceStable(ops, func(i, j int) bool {
		if ops[i].at != ops[j].at {
			return ops[i].at > ops[j].at
		}
		if ops[i].insert != ops[j].insert {
			return !ops[i].insert
		}
		return ops[i].seq > ops[j].seq
	})
	for _, op := range ops {
		if op.at > len(lines) {
			op.at = len(lines)
		}
		tail := append([]string(nil), lines[op.at+op.del:]...)
		lines = append(append(lines[:op.at], op.ins...), tail...)
	}
	return strings.Join(lines, "\n"), applied, sortedKeys(requested)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func firstSectionLine(root *cfgNode) int {
	for _, c := range root.children {
		if c.section {
			return c.line
		}
	}
	return -1
}

// insertionCol is the column new children of parent should start at.
func insertionCol(parent *cfgNode) int {
	if len(parent.children) > 0 {
		return parent.children[0].col
	}
	if parent.col < 0 {
		return 0
	}
	return parent.col + 2
}

func reindentLines(lines []string, delta int) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		switch {
		case delta == 0 || strings.TrimSpace(l) == "":
			out[i] = l
		case delta > 0:
			out[i] = strings.Repeat(" ", delta) + l
		default:
			trim := -delta
			for trim > 0 && len(l) > 0 && l[0] == ' ' {
				l = l[1:]
				trim--
			}
			out[i] = l
		}
	}
	return out
}

// renderSubtree returns the node's lines with the common indentation removed,
// shortened to maxMineLines for display.
func renderSubtree(lines []string, n *cfgNode) string {
	if n.line < 0 || n.end > len(lines) {
		return ""
	}
	src := lines[n.line:n.end]
	minIndent := -1
	for _, l := range src {
		if strings.TrimSpace(l) == "" {
			continue
		}
		ind := len(l) - len(strings.TrimLeft(l, " "))
		if minIndent < 0 || ind < minIndent {
			minIndent = ind
		}
	}
	if minIndent < 0 {
		minIndent = 0
	}
	var out []string
	for i, l := range src {
		if i == maxMineLines {
			out = append(out, "…")
			break
		}
		l = strings.TrimRight(l, "\r")
		if len(l) >= minIndent {
			l = l[minIndent:]
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

func flattenConfig(root *cfgNode) ([]*cfgNode, map[string]*cfgNode) {
	var all []*cfgNode
	by := map[string]*cfgNode{}
	var walk func(n *cfgNode)
	walk = func(n *cfgNode) {
		for _, c := range n.children {
			all = append(all, c)
			if _, dup := by[c.path]; !dup {
				by[c.path] = c
			}
			walk(c)
		}
	}
	walk(root)
	return all, by
}

func displayKey(path string) string { return strings.TrimPrefix(path, "/") }

func normalizeScalar(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
		v = v[1 : len(v)-1]
	}
	return v
}

func compactJSON(s string) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(s)); err != nil {
		return strings.TrimSpace(s)
	}
	return buf.String()
}

func describeFile(s string) string {
	return fmt.Sprintf("%d lines, %d bytes", strings.Count(s, "\n")+1, len(s))
}

// stripComment removes a trailing "# comment" that is not inside quotes.
func stripComment(s string) string {
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == '\\' && quote == '"' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			// A quote only opens a string at the start of a value or after a
			// delimiter; an apostrophe inside a plain word is literal.
			if i == 0 || strings.IndexByte(" \t[{,:=", s[i-1]) >= 0 {
				quote = c
			}
		case c == '#' && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t'):
			return strings.TrimSpace(s[:i])
		}
	}
	return strings.TrimSpace(s)
}

// bracketBalance counts unclosed [ and { outside quotes.
func bracketBalance(s string) int {
	var quote byte
	bal := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == '\\' && quote == '"' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '[' || c == '{':
			bal++
		case c == ']' || c == '}':
			bal--
		}
	}
	return bal
}

func attachNode(parent, n *cfgNode) {
	n.parent = parent
	parent.children = append(parent.children, n)
}

func finalizeEnds(n *cfgNode) {
	for _, c := range n.children {
		finalizeEnds(c)
		if c.end > n.end {
			n.end = c.end
		}
	}
}

// splitYAMLKey splits "key: value" / "key:" lines. Scalars such as URLs
// ("http://x") and flow collections are not keys.
func splitYAMLKey(content string) (key, val string, ok bool) {
	if content == "" || content[0] == '[' || content[0] == '{' {
		return "", "", false
	}
	if content[0] == '"' || content[0] == '\'' {
		end := strings.IndexByte(content[1:], content[0])
		if end < 0 {
			return "", "", false
		}
		end++
		rest := content[end+1:]
		switch {
		case rest == ":":
			return content[1:end], "", true
		case strings.HasPrefix(rest, ": "):
			return content[1:end], strings.TrimSpace(rest[2:]), true
		}
		return "", "", false
	}
	if i := strings.Index(content, ": "); i >= 0 {
		return content[:i], strings.TrimSpace(content[i+2:]), true
	}
	if strings.HasSuffix(content, ":") {
		return content[:len(content)-1], "", true
	}
	return "", "", false
}

// parseYAMLConfig parses the block-style YAML subset Klever's configs use
// (nested mappings, lists of mappings, scalars, block scalars, flow
// collections). It only needs structure and line ranges, not typed values.
func parseYAMLConfig(text string) *cfgNode {
	root := &cfgNode{col: -1, line: -1}
	stack := []*cfgNode{root}
	pop := func(drop func(top *cfgNode) bool) {
		for len(stack) > 1 && drop(stack[len(stack)-1]) {
			stack = stack[:len(stack)-1]
		}
	}
	lines := strings.Split(text, "\n")

	for i := 0; i < len(lines); i++ {
		raw := strings.TrimRight(lines[i], "\r")
		content := strings.TrimLeft(raw, " ")
		if content == "" || content[0] == '#' || content == "---" {
			continue
		}
		indent := len(raw) - len(content)
		col := indent
		var item *cfgNode

		if content == "-" || strings.HasPrefix(content, "- ") {
			c := col
			pop(func(top *cfgNode) bool { return top.col > c || (top.col == c && top.isItem) })
			item = &cfgNode{isItem: true, col: col, indent: indent, line: i, end: i + 1}
			attachNode(stack[len(stack)-1], item)
			stack = append(stack, item)
			rest := strings.TrimLeft(content[1:], " ")
			col = indent + len(content) - len(rest)
			content = rest
			if content == "" {
				continue
			}
		} else {
			c := col
			pop(func(top *cfgNode) bool { return top.col >= c })
		}

		key, val, ok := splitYAMLKey(content)
		if !ok {
			if item != nil {
				item.value = stripComment(content)
			}
			continue
		}

		n := &cfgNode{key: key, col: col, indent: indent, line: i, end: i + 1, dashLine: item != nil}
		v := stripComment(val)
		switch {
		case v != "" && (v[0] == '|' || v[0] == '>'):
			parts := []string{v}
			last := i
			for j := i + 1; j < len(lines); j++ {
				l := strings.TrimRight(lines[j], "\r")
				if strings.TrimSpace(l) == "" {
					continue
				}
				if len(l)-len(strings.TrimLeft(l, " ")) <= indent {
					break
				}
				parts = append(parts, strings.TrimSpace(l))
				last = j
			}
			n.value = strings.Join(parts, "\n")
			n.end = last + 1
			i = last
		case v != "" && bracketBalance(v) > 0:
			parts := []string{v}
			bal := bracketBalance(v)
			j := i + 1
			for ; j < len(lines) && bal > 0; j++ {
				s := stripComment(strings.TrimRight(lines[j], "\r"))
				parts = append(parts, s)
				bal += bracketBalance(s)
			}
			n.value = strings.Join(parts, " ")
			n.end = j
			i = j - 1
		default:
			n.value = v
		}
		attachNode(stack[len(stack)-1], n)
		stack = append(stack, n)
	}

	finalizeEnds(root)
	assignYAMLPaths(root)
	return root
}

// itemIdentity names a list item by its identifying key so entries are matched
// by name rather than by position when Klever reorders or inserts items.
func itemIdentity(item *cfgNode) string {
	if item.isLeaf() {
		return ""
	}
	for _, want := range []string{"name", "username", "id", "key"} {
		for _, c := range item.children {
			if c.key == want && c.isLeaf() {
				return normalizeScalar(c.value)
			}
		}
	}
	return ""
}

func assignYAMLPaths(parent *cfgNode) {
	seen := map[string]int{}
	itemIdx := 0
	for _, c := range parent.children {
		var seg string
		if c.isItem {
			id := itemIdentity(c)
			if id == "" {
				id = "#" + strconv.Itoa(itemIdx)
			}
			itemIdx++
			seg = "[" + id + "]"
		} else {
			seg = "/" + c.key
		}
		p := parent.path + seg
		seen[p]++
		if seen[p] > 1 {
			p += "~" + strconv.Itoa(seen[p])
		}
		c.path = p
		assignYAMLPaths(c)
	}
}

// parseTOMLConfig parses [sections] and "key = value" lines (including
// multi-line arrays). Sections are flat children of the root.
func parseTOMLConfig(text string) *cfgNode {
	root := &cfgNode{col: -1, line: -1}
	cur := root
	arrays := map[string]int{}
	lines := strings.Split(text, "\n")

	for i := 0; i < len(lines); i++ {
		raw := strings.TrimRight(lines[i], "\r")
		content := strings.TrimLeft(raw, " \t")
		if content == "" || content[0] == '#' {
			continue
		}
		indent := len(raw) - len(content)
		stripped := stripComment(content)

		if strings.HasPrefix(stripped, "[") && strings.HasSuffix(stripped, "]") && !strings.Contains(stripped, "=") {
			name := strings.TrimSpace(strings.Trim(stripped, "[]"))
			sec := &cfgNode{key: name, section: true, isItem: strings.HasPrefix(stripped, "[["), col: indent, indent: indent, line: i, end: i + 1}
			sec.path = name
			if sec.isItem {
				sec.path = name + "[#" + strconv.Itoa(arrays[name]) + "]"
				arrays[name]++
			}
			attachNode(root, sec)
			cur = sec
			continue
		}

		eq := strings.IndexByte(content, '=')
		if eq < 0 {
			continue
		}
		n := &cfgNode{key: strings.TrimSpace(content[:eq]), col: indent, indent: indent, line: i, end: i + 1}
		v := stripComment(content[eq+1:])
		switch {
		case strings.Count(v, `"""`)%2 == 1 || strings.Count(v, `'''`)%2 == 1:
			delim := `"""`
			if strings.Count(v, `'''`)%2 == 1 {
				delim = `'''`
			}
			parts := []string{v}
			j := i + 1
			for ; j < len(lines); j++ {
				parts = append(parts, strings.TrimRight(lines[j], "\r"))
				if strings.Contains(lines[j], delim) {
					j++
					break
				}
			}
			n.value = strings.Join(parts, "\n")
			n.end = j
			i = j - 1
		case v != "" && bracketBalance(v) > 0:
			parts := []string{v}
			bal := bracketBalance(v)
			j := i + 1
			for ; j < len(lines) && bal > 0; j++ {
				s := stripComment(strings.TrimRight(lines[j], "\r"))
				parts = append(parts, s)
				bal += bracketBalance(s)
			}
			n.value = strings.Join(parts, " ")
			n.end = j
			i = j - 1
		default:
			n.value = v
		}
		n.path = cur.path + "/" + n.key
		attachNode(cur, n)
	}

	finalizeEnds(root)
	return root
}
