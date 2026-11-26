// Package yamledit reads DSH profile patches (a YAML list of rows) and changes
// them in place: only the lines of the keys and rows being changed are
// rewritten, so comments, blank lines, `!!js` expressions, quoting, folded
// strings and flow lists everywhere else stay byte for byte.
//
// Positions come from the go.yaml.in/yaml/v3 node tree; after every edit the
// text is parsed again, so each edit sees exact positions.
package yamledit

import (
	"errors"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// SyntaxError is a YAML parse error.
type SyntaxError struct{ Msg string }

func (e *SyntaxError) Error() string { return e.Msg }

// ErrNotList: the document is not a YAML list.
var ErrNotList = errors.New("a profile patch must be a YAML list")

// ErrFlow: a nested entry written in flow style cannot be edited in place.
var ErrFlow = errors.New("this entry is written in flow style ({…}); edit it by hand or write it in block style")

// Editor holds a document's lines and its parsed node tree.
type Editor struct {
	lines []string
	final bool // the text ends with a newline
	crlf  bool
	seq   *yaml.Node // the root list, nil for an empty document
}

// New parses a profile patch. An empty or comment-only text is an empty list;
// a list written in flow style is rewritten in block style.
func New(text string) (*Editor, error) {
	e := &Editor{crlf: strings.Contains(text, "\r\n")}
	if e.crlf {
		text = strings.ReplaceAll(text, "\r\n", "\n")
	}
	e.setText(text)
	if err := e.parse(); err != nil {
		return nil, err
	}
	if e.seq != nil && e.seq.Style&yaml.FlowStyle != 0 {
		rows, _ := ToValue(e.seq).([]any)
		var kept []string
		for _, l := range e.lines {
			if t := strings.TrimSpace(l); t == "" || strings.HasPrefix(t, "#") {
				kept = append(kept, l)
			}
		}
		e.lines = append(kept, renderSeqBody(rows, 0)...)
		e.final = true
		if err := e.parse(); err != nil {
			return nil, err
		}
	}
	return e, nil
}

func (e *Editor) setText(text string) {
	e.final = strings.HasSuffix(text, "\n")
	text = strings.TrimSuffix(text, "\n")
	if text == "" && !e.final {
		e.lines = nil
		return
	}
	e.lines = strings.Split(text, "\n")
}

// Text is the current document.
func (e *Editor) Text() string {
	if len(e.lines) == 0 {
		return ""
	}
	text := strings.Join(e.lines, "\n")
	if e.final {
		text += "\n"
	}
	if e.crlf {
		text = strings.ReplaceAll(text, "\n", "\r\n")
	}
	return text
}

func (e *Editor) parse() error {
	src := strings.Join(e.lines, "\n")
	e.seq = nil
	if strings.TrimSpace(src) == "" {
		return nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src+"\n"), &doc); err != nil {
		return &SyntaxError{Msg: strings.TrimPrefix(err.Error(), "yaml: ")}
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return nil // comments only
	}
	root := doc.Content[0]
	if root.Kind != yaml.SequenceNode {
		return ErrNotList
	}
	e.seq = root
	return nil
}

// Rows are the top-level items.
func (e *Editor) Rows() []*yaml.Node {
	if e.seq == nil {
		return nil
	}
	return e.seq.Content
}

// Value is the whole document as plain values ([]any).
func (e *Editor) Value() []any {
	out := []any{}
	for _, row := range e.Rows() {
		out = append(out, ToValue(row))
	}
	return out
}

// ---- node helpers

// MapValue returns the value node of a key in a mapping node.
func MapValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Kind == yaml.ScalarNode && n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func keyIndex(n *yaml.Node, key string) int {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Kind == yaml.ScalarNode && n.Content[i].Value == key {
			return i / 2
		}
	}
	return -1
}

// Walk visits every node below n in document order (n itself included).
func Walk(n *yaml.Node, visit func(*yaml.Node)) {
	if n == nil {
		return
	}
	visit(n)
	for _, c := range n.Content {
		Walk(c, visit)
	}
}

// ---- line geometry (lines are 0-based, node positions 1-based)

func indentOf(line string) int { return len(line) - len(strings.TrimLeft(line, " ")) }

