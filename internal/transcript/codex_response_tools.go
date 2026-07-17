package transcript

// Current Codex hosts can encode tool calls as response_item records instead
// of the older event_msg exec_command_begin/end pair. Conductor's exec tool is
// a small JavaScript cell, so this file tokenizes only enough JavaScript to
// recover direct tools.<name>(...) calls and their literal string arguments.

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
)

type codexResponseTool struct {
	name  string
	cmd   string
	patch string
}

type codexJSToken struct {
	kind byte // i: identifier, s: decoded string, p: punctuation
	text string
}

var codexPatchFileRe = regexp.MustCompile(`(?m)^\*\*\* (?:Add|Update|Delete) File: (.+?)[ \t]*$`)

func codexResponseTools(p map[string]any) []codexResponseTool {
	name := getStr(p, "name")
	switch getStr(p, "type") {
	case "function_call":
		args := getStr(p, "arguments")
		if args == "" {
			if m, ok := p["arguments"].(map[string]any); ok {
				b, _ := json.Marshal(m)
				args = string(b)
			}
		}
		return []codexResponseTool{{
			name:  name,
			cmd:   jsonStringField(args, "cmd"),
			patch: firstNonEmptyString(jsonStringField(args, "patch"), jsonStringField(args, "input")),
		}}
	case "custom_tool_call":
		input := getStr(p, "input")
		if name == "exec" {
			if calls := scanCodexToolCell(input); len(calls) > 0 {
				return calls
			}
		}
		call := codexResponseTool{name: name}
		if name == "exec_command" {
			call.cmd = jsonStringField(input, "cmd")
		}
		if name == "apply_patch" {
			call.patch = input
		}
		return []codexResponseTool{call}
	}
	return nil
}

