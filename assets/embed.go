// Package assets embeds the artifacts the binary ships: the dashboard UI
// served by `devbrain dashboard` and the skill bodies `devbrain install`
// extracts. It lives beside the files because go:embed cannot reference
// paths outside the package directory.
package assets

import "embed"

// DashboardHTML is served byte-identical at / and /index.html; it links the
// sibling stylesheet and script it was split from.
//
//go:embed dashboard.html
var DashboardHTML []byte

// DashboardCSS and DashboardJS are the extracted styles/scripts, served
// byte-identical at /dashboard.css and /dashboard.js.
//
//go:embed dashboard.css
var DashboardCSS []byte

//go:embed dashboard.js
var DashboardJS []byte

// Skills is the embedded skills tree (skills/<name>/SKILL.md …).
//
//go:embed all:skills
var Skills embed.FS

// Prompts holds the nightshift worker prompts (drain rules + planning turn).
// A copy on disk beside the repo wins at runtime; this is the fallback.
//
//go:embed prompts
var Prompts embed.FS