func isBlankOrComment(line string) bool {
	t := strings.TrimSpace(line)
	return t == "" || strings.HasPrefix(t, "#")
}

// byteOffset converts a 0-based character column into a byte offset of the line.
func byteOffset(line string, col int) int {
	off := 0
	for i := 0; i < col && off < len(line); i++ {
		_, size := utf8.DecodeRuneInString(line[off:])
		off += size
	}
	return off
}

// endLine is the last line of a node's text.
func (e *Editor) endLine(n *yaml.Node) int {
	if n == nil || n.Line == 0 {
		return -1
	}
	switch n.Kind {
	case yaml.ScalarNode:
		return e.scalarEnd(n)
	case yaml.MappingNode, yaml.SequenceNode:
		if n.Style&yaml.FlowStyle != 0 {
			return e.flowEnd(n)
		}
		last := n.Line - 1
		for i, c := range n.Content {
			var end int
			if n.Kind == yaml.MappingNode && i%2 == 0 && c.Kind == yaml.ScalarNode {
				end = c.Line - 1 // keys are one line
			} else {
				end = e.endLine(c)
			}
			last = max(last, end)
		}
		return last
	default:
		return n.Line - 1
	}
}

func (e *Editor) scalarEnd(n *yaml.Node) int {
	start := n.Line - 1
	if start >= len(e.lines) {
		return start
	}
	if n.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle) != 0 {
		line, _ := e.quotedEnd(n)
		return line
	}
	base := indentOf(e.lines[start])
	block := n.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0
	ownLine := !block && n.Column-1 <= base
	end := start
	for i := start + 1; i < len(e.lines); i++ {
		l := e.lines[i]
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		if !block && strings.HasPrefix(t, "#") {
			break
		}
		ind := indentOf(l)
		if ind > base || (ownLine && ind >= base) {
			end = i
			continue
		}
		break
	}
	return end
}

// quotedEnd finds the closing quote of a quoted scalar: line and byte offset after it.
func (e *Editor) quotedEnd(n *yaml.Node) (int, int) {
	line := n.Line - 1
	off := byteOffset(e.lines[line], n.Column-1)
	// skip a tag or an anchor before the quote
	for off < len(e.lines[line]) && e.lines[line][off] != '"' && e.lines[line][off] != '\'' {
		off++
	}
	if off >= len(e.lines[line]) {
		return line, len(e.lines[line])
	}
	q := e.lines[line][off]
	off++
	for line < len(e.lines) {
		l := e.lines[line]
		for off < len(l) {
			c := l[off]
			switch {
			case q == '"' && c == '\\':
				off += 2
				continue
			case c == q && q == '\'' && off+1 < len(l) && l[off+1] == '\'':
				off += 2
				continue
			case c == q:
				return line, off + 1
			}
			off++
		}
		line++
		off = 0
	}
	return len(e.lines) - 1, len(e.lines[len(e.lines)-1])
}

// flowEnd finds the line of the bracket that closes a flow collection.
func (e *Editor) flowEnd(n *yaml.Node) int {
	line := n.Line - 1
	off := byteOffset(e.lines[line], n.Column-1)
	depth := 0
	var quoteChar byte
	for line < len(e.lines) {
		l := e.lines[line]
		for off < len(l) {
			c := l[off]
			switch {
			case quoteChar != 0:
				if quoteChar == '"' && c == '\\' {
					off++
				} else if c == quoteChar {
					quoteChar = 0
				}
			case c == '"' || c == '\'':
				quoteChar = c
			case c == '#' && (off == 0 || l[off-1] == ' '):
				off = len(l)
				continue
			case c == '[' || c == '{':
				depth++
			case c == ']' || c == '}':
				depth--
				if depth == 0 {
					return line
				}
			}
			off++
		}
		line++
		off = 0
	}
	return len(e.lines) - 1
}

// rowStart is the line of the `-` that opens a top-level row.
func (e *Editor) rowStart(i int) int {
	dash := e.seq.Column - 1
	l := e.seq.Content[i].Line - 1
	for l > 0 {
		line := e.lines[l]
		if indentOf(line) == dash && strings.HasPrefix(strings.TrimSpace(line), "-") {
			break
		}
		l--
	}
	return l
}

// ---- text edits

