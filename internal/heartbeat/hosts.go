package heartbeat

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/TheWeiHu/devbrain/internal/config"
)

const retiredFile = "retired-hosts.json"

// Record the last capture, not retirement time: any newer capture revives a host.
func readRetired(dataDir string) (map[string]time.Time, error) {
	m := map[string]time.Time{}
	b, err := os.ReadFile(filepath.Join(dataDir, retiredFile))
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]time.Time{}
	}
	return m, nil
}

func writeRetired(dataDir string, m map[string]time.Time) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dataDir, ".retired-hosts-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dataDir, retiredFile))
}

// Run manages capture sources in the selected brain only.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] == "list" && len(args) != 1) ||
		((args[0] == "retire" || args[0] == "restore") && len(args) != 2) ||
		(args[0] != "list" && args[0] != "retire" && args[0] != "restore") {
		fmt.Fprintln(stderr, "usage: devbrain hosts list | retire <hostname> | restore <hostname>")
		return 2
	}
	dataDir, err := config.ResolveDataDir()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	m, err := readRetired(dataDir)
	if err != nil {
		fmt.Fprintf(stderr, "hosts: read %s: %v\n", retiredFile, err)
		return 1
	}
	st := Check(dataDir)
	if args[0] == "list" {
		known := map[string]Host{}
		for name, last := range m {
			known[name] = Host{Name: name, Last: last}
		}
		for _, h := range st.Hosts {
			known[h.Name] = h
		}
		names := make([]string, 0, len(known))
		for name := range known {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			h := known[name]
			state := "active"
			if Now().Sub(h.Last) > staleAfter {
				state = "stale"
			}
			if Now().Sub(h.Last) > activeWindow && name != selfHost() {
				state = "aged out"
			}
			if last, ok := m[name]; ok && !h.Last.After(last) && name != selfHost() {
				state = "retired"
			}
			fmt.Fprintf(stdout, "%s\t%s\tlast capture %s\n", name, state, h.Last.UTC().Format(time.RFC3339))
		}
		return 0
	}
	name := args[1]
	if args[0] == "retire" {
		if name == selfHost() {
			fmt.Fprintln(stderr, "hosts: cannot retire this machine; local capture warnings remain enabled")
			return 1
		}
		found := false
		for _, h := range st.Hosts {
			if h.Name == name {
				m[name], found = h.Last, true
			}
		}
		if _, ok := m[name]; !found && !ok {
			fmt.Fprintf(stderr, "hosts: unknown capture host %q; use hosts list\n", name)
			return 1
		}
	} else {
		if _, ok := m[name]; !ok {
			fmt.Fprintf(stderr, "hosts: %q is not retired\n", name)
			return 1
		}
		delete(m, name)
	}
	if err := writeRetired(dataDir, m); err != nil {
		fmt.Fprintf(stderr, "hosts: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "%s: %s in %s\n", args[0], name, dataDir)
	return 0
}
