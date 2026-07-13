package install

// codextrust.go — record Codex hook trust for devbrain's own hooks. Codex
// only runs a user hook whose fingerprint matches the trusted_hash persisted
// in config.toml; every hooks.json rewrite (each install/upgrade) invalidates
// that hash and Codex silently skips capture until the user re-approves via
// /hooks. Since the user just ran `devbrain install`, stamping trust for the
// hooks devbrain itself wrote is the intent — the same record /hooks
// "Trust all" makes.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// codexEventLabels maps the hooks.json event names devbrain registers to
// Codex's snake_case fingerprint labels.
var codexEventLabels = map[string]string{
	"UserPromptSubmit": "user_prompt_submit",
	"PostToolUse":      "post_tool_use",
	"Stop":             "stop",
	"SessionStart":     "session_start",
}

type codexHooksFile struct {
	Hooks map[string][]codexMatcherGroup `json:"hooks"`
}

type codexMatcherGroup struct {
	Matcher *string        `json:"matcher"`
	Hooks   []codexHandler `json:"hooks"`
}

type codexHandler struct {
	Type          string  `json:"type"`
	Command       string  `json:"command"`
	Timeout       *uint64 `json:"timeout"`
	Async         bool    `json:"async"`
	StatusMessage *string `json:"statusMessage"`
}

// isDevbrainHook reports whether a hook command is one devbrain registers:
// `[DEVBRAIN_HARNESS=codex] <path-to-devbrain> hook <name>`.
func isDevbrainHook(command string) bool {
	fields := strings.Fields(command)
	if len(fields) == 4 && fields[0] == "DEVBRAIN_HARNESS=codex" {
		fields = fields[1:]
	}
	return len(fields) == 3 && fields[1] == "hook" &&
		strings.Contains(filepath.Base(fields[0]), "devbrain")
}

// codexHookHash mirrors Codex's command_hook_hash + version_for_toml
// (codex-rs rust-v0.138.0): a normalized hook identity serialized as
// canonical sorted-key JSON, SHA-256'd. Verified against trusted_hash values
// the real Codex binary accepts.
func codexHookHash(eventLabel string, matcher *string, h codexHandler) string {
	timeout := uint64(600)
	if h.Timeout != nil && *h.Timeout > 0 {
		timeout = *h.Timeout
	}
	normalized := map[string]any{
		"type":    "command",
		"command": h.Command,
		"timeout": timeout,
		"async":   h.Async,
	}
	if h.StatusMessage != nil {
		normalized["statusMessage"] = *h.StatusMessage
	}
	identity := map[string]any{
		"event_name": eventLabel,
		"hooks":      []any{normalized},
	}
	// Codex strips matchers from events that don't support them before
	// fingerprinting.
	if matcher != nil && eventLabel != "user_prompt_submit" && eventLabel != "stop" {
		identity["matcher"] = *matcher
	}
	b, _ := json.Marshal(identity) // encoding/json sorts map keys recursively
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// trustCodexHooks stamps trusted_hash in configTOML's [hooks.state] tables
// for every devbrain hook found in hooksJSON, keyed the way Codex keys them
// (<hooks.json path>:<event label>:<group>:<handler>). Foreign hooks and all
// other config content are left untouched. Returns how many hooks were
// stamped.
func trustCodexHooks(hooksJSON, configTOML string) (int, error) {
	b, err := os.ReadFile(hooksJSON)
	if err != nil {
		return 0, err
	}
	var file codexHooksFile
	if err := json.Unmarshal(b, &file); err != nil {
		return 0, fmt.Errorf("parse %s: %w", hooksJSON, err)
	}
	trust := map[string]string{} // state key -> current hash
	for event, groups := range file.Hooks {
		label, ok := codexEventLabels[event]
		if !ok {
			continue
		}
		for gi, group := range groups {
			for hi, h := range group.Hooks {
				if h.Type != "command" || !isDevbrainHook(h.Command) {
					continue
				}
				key := fmt.Sprintf("%s:%s:%d:%d", hooksJSON, label, gi, hi)
				trust[key] = codexHookHash(label, group.Matcher, h)
			}
		}
	}
	if len(trust) == 0 {
		return 0, nil
	}
	if err := upsertTrustedHashes(configTOML, trust); err != nil {
		return 0, err
	}
	return len(trust), nil
}

// upsertTrustedHashes rewrites configTOML line-wise: for each state key its
// [hooks.state."<key>"] table gets trusted_hash replaced (or inserted after
// the header); missing tables are appended at EOF. Everything else is
// preserved byte-for-byte.
func upsertTrustedHashes(configTOML string, trust map[string]string) error {
	raw, err := os.ReadFile(configTOML)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	lines := strings.Split(string(raw), "\n")
	header := func(key string) string { return "[hooks.state." + strconv.Quote(key) + "]" }

	done := map[string]bool{}
	var out []string
	current := "" // state key of the table we're inside, if one of ours
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") {
			current = ""
			for key := range trust {
				if t == header(key) {
					current = key
					break
				}
			}
			out = append(out, line)
			if current != "" && !done[current] {
				out = append(out, `trusted_hash = "`+trust[current]+`"`)
				done[current] = true
			}
			continue
		}
		if current != "" && strings.HasPrefix(t, "trusted_hash") {
			continue // replaced at the header
		}
		out = append(out, line)
	}
	for key, hash := range trust {
		if done[key] {
			continue
		}
		if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
			out = append(out, "")
		}
		out = append(out, header(key), `trusted_hash = "`+hash+`"`, "")
	}
	text := strings.Join(out, "\n")
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return os.WriteFile(configTOML, []byte(text), 0o644)
}
