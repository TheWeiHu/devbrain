// Package assets embeds the dashboard UI served by `devbrain queue`.
package assets

import _ "embed"

// DashboardHTML is served byte-identical at / and /index.html.
//
//go:embed dashboard.html
var DashboardHTML []byte
