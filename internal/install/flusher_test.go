package install

import "testing"

// TestUserBusEnv covers the headless-box gap: `systemctl --user` needs
// XDG_RUNTIME_DIR / DBUS_SESSION_BUS_ADDRESS a fresh SSH shell doesn't set.
func TestUserBusEnv(t *testing.T) {
	has := func(env []string, want string) bool {
		for _, e := range env {
			if e == want {
				return true
			}
		}
		return false
	}

	// Nothing set: synthesize both from the uid.
	got := userBusEnv(1002, func(string) string { return "" })
	if len(got) != 2 {
		t.Fatalf("want 2 vars, got %v", got)
	}
	if !has(got, "XDG_RUNTIME_DIR=/run/user/1002") {
		t.Errorf("missing XDG_RUNTIME_DIR: %v", got)
	}
	if !has(got, "DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1002/bus") {
		t.Errorf("missing DBUS address: %v", got)
	}

	// XDG already set: reuse it for the bus path, don't re-emit XDG.
	got = userBusEnv(1002, func(k string) string {
		if k == "XDG_RUNTIME_DIR" {
			return "/custom/run"
		}
		return ""
	})
	if len(got) != 1 || !has(got, "DBUS_SESSION_BUS_ADDRESS=unix:path=/custom/run/bus") {
		t.Errorf("DBUS should reuse the existing XDG, XDG not re-emitted: %v", got)
	}

	// Both already set: nothing to add.
	if got := userBusEnv(1002, func(string) string { return "set" }); len(got) != 0 {
		t.Errorf("both set should yield no env, got %v", got)
	}
}
