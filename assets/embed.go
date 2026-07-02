// Package assets embeds the user-facing skill bodies so `devbrain install`
// can extract them without a repo checkout. It lives beside the skills tree
// because go:embed cannot reference files outside the package directory;
// internal/install consumes assets.Skills.
package assets

import "embed"

// Skills is the embedded skills/ tree (skills/<name>/SKILL.md …).
//
//go:embed all:skills
var Skills embed.FS
