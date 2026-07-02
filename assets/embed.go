// Package assets embeds the artifacts the binary ships: the dashboard UI
// served by `devbrain queue` and the skill bodies `devbrain install`
// extracts. It lives beside the files because go:embed cannot reference
// paths outside the package directory.
package assets

import "embed"

// DashboardHTML is served byte-identical at / and /index.html.
//
//go:embed dashboard.html
var DashboardHTML []byte

// Skills is the embedded skills tree (skills/<name>/SKILL.md …).
//
//go:embed all:skills
var Skills embed.FS
