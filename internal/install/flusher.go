package install

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// plistTemplate is the launchd flusher job. No sed placeholders — the binary
// path and log file are formatted in directly; the data dir comes from
// ~/.config/devbrain/config.json (written by install), not a pinned env var.
// Fields: %s binary, %s extra env entries, %s log, %s log.
const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.devbrain.flush</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>flush</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key>
    <string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>%s
  </dict>
  <key>StartInterval</key>
  <integer>60</integer>
  <key>RunAtLoad</key>
  <true/>
  <key>StandardOutPath</key>
  <string>%s</string>
  <key>StandardErrorPath</key>
  <string>%s</string>
</dict>
</plist>
`

func (c *ctx) plistPath() string {
	return filepath.Join(c.home, "Library", "LaunchAgents", "com.devbrain.flush.plist")
}

// wireFlusher installs the one-minute flusher (sweep + commit + push):
// launchd on macOS, a systemd user timer on Linux (falling back to cron, then
// a manual note). One minute is capture's freshness ceiling; an idle tick
// exits in milliseconds (sweep cursor + clean worktree). Best-effort — a
// missing scheduler never fails the install.
func (c *ctx) wireFlusher() {
	if c.goos == "darwin" {
		logf := filepath.Join(c.home, "Library", "Logs", "devbrain-flush.log")
		plist := c.plistPath()
		_ = os.MkdirAll(filepath.Dir(plist), 0o755)
		_ = os.MkdirAll(filepath.Dir(logf), 0o755)
		_ = run("launchctl", "unload", plist)
		if err := os.WriteFile(plist, fmt.Appendf(nil, plistTemplate, c.bin, plistExtraEnv(), logf, logf), 0o644); err != nil {
			fmt.Fprintf(c.stderr, "  flusher: cannot write %s: %v\n", plist, err)
			return
		}
		if run("launchctl", "load", plist) == nil {
			fmt.Fprintf(c.stdout, "  loaded flusher LaunchAgent (every minute) -> %s\n", plist)
		} else {
			fmt.Fprintf(c.stdout, "  wrote flusher LaunchAgent -> %s (launchctl load failed — load it yourself)\n", plist)
		}
		return
	}

	// Linux ladder: systemd user timer -> cron -> note.
	_ = run("loginctl", "enable-linger", os.Getenv("USER"))
	if haveCmd("systemctl") && run("systemctl", "--user", "show-environment") == nil {
		sd := filepath.Join(c.home, ".config", "systemd", "user")
		_ = os.MkdirAll(sd, 0o755)
		service := "[Unit]\nDescription=devbrain flush — commit+push the prompt-log data repo\n" +
			"[Service]\nType=oneshot\n" + systemdExtraEnv() + "ExecStart=" + c.bin + " flush\n"
		timer := "[Unit]\nDescription=devbrain flush every minute\n" +
			"[Timer]\nOnBootSec=2min\nOnUnitActiveSec=1min\nPersistent=true\n" +
			"[Install]\nWantedBy=timers.target\n"
		_ = os.WriteFile(filepath.Join(sd, "devbrain-flush.service"), []byte(service), 0o644)
		_ = os.WriteFile(filepath.Join(sd, "devbrain-flush.timer"), []byte(timer), 0o644)
		if run("systemctl", "--user", "daemon-reload") == nil &&
			run("systemctl", "--user", "enable", "--now", "devbrain-flush.timer") == nil {
			fmt.Fprintln(c.stdout, "  enabled systemd user timer (every minute) -> devbrain-flush.timer")
			return
		}
	}
	if haveCmd("crontab") && c.installCron() {
		fmt.Fprintln(c.stdout, "  installed cron entry (every minute) -> devbrain flush")
		return
	}
	fmt.Fprintf(c.stdout, "  NOTE: no systemd --user timer or cron available — run '%s flush' on your own schedule to auto-flush\n", c.bin)
}

// installCron rewrites the crontab with our flush line (replacing any prior
// devbrain flush entry).
func (c *ctx) installCron() bool {
	existing, _ := exec.Command("crontab", "-l").Output() // empty crontab -> error, fine
	var kept []string
	for _, l := range strings.Split(string(existing), "\n") {
		if l == "" || isFlushCronLine(l) {
			continue
		}
		kept = append(kept, l)
	}
	kept = append(kept, fmt.Sprintf("* * * * * %s%s flush >/dev/null 2>&1", cronExtraEnv(), c.bin))
	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(strings.Join(kept, "\n") + "\n")
	return cmd.Run() == nil
}

// isFlushCronLine matches both the legacy copy (devbrain-flush.sh) and the
// binary form (<path> flush).
func isFlushCronLine(l string) bool {
	return strings.Contains(l, "devbrain-flush.sh") ||
		(strings.Contains(l, "devbrain") && strings.Contains(l, " flush "))
}

// removeFlusher reverses wireFlusher (used by uninstall).
func (c *ctx) removeFlusher() {
	if c.goos == "darwin" {
		plist := c.plistPath()
		if exists(plist) {
			_ = run("launchctl", "unload", plist)
			_ = os.Remove(plist)
			fmt.Fprintln(c.stdout, "removed flusher LaunchAgent")
		}
		return
	}
	if haveCmd("systemctl") {
		_ = run("systemctl", "--user", "disable", "--now", "devbrain-flush.timer")
		sd := filepath.Join(c.home, ".config", "systemd", "user")
		removed := false
		for _, f := range []string{"devbrain-flush.timer", "devbrain-flush.service"} {
			if os.Remove(filepath.Join(sd, f)) == nil {
				removed = true
			}
		}
		_ = run("systemctl", "--user", "daemon-reload")
		if removed {
			fmt.Fprintln(c.stdout, "removed systemd flush timer")
		}
	}
	if haveCmd("crontab") {
		existing, err := exec.Command("crontab", "-l").Output()
		if err != nil || !strings.Contains(string(existing), "devbrain") {
			return
		}
		var kept []string
		for _, l := range strings.Split(strings.TrimRight(string(existing), "\n"), "\n") {
			if isFlushCronLine(l) {
				continue
			}
			kept = append(kept, l)
		}
		cmd := exec.Command("crontab", "-")
		cmd.Stdin = strings.NewReader(strings.Join(kept, "\n") + "\n")
		_ = cmd.Run()
	}
}

// The scheduled flusher runs outside the user's shell, so a custom CODEX_HOME
// set at install time must be baked into the job or the sweep watches the
// wrong Codex sessions tree (~/.codex) forever.
func plistExtraEnv() string {
	if ch := os.Getenv("CODEX_HOME"); ch != "" {
		return "\n    <key>CODEX_HOME</key>\n    <string>" + ch + "</string>"
	}
	return ""
}

func systemdExtraEnv() string {
	if ch := os.Getenv("CODEX_HOME"); ch != "" {
		return "Environment=CODEX_HOME=" + ch + "\n"
	}
	return ""
}

func cronExtraEnv() string {
	if ch := os.Getenv("CODEX_HOME"); ch != "" {
		return "CODEX_HOME=" + ch + " "
	}
	return ""
}
