// Package jsonedit is an order-preserving JSON editor for the user's
// ~/.claude/settings.json and ~/.codex/hooks.json. The legacy implementation
// was Python, whose dicts preserve insertion order and whose json.dump
// (indent=2, ensure_ascii=False) defined the on-disk byte format; Go maps do
// not preserve order, so a naive port would shuffle a user's settings file on
// every install. This package parses JSON into an ordered tree and re-emits
// it in exactly Python's format (pinned by testdata/golden/settings/).
package jsonedit

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Kind tags a Value.
type Kind int

const (
	Null Kind = iota
	Bool
	Number
	String
	Array
	Object
)

// Member is one object entry, order-significant.
type Member struct {
	Key string
	Val *Value
}

// Value is one JSON value with object key order preserved.
type Value struct {
	Kind Kind
	Bool bool
	Num  json.Number
	Str  string
	Arr  []*Value
	Obj  []Member
}

// --- constructors -----------------------------------------------------------

func NewString(s string) *Value  { return &Value{Kind: String, Str: s} }
func NewObject() *Value          { return &Value{Kind: Object} }
func NewArray() *Value           { return &Value{Kind: Array} }

// Get returns the member value for key, or nil.
func (v *Value) Get(key string) *Value {
	if v == nil || v.Kind != Object {
		return nil
	}
	for _, m := range v.Obj {
		if m.Key == key {
			return m.Val
		}
	}
	return nil
}

// Set replaces or appends a member (append preserves discovery order, like
// Python dict assignment).
func (v *Value) Set(key string, val *Value) {
	for i, m := range v.Obj {
		if m.Key == key {
			v.Obj[i].Val = val
			return
		}
	}
	v.Obj = append(v.Obj, Member{key, val})
}

// --- parse ------------------------------------------------------------------

// Parse decodes JSON preserving object key order and number text.
func Parse(b []byte) (*Value, error) {
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	v, err := parseValue(dec)
	if err != nil {
		return nil, err
	}
	// trailing garbage check (Python json.loads rejects it)
	if dec.More() {
		return nil, errors.New("trailing data after JSON value")
	}
	return v, nil
}

func parseValue(dec *json.Decoder) (*Value, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	return fromToken(dec, tok)
}

func fromToken(dec *json.Decoder, tok json.Token) (*Value, error) {
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			v := &Value{Kind: Object}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, ok := kt.(string)
				if !ok {
					return nil, fmt.Errorf("bad object key %v", kt)
				}
				mv, err := parseValue(dec)
				if err != nil {
					return nil, err
				}
				// Python dict: duplicate key -> last wins, position of FIRST kept
				v.Set(key, mv)
			}
			if _, err := dec.Token(); err != nil { // consume '}'
				return nil, err
			}
			return v, nil
		case '[':
			v := &Value{Kind: Array}
			for dec.More() {
				ev, err := parseValue(dec)
				if err != nil {
					return nil, err
				}
				v.Arr = append(v.Arr, ev)
			}
			if _, err := dec.Token(); err != nil { // consume ']'
				return nil, err
			}
			return v, nil
		}
		return nil, fmt.Errorf("unexpected delimiter %v", t)
	case string:
		return &Value{Kind: String, Str: t}, nil
	case json.Number:
		return &Value{Kind: Number, Num: t}, nil
	case bool:
		return &Value{Kind: Bool, Bool: t}, nil
	case nil:
		return &Value{Kind: Null}, nil
	}
	return nil, fmt.Errorf("unexpected token %v", tok)
}

// --- encode (Python json.dump(indent=2, ensure_ascii=False) format) ---------

// Encode renders the value exactly as Python's json.dump with indent=2 and
// ensure_ascii=False, plus a trailing newline (the legacy _write_settings).
func Encode(v *Value) []byte {
	var b strings.Builder
	encode(&b, v, 0)
	b.WriteByte('\n')
	return []byte(b.String())
}

