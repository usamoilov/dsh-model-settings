package yamledit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Map is a YAML mapping that keeps its key order.
type Map struct {
	keys []string
	vals map[string]any
}

// JS is a `!!js` expression: DSH evaluates it, this program never does.
type JS string

// NewMap builds a map from key, value pairs.
func NewMap(kv ...any) *Map {
	m := &Map{vals: map[string]any{}}
	for i := 0; i+1 < len(kv); i += 2 {
		m.Set(kv[i].(string), kv[i+1])
	}
	return m
}

// Keys in document order.
func (m *Map) Keys() []string {
	if m == nil {
		return nil
	}
	return append([]string(nil), m.keys...)
}

// Len is the number of keys.
func (m *Map) Len() int {
	if m == nil {
		return 0
	}
	return len(m.keys)
}

// Has reports whether the key is present.
func (m *Map) Has(key string) bool {
	if m == nil {
		return false
	}
	_, ok := m.vals[key]
	return ok
}

// Get returns a value and whether the key is present.
func (m *Map) Get(key string) (any, bool) {
	if m == nil {
		return nil, false
	}
	v, ok := m.vals[key]
	return v, ok
}

// Value returns a value, nil when absent.
func (m *Map) Value(key string) any {
	v, _ := m.Get(key)
	return v
}

// String returns a string value ("" when absent or not a string).
func (m *Map) String(key string) string {
	s, _ := m.Value(key).(string)
	return s
}

// Map returns a nested map (nil when absent or not a map).
func (m *Map) Map(key string) *Map {
	v, _ := m.Value(key).(*Map)
	return v
}

// Set adds a key at the end, or replaces the value of an existing key in place.
func (m *Map) Set(key string, v any) {
	if _, ok := m.vals[key]; !ok {
		m.keys = append(m.keys, key)
	}
	m.vals[key] = v
}

// Delete removes a key.
func (m *Map) Delete(key string) {
	if _, ok := m.vals[key]; !ok {
		return
	}
	delete(m.vals, key)
	for i, k := range m.keys {
		if k == key {
			m.keys = append(m.keys[:i], m.keys[i+1:]...)
			break
		}
	}
}

// Clone is a shallow copy (nil gives an empty map).
func (m *Map) Clone() *Map {
	out := NewMap()
	for _, k := range m.Keys() {
		out.Set(k, m.vals[k])
	}
	return out
}

// MarshalJSON keeps the key order.
func (m *Map) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range m.Keys() {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, _ := json.Marshal(k)
		buf.Write(key)
		buf.WriteByte(':')
		val, err := json.Marshal(m.vals[k])
		if err != nil {
			return nil, err
		}
		buf.Write(val)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// FromJSON turns decoded JSON (map[string]any, []any, float64…) into YAML values.
// Object keys are sorted, since Go maps have no order.
func FromJSON(v any) any {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		m := NewMap()
		for _, k := range keys {
			m.Set(k, FromJSON(x[k]))
		}
		return m
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = FromJSON(item)
		}
		return out
	default:
		return v
	}
}

// ToValue converts a node into plain values: *Map, []any, string, bool,
// int64, float64, JS or nil.
func ToValue(n *yaml.Node) any {
	if n == nil {
		return nil
	}
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil
		}
		return ToValue(n.Content[0])
	case yaml.AliasNode:
		return ToValue(n.Alias)
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			out = append(out, ToValue(c))
		}
		return out
	case yaml.MappingNode:
		m := NewMap()
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			key := k.Value
			if k.Kind != yaml.ScalarNode {
				key = fmt.Sprint(ToValue(k))
			}
			m.Set(key, ToValue(n.Content[i+1]))
		}
		return m
	case yaml.ScalarNode:
		return scalarValue(n)
	}
	return nil
}

func scalarValue(n *yaml.Node) any {
	switch n.ShortTag() {
	case "!!null":
		return nil
	case "!!bool":
		if b, err := strconv.ParseBool(strings.ToLower(n.Value)); err == nil {
			return b
		}
		return n.Value
	case "!!int":
		if i, err := strconv.ParseInt(n.Value, 0, 64); err == nil {
			return i
		}
		if f, err := strconv.ParseFloat(n.Value, 64); err == nil {
			return f
		}
		return n.Value
	case "!!float":
		switch strings.ToLower(n.Value) {
		case ".inf", "+.inf":
			return math.Inf(1)
		case "-.inf":
			return math.Inf(-1)
		case ".nan":
			return math.NaN()
		}
		if f, err := strconv.ParseFloat(n.Value, 64); err == nil {
			return f
		}
		return n.Value
	case "!!js":
		return JS(n.Value)
	default:
		return n.Value
	}
}

// Equal compares two values by meaning; map key order counts, as it does
// for a JSON comparison.
func Equal(a, b any) bool {
	switch x := a.(type) {
	case *Map:
		y, ok := b.(*Map)
		if !ok || x.Len() != y.Len() {
			return false
		}
		for i, k := range x.keys {
			if y.keys[i] != k || !Equal(x.vals[k], y.vals[k]) {
				return false
			}
		}
		return true
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !Equal(x[i], y[i]) {
				return false
			}
		}
		return true
	case int64:
		switch y := b.(type) {
		case int64:
			return x == y
		case float64:
			return float64(x) == y
		}
		return false
	case float64:
		switch y := b.(type) {
		case int64:
			return x == float64(y)
		case float64:
			return x == y
		}
		return false
	case nil:
		return b == nil
	default:
		return a == b
	}
}

// Truthy is JavaScript truthiness for plain values.
func Truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case JS:
		return x != ""
	case int64:
		return x != 0
	case float64:
		return x != 0 && !math.IsNaN(x)
	default:
		return true
	}
}
