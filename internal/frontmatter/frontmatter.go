// Package frontmatter reads and writes the task files' flat `key: value`
// header block. The format is deliberately NOT YAML: the legacy readers are
// todo.sh's awk line-matching and queue.py's split(":", 1), both preserving
// key insertion order on rewrite. A real YAML library would re-quote and
// re-order values, breaking the frozen on-disk format — so this package is
// the one shared implementation both the todo CLI and the queue server use.
//
// Reading goes through Parse (queue.py's task view: fm map + key order +
// title + body — see internal/task). Writing keeps the two legacy shapes:
//   - SetField — todo.sh's awk field edit, operating on whole file content so
//     untouched lines survive byte-for-byte;
//   - Render — queue.py's full-file rewrite in original key order.
package frontmatter

import "strings"

// fence reports whether a line is a frontmatter fence: `---` plus optional
// trailing whitespace (awk /^---[[:space:]]*$/).
func fence(line string) bool {
	if !strings.HasPrefix(line, "---") {
		return false
	}
	return strings.TrimRight(line[3:], " \t\v\f\r") == ""
}

// splitKeep splits content into lines without dropping a trailing newline
// marker; join with "\n" reproduces the input exactly.
func splitKeep(content string) []string {
	return strings.Split(content, "\n")
}

// SetField updates a frontmatter field in place; if absent, it is inserted
// just before the closing fence — the todo.sh set_field awk. Content is
// returned with all other lines untouched.
func SetField(content, key, value string) string {
	lines := splitKeep(content)
	out := make([]string, 0, len(lines)+1)
	n, done := 0, false
	for _, line := range lines {
		if fence(line) {
			n++
			if n == 2 && !done {
				out = append(out, key+": "+value)
				done = true
			}
			out = append(out, line)
			continue
		}
		if n == 1 && !done && strings.HasPrefix(line, key+":") {
			out = append(out, key+": "+value)
			done = true
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// Task is queue.py's parsed view of a task file.
type Task struct {
	FM    map[string]string
	Order []string
	Title string
	Body  string
}

// Parse ports queue.py's Queue.parse regex split: ^---\n(.*?)\n---\n?(.*)$
// (non-greedy, DOTALL). Files without a valid fence pair are all body.
func Parse(text string) Task {
	t := Task{FM: map[string]string{}}
	rest := ""
	if strings.HasPrefix(text, "---\n") {
		// non-greedy: FIRST "\n---" after the opening fence
		if i := strings.Index(text[4:], "\n---"); i >= 0 {
			head := text[4 : 4+i]
			tail := text[4+i+4:] // after "\n---"
			tail = strings.TrimPrefix(tail, "\n")
			for _, line := range strings.Split(head, "\n") {
				if k, v, ok := strings.Cut(line, ":"); ok {
					k = strings.TrimSpace(k)
					t.FM[k] = strings.TrimSpace(v)
					t.Order = append(t.Order, k)
				}
			}
			rest = tail
		} else {
			rest = text
			t.Body = strings.TrimSpace(rest)
			return t
		}
	} else {
		t.Body = strings.TrimSpace(text)
		return t
	}
	lines := strings.Split(rest, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "# ") {
			t.Title = strings.TrimSpace(l[2:])
			t.Body = strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
			return t
		}
	}
	t.Body = strings.TrimSpace(rest)
	return t
}

// Render ports queue.py's Queue.write body: existing keys in original order
// (skipping deletions), then any new keys in `updates` order, then the title
// and body. updates values of "" write empty; a key present in deleted is
// removed.
func Render(order []string, fm map[string]string, newKeys []string, title, body string) string {
	var b strings.Builder
	b.WriteString("---\n")
	for _, k := range order {
		b.WriteString(k + ": " + fm[k] + "\n")
	}
	for _, k := range newKeys {
		b.WriteString(k + ": " + fm[k] + "\n")
	}
	b.WriteString("---\n\n# " + title + "\n\n" + strings.TrimRight(body, " \t\n\r\v\f") + "\n")
	return b.String()
}