// splice replaces lines [start, end] with repl (end = start-1 inserts before start) and parses again.
func (e *Editor) splice(start, end int, repl []string) error {
	next := append([]string{}, e.lines[:start]...)
	next = append(next, repl...)
	if end+1 < len(e.lines) {
		next = append(next, e.lines[end+1:]...)
	}
	e.lines = next
	if len(e.lines) > 0 {
		e.final = true
	}
	return e.parse()
}

// Loc finds a mapping node in the current tree (positions change after each edit).
type Loc func(e *Editor) *yaml.Node

// RowLoc is the top-level row i.
func RowLoc(i int) Loc {
	return func(e *Editor) *yaml.Node {
		rows := e.Rows()
		if i < 0 || i >= len(rows) {
			return nil
		}
		return rows[i]
	}
}

// KeyLoc is the value of a key in the mapping that parent finds.
func KeyLoc(parent Loc, key string) Loc {
	return func(e *Editor) *yaml.Node { return MapValue(parent(e), key) }
}

// pairSpan: first and last line of the pair i of a block mapping.
func (e *Editor) pairSpan(m *yaml.Node, i int) (int, int) {
	k, v := m.Content[2*i], m.Content[2*i+1]
	start := k.Line - 1
	return start, max(start, e.endLine(v))
}

// keyPrefix is the text before the key on its line: indentation or `- `.
func (e *Editor) keyPrefix(k *yaml.Node) string {
	line := e.lines[k.Line-1]
	return line[:byteOffset(line, k.Column-1)]
}

// SetKey sets a key of the mapping that loc finds. An equal value is left
// alone; a map over a block map is merged key by key, so its comments stay.
func (e *Editor) SetKey(loc Loc, key string, v any) error {
	m := loc(e)
	if m == nil || m.Kind != yaml.MappingNode {
		return errors.New("yamledit: no mapping to edit")
	}
	if m.Style&yaml.FlowStyle != 0 {
		return e.replaceFlow(m, func(cur *Map) { cur.Set(key, v) })
	}
	i := keyIndex(m, key)
	if i < 0 {
		return e.appendPair(m, key, v)
	}
	k, val := m.Content[2*i], m.Content[2*i+1]
	if Equal(ToValue(val), v) {
		return nil
	}
	if dm, ok := v.(*Map); ok && dm.Len() > 0 && val.Kind == yaml.MappingNode && val.Style&yaml.FlowStyle == 0 && len(val.Content) > 0 {
		return e.SyncMap(KeyLoc(loc, key), dm)
	}
	// a one-line scalar replaced by a scalar: only the value text changes (a trailing comment stays)
	if _, isMap := v.(*Map); !isMap {
		if _, isSeq := v.([]any); !isSeq && val.Kind == yaml.ScalarNode && val.Line == k.Line &&
			val.Style&(yaml.TaggedStyle|yaml.LiteralStyle|yaml.FoldedStyle) == 0 && val.Anchor == "" && e.scalarEnd(val) == val.Line-1 {
			line := e.lines[val.Line-1]
			from := byteOffset(line, val.Column-1)
			to := from + len(val.Value)
			if val.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle) != 0 {
				_, to = e.quotedEnd(val)
			}
			if to <= len(line) && from <= to {
				return e.splice(val.Line-1, val.Line-1, []string{line[:from] + renderScalar(v) + line[to:]})
			}
		}
	}
	start, end := e.pairSpan(m, i)
	return e.splice(start, end, renderPair(e.keyPrefix(k), k.Column-1, key, v))
}

func (e *Editor) appendPair(m *yaml.Node, key string, v any) error {
	if len(m.Content) == 0 {
		return e.replaceFlow(m, func(cur *Map) { cur.Set(key, v) })
	}
	indent := m.Content[0].Column - 1
	end := e.endLine(m)
	return e.splice(end+1, end, renderPair(spaces(indent), indent, key, v))
}

