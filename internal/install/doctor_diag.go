package install

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/TheWeiHu/devbrain/internal/diagnostics"
)

func doctorData(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("devbrain doctor data", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cwd := fs.String("cwd", "", "working directory to resolve into a project")
	project := fs.String("project", "", "explicit project key")
	dashboardURL := fs.String("dashboard-url", "", "dashboard URL carrying ?project=<key>")
	jsonOut := fs.Bool("json", false, "print JSON")
	fs.Usage = func() {
		fmt.Fprint(stderr, "usage: devbrain doctor data [--cwd PATH] [--project KEY|--dashboard-url URL] [--json]\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *project != "" && *dashboardURL != "" {
		fmt.Fprintln(stderr, "doctor data: use --project or --dashboard-url, not both")
		return 2
	}
	r := diagnostics.ReportData(diagnostics.DataOptions{CWD: *cwd, Project: *project, DashboardURL: *dashboardURL})
	if *jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(r)
	} else {
		renderDataReport(stdout, r)
	}
	if len(r.Failures) > 0 {
		return 1
	}
	return 0
}

func renderDataReport(w io.Writer, r diagnostics.DataReport) {
	home, _ := os.UserHomeDir()
	fmt.Fprintln(w, "devbrain doctor data — context routing")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  data dir:         %s\n", display(r.DataDir, home))
	fmt.Fprintf(w, "  cwd:              %s\n", display(r.CWD, home))
	fmt.Fprintf(w, "  cwd project:      %s\n", orDash(r.CWDProject))
	fmt.Fprintf(w, "  selected project: %s (%s)\n", orDash(r.SelectedProject), r.ProjectSource)
	if r.ProjectMismatch {
		fmt.Fprintln(w, "  project match:    WARN selected project differs from cwd project")
	} else {
		fmt.Fprintln(w, "  project match:    PASS")
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  raw logs:         %d file(s)", r.Raw.Count)
	if r.Raw.NewestEntry != "" {
		fmt.Fprintf(w, " — newest %s in %s", r.Raw.NewestEntry, r.Raw.NewestFile)
	}
	fmt.Fprintln(w)
	if r.Distill.LedgerExists {
		fmt.Fprintf(w, "  distill ledger:   PASS %s\n", display(r.Distill.LedgerPath, home))
	} else {
		fmt.Fprintf(w, "  distill ledger:   WARN missing at %s\n", display(r.Distill.LedgerPath, home))
	}
	fmt.Fprintf(w, "  pending distill:  %d file(s)\n", r.Distill.PendingCount)
	for _, p := range r.Distill.Pending {
		cursor := p.Cursor
		if cursor == "" {
			cursor = "START"
		}
		fmt.Fprintf(w, "    - %s -> %s %s (after %s)\n", p.RelPath, p.Day, p.Newest, cursor)
	}
	fmt.Fprintf(w, "  brain pages:      %d file(s)\n", r.Brain.Count)
	switch {
	case !r.GBrain.Available:
		fmt.Fprintf(w, "  gbrain:           WARN unavailable (%s)\n", r.GBrain.Error)
	case !r.GBrain.SourcesOK:
		fmt.Fprintf(w, "  gbrain:           WARN sources list failed (%s)\n", r.GBrain.Error)
	case !r.GBrain.SourceHasData:
		fmt.Fprintln(w, "  gbrain:           WARN sources list does not mention the devbrain data repo")
	default:
		sync := r.GBrain.LastSync
		if sync == "" {
			sync = "unknown sync time"
		}
		fmt.Fprintf(w, "  gbrain:           PASS source registered, last sync %s\n", sync)
	}
	fmt.Fprintln(w)
	for _, f := range r.Failures {
		fmt.Fprintf(w, "  FAIL %s\n", f)
	}
	for _, warn := range r.Warnings {
		fmt.Fprintf(w, "  WARN %s\n", warn)
	}
	fmt.Fprintf(w, "\nDiagnosis: %s\n", r.Diagnosis)
}

func doctorBrew(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("devbrain doctor brew", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", false, "print JSON")
	fs.Usage = func() {
		fmt.Fprint(stderr, "usage: devbrain doctor brew [--json]\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	r := diagnostics.ReportBrew()
	if *jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(r)
	} else {
		renderBrewReport(stdout, r)
	}
	if len(r.Failures) > 0 {
		return 1
	}
	return 0
}

func renderBrewReport(w io.Writer, r diagnostics.BrewReport) {
	home, _ := os.UserHomeDir()
	fmt.Fprintln(w, "devbrain doctor brew — Homebrew tap integrity")
	fmt.Fprintln(w)
	if !r.Available {
		fmt.Fprintln(w, "  brew:       SKIP not found on PATH")
		fmt.Fprintf(w, "\nDiagnosis: %s\n", r.Diagnosis)
		return
	}
	fmt.Fprintf(w, "  brew:       PASS %s\n", r.BrewPath)
	fmt.Fprintf(w, "  installed:  %s\n", orDash(r.Installed))
	fmt.Fprintf(w, "  tap repo:   %s\n", display(r.TapRepo, home))
	fmt.Fprintf(w, "  formula:    %s\n", display(r.FormulaPath, home))
	if r.TapConflicted {
		fmt.Fprintln(w, "  formula:    FAIL unresolved merge-conflict markers")
	} else if r.FormulaDirty {
		fmt.Fprintln(w, "  formula:    WARN local modifications")
	} else {
		fmt.Fprintln(w, "  formula:    PASS parseable")
	}
	if r.TapDirty && !r.FormulaDirty {
		fmt.Fprintln(w, "  tap state:  WARN local modifications outside Formula/devbrain.rb")
	}
	if r.InfoOK {
		fmt.Fprintln(w, "  brew info:  PASS")
	} else if len(r.Failures) > 0 {
		fmt.Fprintln(w, "  brew info:  FAIL")
	}
	fmt.Fprintln(w)
	for _, f := range r.Failures {
		fmt.Fprintf(w, "  FAIL %s\n", f)
	}
	for _, warn := range r.Warnings {
		fmt.Fprintf(w, "  WARN %s\n", warn)
	}
	if strings.TrimSpace(r.Remediation) != "" {
		fmt.Fprintf(w, "\nRemediation: %s\n", r.Remediation)
	}
	fmt.Fprintf(w, "\nDiagnosis: %s\n", r.Diagnosis)
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
