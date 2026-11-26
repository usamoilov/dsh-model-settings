package yamledit

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Rendering of the values this program writes: block mappings and sequences of
// strings, booleans, numbers and small maps, two spaces per level (DSH's style).

func spaces(n int) string { return strings.Repeat(" ", n) }

// renderPair renders `key: value`; prefix is the text before the key on its
// first line (indentation, or `- ` of a sequence item), indent the key's column.
func renderPair(prefix string, indent int, key string, v any) []string {
	k := renderScalar(key)
	switch x := v.(type) {
	case *Map:
		if x.Len() == 0 {
			return []string{prefix + k + ": {}"}
		}
		return append([]string{prefix + k + ":"}, renderMapBody(x, indent+2)...)
	case []any:
		if len(x) == 0 {
			return []string{prefix + k + ": []"}
		}
		return append([]string{prefix + k + ":"}, renderSeqBody(x, indent+2)...)
	default:
		return []string{prefix + k + ": " + renderScalar(v)}
	}
}

func renderMapBody(m *Map, indent int) []string {
	var out []string
	for _, k := range m.keys {
		out = append(out, renderPair(spaces(indent), indent, k, m.vals[k])...)
	}
	return out
}

func renderSeqBody(s []any, indent int) []string {
	var out []string
	pad := spaces(indent)
	for _, item := range s {
		switch x := item.(type) {
		case *Map:
			if x.Len() == 0 {
				out = append(out, pad+"- {}")
				continue
			}
			for i, k := range x.keys {
				prefix := spaces(indent + 2)
				if i == 0 {
					prefix = pad + "- "
				}
				out = append(out, renderPair(prefix, indent+2, k, x.vals[k])...)
			}
		case []any:
			out = append(out, pad+"- "+renderFlow(x))
		default:
			out = append(out, pad+"- "+renderScalar(item))
		}
	}
	return out
}

// renderFlow is used only for a sequence nested directly in a sequence.
func renderFlow(v any) string {
	switch x := v.(type) {
	case *Map:
		parts := make([]string, 0, x.Len())
		for _, k := range x.keys {
			parts = append(parts, renderScalar(k)+": "+renderFlow(x.vals[k]))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case []any:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			parts = append(parts, renderFlow(item))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		s := renderScalar(v)
		if strings.ContainsAny(s, ",[]{}") && !strings.HasPrefix(s, `"`) {
			return quote(fmt.Sprint(v))
		}
		return s
	}
}

func renderScalar(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(x)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		switch {
		case math.IsInf(x, 1):
			return ".inf"
		case math.IsInf(x, -1):
			return "-.inf"
		case math.IsNaN(x):
			return ".nan"
		case x == math.Trunc(x) && math.Abs(x) < 1e15:
			return strconv.FormatFloat(x, 'f', -1, 64)
		}
		return strconv.FormatFloat(x, 'g', -1, 64)
	case JS:
		if plainOK(string(x)) {
			return "!!js " + string(x)
		}
		return "!!js " + quote(string(x))
	case string:
		if plainOK(x) {
			return x
		}
		return quote(x)
	default:
		return quote(fmt.Sprint(v))
	}
}

var (
	reInt     = regexp.MustCompile(`^[-+]?(0|[1-9][0-9_]*|0[0-7_]+|0x[0-9a-fA-F_]+|0o[0-7_]+|0b[01_]+)$`)
	reFloat   = regexp.MustCompile(`^[-+]?(\.[0-9]+|[0-9][0-9_]*(\.[0-9_]*)?)([eE][-+]?[0-9]+)?$`)
	reSpecial = regexp.MustCompile(`^([-+]?\.(inf|Inf|INF)|\.(nan|NaN|NAN))$`)
	reBoolNil = regexp.MustCompile(`^(true|True|TRUE|false|False|FALSE|null|Null|NULL|~)$`)
)

// plainOK: the string can be written without quotes and reads back as the same string.
func plainOK(s string) bool {
	if s == "" || strings.TrimSpace(s) != s {
		return false
	}
	if reBoolNil.MatchString(s) || reInt.MatchString(s) || reFloat.MatchString(s) || reSpecial.MatchString(s) {
		return false
	}
	if strings.ContainsAny(s[:1], "-?:,[]{}#&*!|>'\"%@`") {
		return false
	}
	if strings.Contains(s, ": ") || strings.Contains(s, " #") || strings.HasSuffix(s, ":") || strings.ContainsAny(s, ",[]{}") {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f || r == 0xfeff {
			return false
		}
	}
	return true
}

// quote writes a YAML double-quoted scalar.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\x%02x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
