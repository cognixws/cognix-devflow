// Package agentsmd keeps the Dev flow block in a project's AGENTS.md: the
// few lines that tell every agent CLI how work flows here. The block's
// begin marker carries its version and the hash of its body as written, so
// the flow can tell a block it wrote (and may replace with a newer one) from
// a block a person edited (which it never overwrites unless asked to).
//
//	<!-- picode-devflow:begin v2 sha256=<12 hex> -->
//	…body…
//	<!-- picode-devflow:end -->
//
// A person who keeps a block of their own marks it
// `<!-- picode-devflow:begin custom -->`; the flow then leaves it alone.
package agentsmd

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strconv"
	"strings"
)

// File is where the block lives, at the repository root.
const File = "AGENTS.md"

// Version is the block this Dev flow writes.
const Version = 2

// Body is the current block's text, between the markers.
const Body = `## How work flows here (Dev flow)

- Start with the Dev flow status tool: it says where your change stands and what is next.
- One worktree per agent; never commit on the main branch in the main checkout.
- While you work, the Dev flow check tool runs the checks your change needs.
- At the end, call close and do what it lists as owed; then land.
- Stage explicit paths, never ` + "`git add -A`" + `; never merge by hand.
- A change that crosses a boundary (protocol, persistence, security model, process) gets a decision record (adr tool).
- Debts go to docs/handoff/open/<topic>.md with the debt tool; pay them by marking, never by deleting.
`

// bodyV1 is the block dev-flow-setup wrote before the marker carried a hash;
// a v1 block with exactly this body is the flow's own and may be replaced.
const bodyV1 = `## How work flows here (Dev flow)

- One worktree per agent; never commit on the main branch in the main checkout.
- While you work, the Dev flow check tool runs the checks your change needs.
- At the end, call close and do what it lists as owed; then land.
- Stage explicit paths, never ` + "`git add -A`" + `; never merge by hand.
- A change that crosses a boundary (protocol, persistence, security model, process) gets a decision record (adr tool).
- Debts go to docs/handoff/open/<topic>.md with the debt tool; pay them by marking, never by deleting.
`

// States of the block.
const (
	Missing = "missing" // no AGENTS.md, or no block in it
	Current = "current" // the flow's block, this version, untouched
	Older   = "older"   // the flow's block, an older version, untouched
	Edited  = "edited"  // the body no longer matches what the flow wrote (or a v1 block that differs)
	Custom  = "custom"  // the person keeps a block of their own
	Broken  = "broken"  // a begin marker without its end, or two blocks
)

const endMarker = "<!-- picode-devflow:end -->"

var beginRe = regexp.MustCompile(`(?m)^<!-- picode-devflow:begin ([^>]*?)\s*-->[ \t]*\r?\n`)

// Status is what Inspect found.
type Status struct {
	State   string `json:"state"`
	Version int    `json:"version,omitempty"` // the block's, when it says
	Latest  int    `json:"latest"`
}

// Hash is the short hash a marker carries for a body.
func Hash(body string) string {
	sum := sha256.Sum256([]byte(normalize(body)))
	return hex.EncodeToString(sum[:])[:12]
}

// Block is the full current block, markers included.
func Block() string {
	return "<!-- picode-devflow:begin v" + strconv.Itoa(Version) + " sha256=" + Hash(Body) + " -->\n" + Body + endMarker + "\n"
}

type found struct {
	start, end int // byte range of the whole block, end marker line included
	marker     string
	body       string
}

func locate(content string) (found, int, error) {
	ms := beginRe.FindAllStringSubmatchIndex(content, -1)
	if len(ms) == 0 {
		return found{}, 0, nil
	}
	if len(ms) > 1 {
		return found{}, len(ms), errors.New("two Dev flow blocks")
	}
	m := ms[0]
	rest := content[m[1]:]
	i := strings.Index(rest, endMarker)
	if i < 0 {
		return found{}, 1, errors.New("a begin marker without its end")
	}
	end := m[1] + i + len(endMarker)
	if strings.HasPrefix(content[end:], "\r\n") {
		end += 2
	} else if strings.HasPrefix(content[end:], "\n") {
		end++
	}
	return found{start: m[0], end: end, marker: content[m[2]:m[3]], body: rest[:i]}, 1, nil
}

// Inspect reads an AGENTS.md's content ("" when the file is absent).
func Inspect(content string) Status {
	st := Status{Latest: Version}
	f, n, err := locate(content)
	if err != nil {
		st.State = Broken
		return st
	}
	if n == 0 {
		st.State = Missing
		return st
	}
	fields := strings.Fields(f.marker)
	if len(fields) > 0 && fields[0] == "custom" {
		st.State = Custom
		return st
	}
	if len(fields) > 0 && strings.HasPrefix(fields[0], "v") {
		st.Version, _ = strconv.Atoi(fields[0][1:])
	}
	hash := ""
	for _, x := range fields[1:] {
		if strings.HasPrefix(x, "sha256=") {
			hash = strings.TrimPrefix(x, "sha256=")
		}
	}
	untouched := (hash != "" && hash == Hash(f.body)) || (hash == "" && st.Version == 1 && normalize(f.body) == normalize(bodyV1))
	switch {
	case !untouched:
		st.State = Edited
	case st.Version < Version:
		st.State = Older
	default:
		st.State = Current
	}
	return st
}

// ErrEdited refuses to overwrite a block a person changed or keeps.
var ErrEdited = errors.New("the Dev flow block in AGENTS.md was edited by hand; pass force to replace it with the current one")

// Apply answers the content with the current block in place: appended when
// missing, replaced when older, and, only with force, replaced when edited,
// custom or broken. changed is false when the content already holds it.
func Apply(content string, force bool) (out string, changed bool, err error) {
	st := Inspect(content)
	switch st.State {
	case Current:
		return content, false, nil
	case Missing:
		sep := ""
		if content != "" {
			sep = "\n"
			if !strings.HasSuffix(content, "\n") {
				sep = "\n\n"
			}
		}
		return content + sep + Block(), true, nil
	case Edited, Custom, Broken:
		if !force {
			return content, false, ErrEdited
		}
		if st.State == Broken {
			return content, false, errors.New("AGENTS.md has a broken Dev flow block (" + brokenWhy(content) + "); fix the markers by hand")
		}
	}
	f, _, _ := locate(content)
	return content[:f.start] + Block() + content[f.end:], true, nil
}

func brokenWhy(content string) string {
	if _, _, err := locate(content); err != nil {
		return err.Error()
	}
	return "unknown"
}

// normalize ignores line endings and trailing blank space, so a checkout
// with CRLF or an editor that trims the last newline still reads as written.
func normalize(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}