func encode(b *strings.Builder, v *Value, depth int) {
	pad := strings.Repeat("  ", depth)
	inner := pad + "  "
	switch v.Kind {
	case Null:
		b.WriteString("null")
	case Bool:
		if v.Bool {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case Number:
		b.WriteString(v.Num.String())
	case String:
		writeString(b, v.Str)
	case Array:
		if len(v.Arr) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteString("[\n")
		for i, e := range v.Arr {
			b.WriteString(inner)
			encode(b, e, depth+1)
			if i < len(v.Arr)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		b.WriteString(pad + "]")
	case Object:
		if len(v.Obj) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteString("{\n")
		for i, m := range v.Obj {
			b.WriteString(inner)
			writeString(b, m.Key)
			b.WriteString(": ")
			encode(b, m.Val, depth+1)
			if i < len(v.Obj)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		b.WriteString(pad + "}")
	}
}

// writeString escapes exactly like Python json (ensure_ascii=False):
// short escapes for \" \\ \n \r \t \b \f, \u00XX for other control chars,
// everything else raw.
func writeString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			if r < 0x20 {
				b.WriteString(fmt.Sprintf(`\u%04x`, r))
			} else if r == utf8.RuneError {
				// invalid UTF-8 byte: Python would have failed reading; emit replacement
				b.WriteRune(r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

// --- settings file I/O -------------------------------------------------------

// ReadSettings parses path, or returns an empty object if the file is absent
// or blank. A malformed file returns an error — callers must abort rather
// than overwrite a user's real (but unparseable-here) settings.
func ReadSettings(path string) (*Value, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return NewObject(), nil
		}
		return nil, err
	}
	if strings.TrimSpace(string(b)) == "" {
		return NewObject(), nil
	}
	return Parse(b)
}

// WriteSettings writes atomically (temp file + rename in the same dir), so an
// interrupted write can never truncate the user's real settings.json.
func WriteSettings(path string, v *Value) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".settings-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(Encode(v)); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// --- hook registration (port of register_hook / unregister_hook) ------------

// truthy mirrors Python truthiness for JSON values.
func truthy(v *Value) bool {
	if v == nil {
		return false
	}
	switch v.Kind {
	case Null:
		return false
	case Bool:
		return v.Bool
	case Number:
		f, err := v.Num.Float64()
		return err == nil && f != 0
	case String:
		return v.Str != ""
	case Array:
		return len(v.Arr) > 0
	case Object:
		return len(v.Obj) > 0
	}
	return false
}

// entryCommands lists the command strings inside one hooks entry.
func entryCommands(entry *Value) []string {
	var out []string
	inner := entry.Get("hooks")
	if inner == nil || inner.Kind != Array {
		return out
	}
	for _, h := range inner.Arr {
		if h.Kind == Object {
			if c := h.Get("command"); c != nil && c.Kind == String {
				out = append(out, c.Str)
			}
		}
	}
	return out
}

// RegisterHook idempotently adds {matcher?, hooks:[{type:command,command}]}
// under .hooks.<event>, keyed on the command string.
func RegisterHook(path, event, matcher, command string) error {
	obj, err := ReadSettings(path)
	if err != nil {
		return err
	}
	hooks := obj.Get("hooks")
	if hooks == nil {
		hooks = NewObject()
		obj.Set("hooks", hooks)
	}
	if hooks.Kind != Object {
		return errors.New("settings .hooks is not an object")
	}
	arr := hooks.Get(event)
	if arr == nil {
		arr = NewArray()
		hooks.Set(event, arr)
	}
	if arr.Kind != Array {
		return fmt.Errorf("settings .hooks.%s is not an array", event)
	}
	for _, e := range arr.Arr {
		if e.Kind != Object {
			continue
		}
		for _, c := range entryCommands(e) {
			if c == command {
				return WriteSettings(path, obj) // already registered; rewrite like legacy
			}
		}
	}
	entry := NewObject()
	if matcher != "" {
		entry.Set("matcher", NewString(matcher)) // matcher first, like the legacy object
	}
	hook := NewObject()
	hook.Set("type", NewString("command"))
	hook.Set("command", NewString(command))
	inner := NewArray()
	inner.Arr = append(inner.Arr, hook)
	entry.Set("hooks", inner)
	arr.Arr = append(arr.Arr, entry)
	return WriteSettings(path, obj)
}

// UnregisterHook strips the given hook commands from every event. Sibling
// hooks inside a grouped entry are KEPT; an entry is dropped only once it has
// no hooks left. Absent/invalid .hooks is a silent no-op (no write), matching
// the legacy behavior.
func UnregisterHook(path string, commands []string) error {
	obj, err := ReadSettings(path)
	if err != nil {
		return err
	}
	hooks := obj.Get("hooks")
	if hooks == nil || hooks.Kind != Object {
		return nil
	}
	cmds := map[string]bool{}
	for _, c := range commands {
		cmds[c] = true
	}
	for _, m := range hooks.Obj {
		arr := m.Val
		if arr.Kind != Array {
			continue
		}
		var kept []*Value
		for _, e := range arr.Arr {
			if e.Kind != Object {
				kept = append(kept, e)
				continue
			}
			inner := e.Get("hooks")
			if inner != nil && inner.Kind == Array {
				var keepHooks []*Value
				for _, h := range inner.Arr {
					if h.Kind == Object {
						if c := h.Get("command"); c != nil && c.Kind == String && cmds[c.Str] {
							continue
						}
					}
					keepHooks = append(keepHooks, h)
				}
				ne := &Value{Kind: Object, Obj: append([]Member(nil), e.Obj...)}
				ne.Set("hooks", &Value{Kind: Array, Arr: keepHooks})
				e = ne
			}
			// drop the entry when its hooks value is falsy (empty list, absent,
			// null, "" …) — Python truthiness on e.get("hooks")
			if truthy(e.Get("hooks")) {
				kept = append(kept, e)
			}
		}
		m.Val.Arr = kept
	}
	return WriteSettings(path, obj)
}