func jsonStringField(src, field string) string {
	if src == "" {
		return ""
	}
	var obj map[string]any
	if json.Unmarshal([]byte(src), &obj) == nil {
		return getStr(obj, field)
	}
	values := jsTokenFields(tokenizeCodexJS(src), field)
	if len(values) > 0 {
		return values[0]
	}
	return ""
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// scanCodexToolCell ignores tool-shaped text inside strings/comments. When a
// parallel map passes a variable to exec_command, literal cmd fields from the
// surrounding cell replace that otherwise command-less invocation.
func scanCodexToolCell(src string) []codexResponseTool {
	tokens := tokenizeCodexJS(src)
	var calls []codexResponseTool
	missingExecCommand := false
	for i := 0; i+3 < len(tokens); i++ {
		if tokens[i].kind != 'i' || tokens[i].text != "tools" ||
			tokens[i+1].text != "." || tokens[i+2].kind != 'i' || tokens[i+3].text != "(" {
			continue
		}
		close := matchingTokenParen(tokens, i+3)
		if close < 0 {
			break
		}
		name := tokens[i+2].text
		args := tokens[i+4 : close]
		call := codexResponseTool{name: name}
		switch name {
		case "exec_command":
			if values := jsTokenFields(args, "cmd"); len(values) > 0 {
				call.cmd = values[0]
			}
			missingExecCommand = missingExecCommand || call.cmd == ""
		case "apply_patch":
			call.patch = firstStringToken(args)
			if call.patch == "" {
				if values := jsTokenFields(args, "patch"); len(values) > 0 {
					call.patch = values[0]
				}
			}
		}
		calls = append(calls, call)
		i = close
	}
	if !missingExecCommand {
		return calls
	}

	known := map[string]int{}
	filtered := calls[:0]
	for _, call := range calls {
		if call.name == "exec_command" {
			if call.cmd == "" {
				continue
			}
			known[call.cmd]++
		}
		filtered = append(filtered, call)
	}
	added := 0
	for _, cmd := range jsTokenFields(tokens, "cmd") {
		if known[cmd] > 0 {
			known[cmd]--
			continue
		}
		filtered = append(filtered, codexResponseTool{name: "exec_command", cmd: cmd})
		added++
	}
	if added == 0 {
		filtered = append(filtered, codexResponseTool{name: "exec_command"})
	}
	return filtered
}

func tokenizeCodexJS(src string) []codexJSToken {
	var tokens []codexJSToken
	for i := 0; i < len(src); {
		switch {
		case strings.ContainsRune(" \t\r\n", rune(src[i])):
			i++
		case i+1 < len(src) && src[i:i+2] == "//":
			if n := strings.IndexByte(src[i+2:], '\n'); n >= 0 {
				i += n + 3
			} else {
				i = len(src)
			}
		case i+1 < len(src) && src[i:i+2] == "/*":
			if n := strings.Index(src[i+2:], "*/"); n >= 0 {
				i += n + 4
			} else {
				i = len(src)
			}
		case src[i] == '\'' || src[i] == '"' || src[i] == '`':
			value, next, ok := readCodexJSString(src, i)
			if !ok {
				return tokens
			}
			tokens = append(tokens, codexJSToken{kind: 's', text: value})
			i = next
		case isCodexJSIdent(src[i]):
			end := i + 1
			for end < len(src) && isCodexJSIdent(src[end]) {
				end++
			}
			tokens = append(tokens, codexJSToken{kind: 'i', text: src[i:end]})
			i = end
		default:
			tokens = append(tokens, codexJSToken{kind: 'p', text: src[i : i+1]})
			i++
		}
	}
	return tokens
}

func isCodexJSIdent(b byte) bool {
	return b == '_' || b == '$' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

func readCodexJSString(src string, start int) (string, int, bool) {
	quote := src[start]
	for i := start + 1; i < len(src); i++ {
		if src[i] == '\\' {
			i++
			continue
		}
		if src[i] != quote {
			continue
		}
		raw := src[start : i+1]
		if quote == '"' {
			var value string
			if json.Unmarshal([]byte(raw), &value) == nil {
				return value, i + 1, true
			}
		}
		return unescapeCodexJS(raw[1 : len(raw)-1]), i + 1, true
	}
	return "", len(src), false
}

func unescapeCodexJS(src string) string {
	var b strings.Builder
	for i := 0; i < len(src); i++ {
		if src[i] != '\\' || i+1 >= len(src) {
			b.WriteByte(src[i])
			continue
		}
		i++
		switch src[i] {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case '\n':
			// JavaScript line continuation.
		default:
			b.WriteByte(src[i])
		}
	}
	return b.String()
}

func matchingTokenParen(tokens []codexJSToken, open int) int {
	depth := 0
	for i := open; i < len(tokens); i++ {
		switch tokens[i].text {
		case "(":
			depth++
		case ")":
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func jsTokenFields(tokens []codexJSToken, field string) []string {
	var values []string
	for i := 0; i+2 < len(tokens); i++ {
		if (tokens[i].kind == 'i' || tokens[i].kind == 's') && tokens[i].text == field &&
			tokens[i+1].text == ":" && tokens[i+2].kind == 's' {
			values = append(values, tokens[i+2].text)
		}
	}
	return values
}

func firstStringToken(tokens []codexJSToken) string {
	for _, token := range tokens {
		if token.kind == 's' {
			return token.text
		}
	}
	return ""
}

func codexToolOutput(p map[string]any) string {
	switch output := p["output"].(type) {
	case string:
		return output
	case []any:
		var b strings.Builder
		for _, item := range output {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			switch getStr(m, "type") {
			case "input_text", "output_text", "text":
				b.WriteString(getStr(m, "text"))
			}
		}
		return b.String()
	}
	return ""
}

func addCodexPatchFiles(files *Set, patch string) {
	for _, match := range codexPatchFileRe.FindAllStringSubmatch(patch, -1) {
		path := strings.TrimSpace(match[1])
		if path != "" {
			files.Add(filepath.Base(path))
		}
	}
}
