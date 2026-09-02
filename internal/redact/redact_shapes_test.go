package redact

import "testing"

// Shapes that reached the raw prompt logs in cleartext on 2026-09-02 because
// only vendor-prefixed tokens and NAME=value lines were redacted. Values are
// fabricated; the shapes are real.
func TestRedactLabelStyleHandoffs(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, in, want string }{
		{"prose api key with hex", "Semrush api key: 0123456789abcdef0123456789abcdef", "Semrush api key: [REDACTED]"},
		{"tabbed API Key", "API Key:\tpk1_abcdefghijklmnopqrstuvwxyz0123456789ab", "API Key:\t[REDACTED]"},
		{"tabbed Secret Key", "Secret Key:\tsk1_0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "Secret Key:\t[REDACTED]"},
		{"password", "password: hunter2secret", "password: [REDACTED]"},
		{"passcode", "Passcode: eEwG3fP#", "Passcode: [REDACTED]"},
		{"token with colon", "token: abcdef123456", "token: [REDACTED]"},
		{"inline equals left to the env rule (golden contract)", "in-prose api_key=notredacted123456 stays", "in-prose api_key=notredacted123456 stays"},
		{"client secret", "client secret: 9f8e7d6c5b4a3210", "client secret: [REDACTED]"},
		{"placeholder value kept", "API key: <your-key-here>", "API key: <your-key-here>"},
		{"already redacted kept", "api key: [REDACTED]", "api key: [REDACTED]"},
		{"short prose value kept", "The password: field is required", "The password: field is required"},
		{"rate wording kept", "Token bucket: 10 per second", "Token bucket: 10 per second"},
		{"variable reference kept", "token: ${SEMRUSH_TOKEN}", "token: ${SEMRUSH_TOKEN}"},
		{"plain word after label kept", "Use the token: authentication", "Use the token: authentication"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := Redact(c.in); got != c.want {
				t.Errorf("Redact(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestRedactBareVendorShapes(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, in, want string }{
		{"porkbun api key", "use pk1_abcdefghijklmnopqrstuvwxyz0123456789ab now", "use [REDACTED] now"},
		{"porkbun secret key", "use sk1_0123456789abcdef0123456789abcdef01234567 now", "use [REDACTED] now"},
		{"apify token", "token apify_api_NHdQhbY7pbPaxz6nHIdzGSo8xSn4ZRabcd", "token [REDACTED]"},
		{"supabase secret", "sb_secret_abcdefghijklmnopqrstuvwxyz012345", "[REDACTED]"},
		{"supabase publishable", "sb_publishable_abcdefghijklmnopqrstuvwxyz012345", "[REDACTED]"},
		{"jwt", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abcDEF123_-xyzABCdef456", "[REDACTED]"},
		{"jwt-like prefix in prose kept", "eyJ is how every JWT starts", "eyJ is how every JWT starts"},
		{"git sha kept", "commit 0fe7bac3f2a1d4e5b6c7d8e9f0a1b2c3d4e5f6a7", "commit 0fe7bac3f2a1d4e5b6c7d8e9f0a1b2c3d4e5f6a7"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := Redact(c.in); got != c.want {
				t.Errorf("Redact(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
