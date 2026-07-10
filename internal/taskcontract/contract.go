// Package taskcontract parses and validates the optional machine-readable
// scheduling contract attached to a TODO task.
package taskcontract

import (
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/TheWeiHu/devbrain/internal/task"
)

const Version = 1

var (
	taskIDRE   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	resourceRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]*$`)
	taskTypes  = []string{"bug", "chore", "docs", "feature", "investigation", "landing", "refactor", "test"}
)

// Contract is the parsed task contract. Scheduling fields live in flat
// frontmatter; the richer delegation contract remains readable in Markdown.
type Contract struct {
	Version      int      `json:"version"`
	Type         string   `json:"type"`
	DependsOn    []string `json:"depends_on"`
	ConflictKeys []string `json:"conflict_keys"`
	BudgetTurns  int      `json:"budget_turns"`
	Outcome      string   `json:"outcome"`
	Evidence     string   `json:"evidence"`
	Scope        string   `json:"scope"`
	NonGoals     string   `json:"non_goals"`
	Acceptance   string   `json:"acceptance"`
	Verify       string   `json:"verify"`
}

// Report classifies a task as legacy, valid, or invalid. Legacy is advisory:
// existing queues keep working until contract scheduling is explicitly enabled.
type Report struct {
	ID       string   `json:"id"`
	State    string   `json:"state"`
	Contract Contract `json:"contract"`
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`
}

// Inspect parses and validates one task's optional versioned contract.
func Inspect(t *task.Task) Report {
	r := Report{ID: t.ID, State: "legacy", Errors: []string{}, Warnings: []string{}}
	versionRaw := strings.TrimSpace(t.Raw("contract_version"))
	if versionRaw == "" {
		r.Warnings = append(r.Warnings, "legacy task: no contract_version")
		return r
	}

	version, err := strconv.Atoi(versionRaw)
	if err != nil || version != Version {
		r.State = "invalid"
		r.Errors = append(r.Errors, "contract_version must be 1")
		return r
	}
	r.Contract.Version = version
	r.Contract.Type = strings.ToLower(strings.TrimSpace(t.Raw("task_type")))
	r.Contract.DependsOn = splitList(t.Raw("depends_on"), true)
	r.Contract.ConflictKeys = splitList(t.Raw("conflict_keys"), false)
	r.Contract.BudgetTurns, _ = strconv.Atoi(strings.TrimSpace(t.Raw("budget_turns")))

	labels := bodyLabels(t.Body)
	r.Contract.Outcome = labels["Outcome"]
	r.Contract.Evidence = labels["Evidence"]
	r.Contract.Scope = labels["Scope"]
	r.Contract.NonGoals = labels["Non-goals"]
	r.Contract.Acceptance = labels["Acceptance"]
	r.Contract.Verify = labels["Verify"]

	if !slices.Contains(taskTypes, r.Contract.Type) {
		r.Errors = append(r.Errors, "task_type must be bug, chore, docs, feature, investigation, landing, refactor, or test")
	}
	if strings.TrimSpace(t.Raw("depends_on")) == "" {
		r.Errors = append(r.Errors, "depends_on is required (use none when unblocked)")
	}
	for _, id := range r.Contract.DependsOn {
		if !taskIDRE.MatchString(id) {
			r.Errors = append(r.Errors, "invalid dependency id: "+id)
		}
	}
	if len(r.Contract.ConflictKeys) == 0 {
		r.Errors = append(r.Errors, "conflict_keys requires at least one path: or resource: key")
	}
	for _, key := range r.Contract.ConflictKeys {
		if err := validConflictKey(key); err != "" {
			r.Errors = append(r.Errors, err)
		}
	}
	if r.Contract.BudgetTurns < 1 {
		r.Errors = append(r.Errors, "budget_turns must be a positive integer")
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{"Outcome", r.Contract.Outcome},
		{"Evidence", r.Contract.Evidence},
		{"Scope", r.Contract.Scope},
		{"Non-goals", r.Contract.NonGoals},
		{"Acceptance", r.Contract.Acceptance},
		{"Verify", r.Contract.Verify},
	} {
		if emptyValue(field.value) {
			r.Errors = append(r.Errors, field.name+" body field is required")
		}
	}

	if len(r.Errors) == 0 {
		r.State = "valid"
	} else {
		r.State = "invalid"
	}
	return r
}

func emptyValue(value string) bool {
	v := strings.TrimSpace(value)
	return v == "" || (strings.HasPrefix(v, "<") && strings.HasSuffix(v, ">"))
}

func splitList(value string, noneIsEmpty bool) []string {
	value = strings.TrimSpace(value)
	if noneIsEmpty && strings.EqualFold(value, "none") {
		return []string{}
	}
	seen := map[string]bool{}
	out := []string{}
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

func validConflictKey(key string) string {
	prefix, value, ok := strings.Cut(key, ":")
	if !ok || (prefix != "path" && prefix != "resource") {
		return "invalid conflict key " + key + ": use path:<repo-relative path> or resource:<name>"
	}
	value = strings.TrimSpace(value)
	if prefix == "resource" {
		if !resourceRE.MatchString(value) {
			return "invalid resource conflict key: " + key
		}
		return ""
	}
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, "*") {
		return "invalid path conflict key: " + key
	}
	trimmed := strings.TrimSuffix(value, "/")
	clean := path.Clean(trimmed)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "invalid path conflict key: " + key
	}
	if clean != trimmed {
		return "path conflict key must be normalized: " + key
	}
	return ""
}

func bodyLabels(body string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## Context (synthesized ") {
			break
		}
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
		if strings.HasPrefix(trimmed, "**") {
			if key, value, ok := strings.Cut(strings.TrimPrefix(trimmed, "**"), ":**"); ok {
				setLabel(out, strings.TrimSpace(key), strings.TrimSpace(value))
				continue
			}
		}
		if key, value, ok := strings.Cut(trimmed, ":"); ok {
			setLabel(out, strings.TrimSpace(key), strings.TrimSpace(value))
		}
	}
	return out
}

func setLabel(labels map[string]string, key, value string) {
	if _, ok := labels[key]; ok {
		return
	}
	switch key {
	case "Outcome", "Evidence", "Scope", "Non-goals", "Acceptance", "Verify":
		labels[key] = value
	}
}

// Conflict returns the first scheduling key shared by two valid contracts.
// Exact resource keys conflict; path directory keys ending in / also conflict
// with descendants.
func Conflict(a, b Contract) string {
	for _, left := range a.ConflictKeys {
		for _, right := range b.ConflictKeys {
			if left == right {
				return left
			}
			if pathKeyContains(left, right) || pathKeyContains(right, left) {
				return left
			}
		}
	}
	return ""
}

func pathKeyContains(parent, child string) bool {
	p, okP := strings.CutPrefix(parent, "path:")
	c, okC := strings.CutPrefix(child, "path:")
	return okP && okC && strings.HasSuffix(p, "/") && strings.HasPrefix(c, p)
}
