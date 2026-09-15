package config

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/TheWeiHu/devbrain/internal/identity"
)

type Brain struct {
	Name string `json:"name"`
	Data string `json:"data"`
}

type Registry struct {
	Brains          []Brain
	Default         string
	Projects        map[string]string
	CaptureProjects []string
}

var brainName = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
var projectName = regexp.MustCompile(`^[a-z0-9._-]+$`)

func Catalog() (Registry, error) {
	f, err := loadStrict()
	if err != nil {
		return Registry{}, err
	}
	return catalog(f)
}

func catalog(f File) (Registry, error) {
	r := Registry{Default: f.DefaultBrain, Projects: f.ProjectBrains, CaptureProjects: f.CaptureProjects}
	if r.Default == "" {
		r.Default = "default"
	}
	data := f.Data
	if len(f.Brains) == 0 && os.Getenv("DEVBRAIN_BRAIN") == "" && os.Getenv("DEVBRAIN_DATA") != "" {
		data = os.Getenv("DEVBRAIN_DATA")
	}
	if data == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return r, err
		}
		data = filepath.Join(home, "devbrain-data")
	}
	paths := map[string]string{"default": data}
	for name, path := range f.Brains {
		if name == "default" || !brainName.MatchString(name) {
			return r, fmt.Errorf("invalid brain name %q (default is reserved)", name)
		}
		paths[name] = path
	}
	for name, path := range paths {
		p, err := requireAbs(expandHome(path), "brain "+name)
		if err != nil {
			return r, err
		}
		p = canonicalPath(p)
		for _, other := range r.Brains {
			if containsPath(p, other.Data) || containsPath(other.Data, p) {
				return r, fmt.Errorf("brains %q and %q have overlapping data paths", name, other.Name)
			}
		}
		r.Brains = append(r.Brains, Brain{Name: name, Data: p})
	}
	sort.Slice(r.Brains, func(i, j int) bool { return r.Brains[i].Name < r.Brains[j].Name })
	if _, err := r.Named(r.Default); err != nil {
		return r, err
	}
	for project, name := range r.Projects {
		if !projectName.MatchString(project) || project == "." || project == ".." {
			return r, fmt.Errorf("invalid project %q", project)
		}
		if _, err := r.Named(name); err != nil {
			return r, fmt.Errorf("project %s: %w", project, err)
		}
	}
	for _, project := range r.CaptureProjects {
		if !projectName.MatchString(project) || project == "." || project == ".." {
			return r, fmt.Errorf("invalid capture project %q", project)
		}
	}
	return r, nil
}

func (r Registry) AllowsCapture(project string) bool {
	if len(r.CaptureProjects) == 0 {
		return true
	}
	for _, allowed := range r.CaptureProjects {
		if project == allowed {
			return true
		}
	}
	return false
}

func (r Registry) Named(name string) (Brain, error) {
	for _, b := range r.Brains {
		if b.Name == name {
			return b, nil
		}
	}
	return Brain{}, fmt.Errorf("unknown brain %q; run devbrain brains list", name)
}

func (r Registry) ForProject(project string) Brain {
	name := r.Projects[project]
	if name == "" {
		name = r.Default
	}
	b, _ := r.Named(name)
	return b
}

func (r Registry) Selected() (Brain, bool, error) {
	name, path := os.Getenv("DEVBRAIN_BRAIN"), os.Getenv("DEVBRAIN_DATA")
	if name != "" {
		b, err := r.Named(name)
		if err == nil && path != "" && canonicalPath(expandHome(path)) != b.Data {
			err = fmt.Errorf("DEVBRAIN_BRAIN and DEVBRAIN_DATA select different brains")
		}
		return b, true, err
	}
	if path != "" {
		p, err := requireAbs(expandHome(path), "$DEVBRAIN_DATA")
		if err != nil {
			return Brain{}, true, err
		}
		for _, b := range r.Brains {
			if b.Data == canonicalPath(p) {
				return b, true, nil
			}
		}
		return Brain{}, true, fmt.Errorf("DEVBRAIN_DATA is not a registered brain; run devbrain brains add")
	}
	return Brain{}, false, nil
}