// DeleteKey removes a key of the mapping that loc finds (no-op when absent).
func (e *Editor) DeleteKey(loc Loc, key string) error {
	m := loc(e)
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	i := keyIndex(m, key)
	if i < 0 {
		return nil
	}
	if m.Style&yaml.FlowStyle != 0 {
		return e.replaceFlow(m, func(cur *Map) { cur.Delete(key) })
	}
	k := m.Content[2*i]
	start, end := e.pairSpan(m, i)
	prefix := e.keyPrefix(k)
	if strings.TrimSpace(prefix) == "" {
		return e.splice(start, end, nil)
	}
	// the key shares its line with `- `: the next key moves onto that line
	if len(m.Content) == 2 {
		return e.splice(start, end, []string{prefix + "{}"})
	}
	next := m.Content[2*(i+1)]
	nl := e.lines[next.Line-1]
	return e.splice(start, next.Line-1, []string{prefix + nl[byteOffset(nl, next.Column-1):]})
}

// SyncMap makes the mapping that loc finds equal to desired, key by key:
// unchanged keys keep their lines, new keys go to the end.
func (e *Editor) SyncMap(loc Loc, desired *Map) error {
	m := loc(e)
	if m == nil || m.Kind != yaml.MappingNode {
		return errors.New("yamledit: no mapping to edit")
	}
	if m.Style&yaml.FlowStyle != 0 || len(m.Content) == 0 {
		return e.replaceFlow(m, func(cur *Map) {
			*cur = *desired.Clone()
		})
	}
	current, _ := ToValue(m).(*Map)
	for _, k := range current.Keys() {
		if !desired.Has(k) {
			if err := e.DeleteKey(loc, k); err != nil {
				return err
			}
		}
	}
	for _, k := range desired.Keys() {
		if err := e.SetKey(loc, k, desired.Value(k)); err != nil {
			return err
		}
	}
	return nil
}

// replaceFlow rewrites a flow (or empty) mapping in block style after a change.
func (e *Editor) replaceFlow(m *yaml.Node, change func(cur *Map)) error {
	cur, _ := ToValue(m).(*Map)
	if cur == nil {
		cur = NewMap()
	} else {
		cur = cur.Clone()
	}
	change(cur)
	parent, idx := e.parentOf(m)
	switch {
	case parent == nil:
		return ErrFlow
	case parent == e.seq && e.seq.Style&yaml.FlowStyle == 0:
		start, end := e.rowStart(idx), e.endLine(m)
		return e.splice(start, end, renderSeqBody([]any{cur}, e.seq.Column-1))
	case parent.Kind == yaml.MappingNode && parent.Style&yaml.FlowStyle == 0 && idx%2 == 1:
		k := parent.Content[idx-1]
		start, end := e.pairSpan(parent, idx/2)
		return e.splice(start, end, renderPair(e.keyPrefix(k), k.Column-1, k.Value, cur))
	default:
		return ErrFlow
	}
}

func (e *Editor) parentOf(target *yaml.Node) (*yaml.Node, int) {
	var found *yaml.Node
	idx := -1
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		for i, c := range n.Content {
			if found != nil {
				return
			}
			if c == target {
				found, idx = n, i
				return
			}
			walk(c)
		}
	}
	if e.seq != nil {
		walk(e.seq)
	}
	return found, idx
}

// AppendRow adds a top-level row at the end, with a comment line above it.
func (e *Editor) AppendRow(comment string, row *Map) error {
	indent := 0
	if e.seq != nil {
		indent = e.seq.Column - 1
	}
	var lines []string
	if comment != "" {
		lines = append(lines, spaces(indent)+"# "+comment)
	}
	lines = append(lines, renderSeqBody([]any{row}, indent)...)
	// drop trailing empty lines of the old text before appending
	end := len(e.lines)
	for end > 0 && strings.TrimSpace(e.lines[end-1]) == "" {
		end--
	}
	e.lines = append(e.lines[:end], lines...)
	e.final = true
	return e.parse()
}

// DeleteRow removes a top-level row and the `# <mark>…` comment lines right above it.
func (e *Editor) DeleteRow(i int, mark string) error {
	if e.seq == nil || i < 0 || i >= len(e.seq.Content) {
		return nil
	}
	start, end := e.rowStart(i), e.endLine(e.seq.Content[i])
	from := start
	for l := start - 1; l >= 0 && isBlankOrComment(e.lines[l]) && strings.TrimSpace(e.lines[l]) != ""; l-- {
		if mark != "" && strings.HasPrefix(strings.TrimSpace(e.lines[l]), "# "+mark) {
			from = l
		}
	}
	return e.splice(from, end, nil)
}
