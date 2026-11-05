// Package skills embeds the Dev flow's craft skills so the guide tool can
// hand their text to agent CLIs that cannot load skills at launch.
package skills

import "embed"

// FS holds every <name>/SKILL.md.
//
//go:embed */SKILL.md
var FS embed.FS
