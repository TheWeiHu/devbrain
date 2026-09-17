package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TheWeiHu/devbrain/internal/clitest"
)

func TestTapWritePreflight(t *testing.T) {
	for _, tc := range []struct {
		name      string
		checkOnly bool
		deny      bool
	}{
		{"valid token", true, false},
		{"read-only token", true, true},
		{"current formula still checks write access", false, true},
		{"current formula is idempotent", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			stub := `#!/bin/sh
printf '%s\n' "$*" >> "$GIT_CALLS"
case " $* " in
  *" clone "*)
    for target do :; done
    mkdir -p "$target/Formula"
    ;;
  *" push --dry-run "*)
    [ "$DENY_WRITE" = 0 ] || exit 128
    ;;
  *" push "*) exit 99 ;;
esac
`
			if err := os.WriteFile(filepath.Join(dir, "git"), []byte(stub), 0o755); err != nil {
				t.Fatal(err)
			}
			args := []string{filepath.Join(clitest.Root(t), "scripts", "brew-formula-push.sh"), "--check-auth"}
			if !tc.checkOnly {
				sums := filepath.Join(dir, "checksums.txt")
				var content strings.Builder
				for _, platform := range []string{"darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64"} {
					content.WriteString(strings.Repeat("a", 64) + "  devbrain_1.5.29_" + platform + ".tar.gz\n")
				}
				if err := os.WriteFile(sums, []byte(content.String()), 0o644); err != nil {
					t.Fatal(err)
				}
				args = []string{args[0], "1.5.29", sums}
			}
			deny := "0"
			if tc.deny {
				deny = "1"
			}
			calls := filepath.Join(dir, "calls")
			cmd := exec.Command("sh", args...)
			cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "GITHUB_TOKEN=fixture-secret", "GIT_CALLS="+calls, "DENY_WRITE="+deny, "BREW_PUSH_DRY=")
			out, err := cmd.CombinedOutput()
			if (err != nil) != tc.deny {
				t.Fatalf("exit = %v, denied = %v: %s", err, tc.deny, out)
			}
			log := mustRead(t, calls)
			if !strings.Contains(log, "push --dry-run origin HEAD") {
				t.Fatalf("write authentication was not exercised: %s", log)
			}
			if strings.Contains(log, "fixture-secret") || strings.Contains(string(out), "fixture-secret") {
				t.Fatal("credential appeared in git arguments or output")
			}
			if !tc.checkOnly && !tc.deny && !strings.Contains(string(out), "formula already current") {
				t.Fatalf("unchanged formula was not idempotent: %s", out)
			}
		})
	}
}