func Project(cwd string) string {
	if p := os.Getenv("DEVBRAIN_PROJECT"); p != "" {
		return strings.Map(func(c rune) rune {
			if c == ' ' {
				return '-'
			}
			if strings.ContainsRune("abcdefghijklmnopqrstuvwxyz0123456789._-", c) {
				return c
			}
			return -1
		}, strings.ToLower(p))
	}
	out, _ := exec.Command("git", "-C", cwd, "remote", "get-url", "origin").Output()
	remote := strings.TrimSpace(string(out))
	for _, prefix := range []string{"/", "./", "../", "~", "file://"} {
		if strings.HasPrefix(remote, prefix) {
			return "miscellaneous"
		}
	}
	if key := identity.RemoteToKey(remote); key != "" {
		return key
	}
	return "miscellaneous"
}

func Resolve(cwd string) (Brain, error) {
	r, err := Catalog()
	if err != nil {
		return Brain{}, err
	}
	b, selected, err := r.Selected()
	if err != nil {
		return Brain{}, err
	}
	if !selected {
		b = r.ForProject(Project(cwd))
		for _, candidate := range r.Brains {
			if containsPath(candidate.Data, canonicalPath(cwd)) {
				b = candidate
				break
			}
		}
	}
	if len(r.Brains) > 1 {
		if err := b.Available(); err != nil {
			return Brain{}, err
		}
	}
	return b, nil
}

func ResolveDataDirFor(cwd string) (string, error) {
	b, err := Resolve(cwd)
	return b.Data, err
}

func (b Brain) Available() error {
	st, err := os.Stat(filepath.Join(b.Data, ".git"))
	if err != nil || !st.IsDir() {
		return fmt.Errorf("brain %q is unavailable at %s; restore its checkout before retrying", b.Name, b.Data)
	}
	return nil
}

func MultipleBrains() bool {
	r, err := Catalog()
	return err != nil || len(r.Brains) > 1
}

func canonicalPath(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return filepath.Clean(p)
}

func containsPath(parent, child string) bool {
	return parent == child || strings.HasPrefix(child, parent+string(filepath.Separator))
}

func InAnyBrain(cwd string) bool {
	r, err := Catalog()
	if err != nil {
		return true
	}
	p, _ := filepath.Abs(cwd)
	for _, b := range r.Brains {
		if containsPath(b.Data, canonicalPath(p)) {
			return true
		}
	}
	return false
}

func RegisterBrain(name, data string) error {
	f, err := registration(name, data)
	if err != nil {
		return err
	}
	return save(f)
}

func ValidateRegistration(name, data string) error {
	_, err := registration(name, data)
	return err
}

func registration(name, data string) (File, error) {
	f, err := loadStrict()
	if err != nil {
		return f, err
	}
	if name == "default" || !brainName.MatchString(name) {
		return f, fmt.Errorf("invalid or reserved brain name %q", name)
	}
	if _, exists := f.Brains[name]; exists {
		return f, fmt.Errorf("brain %q already exists", name)
	}
	if f.Brains == nil {
		f.Brains = map[string]string{}
	}
	if len(f.Brains) == 0 && os.Getenv("DEVBRAIN_DATA") != "" {
		f.Data = os.Getenv("DEVBRAIN_DATA")
	}
	f.Brains[name] = data
	if _, err := catalog(f); err != nil {
		return f, err
	}
	return f, nil
}

func AssignBrain(project, name string) error {
	f, err := loadStrict()
	if err != nil {
		return err
	}
	if f.ProjectBrains == nil {
		f.ProjectBrains = map[string]string{}
	}
	f.ProjectBrains[project] = name
	if _, err := catalog(f); err != nil {
		return err
	}
	return save(f)
}

func SetDefaultBrain(name string) error {
	f, err := loadStrict()
	if err != nil {
		return err
	}
	f.DefaultBrain = name
	if _, err := catalog(f); err != nil {
		return err
	}
	return save(f)
}

func DataID(data string) string {
	sum := sha256.Sum256([]byte(canonicalPath(data)))
	return fmt.Sprintf("%x", sum[:8])
}
