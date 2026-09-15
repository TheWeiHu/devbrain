package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/TheWeiHu/devbrain/internal/config"
	"github.com/TheWeiHu/devbrain/internal/install"
)

func cmdBrains(args []string) int {
	if err := brains(args); err != nil {
		fmt.Fprintf(os.Stderr, "devbrain brains: %v\n", err)
		return 1
	}
	return 0
}

func brains(args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "help" {
		fmt.Print(`devbrain brains
  list                              show registered brains and assignments
  current                           show the brain selected here
  add NAME --data DIR [--repo URL]   register a checkout, or clone into a new DIR
  assign NAME [PROJECT]             route a project (default: current repository)
  default NAME                      choose the default for unassigned projects

Use devbrain --brain NAME <command> for a single command.
Assignments affect new sessions. Existing captured sessions stay in their brain.
`)
		return nil
	}
	r, err := config.Catalog()
	if err != nil {
		return err
	}
	switch args[0] {
	case "list":
		for _, b := range r.Brains {
			label := ""
			if b.Name == r.Default {
				label = " (default)"
			}
			fmt.Printf("%s%s\t%s\n", b.Name, label, b.Data)
		}
		keys := make([]string, 0, len(r.Projects))
		for project := range r.Projects {
			keys = append(keys, project)
		}
		sort.Strings(keys)
		for _, project := range keys {
			fmt.Printf("%s → %s\n", project, r.Projects[project])
		}
	case "current":
		cwd, _ := os.Getwd()
		b, err := config.Resolve(cwd)
		if err != nil {
			return err
		}
		fmt.Printf("%s\t%s\n", b.Name, b.Data)
	case "add":
		if len(args) < 2 {
			return fmt.Errorf("add requires NAME --data DIR")
		}
		name := args[1]
		if _, err := r.Named(name); err == nil {
			return fmt.Errorf("brain %q already exists", name)
		}
		fs := flag.NewFlagSet("brains add", flag.ContinueOnError)
		data := fs.String("data", "", "local data checkout")
		remote := fs.String("repo", "", "GitHub owner/repo or git URL")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if *data == "" || fs.NArg() != 0 {
			return fmt.Errorf("add requires --data DIR")
		}
		if strings.HasPrefix(*data, "~/") {
			home, _ := os.UserHomeDir()
			*data = filepath.Join(home, strings.TrimPrefix(*data, "~/"))
		}
		*data, err = filepath.Abs(*data)
		if err != nil {
			return err
		}
		if err := config.ValidateRegistration(name, *data); err != nil {
			return err
		}
		if _, err := os.Stat(*data); os.IsNotExist(err) {
			if *remote == "" {
				return fmt.Errorf("checkout does not exist; provide --repo to clone it")
			}
			url := *remote
			if !strings.ContainsAny(url, ":@") && !filepath.IsAbs(url) {
				url = "https://github.com/" + url + ".git"
			}
			cmd := exec.Command("git", "clone", "--", url, *data)
			cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
			if err := cmd.Run(); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		b := config.Brain{Name: name, Data: *data}
		if err := b.Available(); err != nil {
			return err
		}
		out, err := exec.Command("git", "-C", *data, "remote", "get-url", "origin").Output()
		if err != nil || strings.TrimSpace(string(out)) == "" {
			return fmt.Errorf("checkout needs an origin remote")
		}
		actual := remoteIdentity(string(out))
		if *remote != "" && actual != remoteIdentity(*remote) {
			return fmt.Errorf("existing checkout's origin does not match --repo")
		}
		for _, other := range r.Brains {
			origin, err := exec.Command("git", "-C", other.Data, "remote", "get-url", "origin").Output()
			if err == nil && remoteIdentity(string(origin)) == actual {
				return fmt.Errorf("brain %q already uses this remote; separate brains need separate repositories", other.Name)
			}
		}
		if err := config.RegisterBrain(name, *data); err != nil {
			return err
		}
		install.RefreshAgentsPrefs()
		if rc := install.LinkPreferences(nil, os.Stdout, os.Stderr); rc != 0 {
			return fmt.Errorf("brain registered, but preference wiring needs repair")
		}
		fmt.Printf("Registered %s at %s. Existing history is unchanged.\n", name, *data)
	case "assign":
		if len(args) < 2 || len(args) > 3 {
			return fmt.Errorf("assign requires NAME [PROJECT]")
		}
		cwd, _ := os.Getwd()
		project := config.Project(cwd)
		if len(args) == 3 {
			project = args[2]
		}
		if project == "miscellaneous" && len(args) == 2 {
			return fmt.Errorf("no repository identity; provide PROJECT explicitly")
		}
		if err := config.AssignBrain(project, args[1]); err != nil {
			return err
		}
		fmt.Printf("%s → %s (new sessions; existing captures stay in place)\n", project, args[1])
	case "default":
		if len(args) != 2 {
			return fmt.Errorf("default requires NAME")
		}
		if err := config.SetDefaultBrain(args[1]); err != nil {
			return err
		}
		fmt.Printf("Default brain: %s\n", args[1])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
	return nil
}

func remoteIdentity(remote string) string {
	remote = strings.TrimSpace(remote)
	if filepath.IsAbs(remote) {
		return config.DataID(remote)
	}
	remote = strings.TrimPrefix(remote, "ssh://")
	remote = strings.TrimPrefix(remote, "https://")
	remote = strings.TrimPrefix(remote, "http://")
	if strings.HasPrefix(remote, "git@") {
		remote = strings.Replace(strings.TrimPrefix(remote, "git@"), ":", "/", 1)
	}
	remote = strings.TrimSuffix(strings.TrimRight(remote, "/"), ".git")
	if strings.Count(remote, "/") == 1 {
		remote = "github.com/" + remote
	}
	return strings.ToLower(remote)
}
