// Package contract reads the project's binding of the Dev flow verbs to its
// stack: the "extensions" → "picode-devflow" section of .picode.json
// (ADR-0401 in the COGNIX[WS] repository). Unknown fields are refused, never
// ignored: a typo in a gate must not silently drop the gate.
package contract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/cognixws/picode-devflow/internal/glob"
)

// ID is the extension id the section is keyed by.
const ID = "picode-devflow"

// File is the project file that carries the section.
const File = ".picode.json"

// ErrNoContract means the project declares no Dev flow section.
var ErrNoContract = errors.New("this project has no Dev flow contract: add extensions.picode-devflow to .picode.json")

// Check is one named command and the paths that make it run.
type Check struct {
	Run        string   `json:"run"`
	When       []string `json:"when,omitempty"`
	Affected   string   `json:"affected,omitempty"`
	JUnit      string   `json:"junit,omitempty"`
	Only       string   `json:"only,omitempty"`
	TimeoutMin int      `json:"timeoutMin,omitempty"`
}

// FullOnly reports whether the check runs only with the full gate.
func (c Check) FullOnly() bool { return c.Only == "full" }

// Rule is either "touching When needs one of Need" or "Refuse these paths
// unless they match Unless". Why is the sentence a refusal shows.
type Rule struct {
	When   []string `json:"when,omitempty"`
	Need   []string `json:"need,omitempty"`
	Refuse []string `json:"refuse,omitempty"`
	Unless []string `json:"unless,omitempty"`
	Why    string   `json:"why"`
}

// Generated is a command whose output must be committed.
type Generated struct {
	Run   string   `json:"run"`
	Files []string `json:"files"`
}

// Docs names the project's documentation folders.
type Docs struct {
	Notes     string `json:"notes,omitempty"`
	Topics    string `json:"topics,omitempty"`
	Changelog string `json:"changelog,omitempty"`
	Decisions string `json:"decisions,omitempty"`
	Books     string `json:"books,omitempty"`
}

// Contract is the parsed section.
type Contract struct {
	Flow         int              `json:"flow"`
	Main         string           `json:"main,omitempty"`
	Checks       map[string]Check `json:"checks,omitempty"`
	Full         string           `json:"full,omitempty"`
	Everything   []string         `json:"everything,omitempty"`
	Rules        []Rule           `json:"rules,omitempty"`
	Generated    []Generated      `json:"generated,omitempty"`
	Docs         Docs             `json:"docs,omitempty"`
	Modules      []string         `json:"modules,omitempty"`
	Worktrees    string           `json:"worktrees,omitempty"`
	BranchPrefix string           `json:"branchPrefix,omitempty"`
	NoteMaxLines int              `json:"noteMaxLines,omitempty"`
	Quarantine   string           `json:"quarantine,omitempty"`
	// Land is the project's own land command, run from the main checkout
	// with {branch} filled in. Without it, changes land through Merge.
	Land string `json:"land,omitempty"`
	// UI names the paths whose change owes a UI review (module ui-review);
	// empty means the usual front-end files.
	UI []string `json:"ui,omitempty"`
	// Queue is how the person sees and orders the project's own land queue
	// (the one Land feeds): absent, the Dev flow shows its own lands only.
	Queue *Queue `json:"queue,omitempty"`
	// Failure declares the project's layers and the boundary each keeps
	// (the failure-review gate): absent, nothing about it changes.
	Failure *Failure `json:"failure,omitempty"`

	digest string
}

// Failure is the failure-review gate: a change to a layer's code owes, in
// the handoff note, a "failure-review: <layer>" line backed by a changed
// test of that layer, or an n/a with its reason.
type Failure struct {
	Layers []Layer `json:"layers"`
}

// Layer is one part of the application and the boundary that contains its
// failures. Paths are its code, Tests the files that prove the boundary,
// Check an optional command (a ratchet) run when the layer is touched.
type Layer struct {
	Name     string   `json:"name"`
	Paths    []string `json:"paths"`
	Boundary string   `json:"boundary"`
	Tests    []string `json:"tests"`
	Check    string   `json:"check,omitempty"`
}

// FailureCheckPrefix names the check a layer's Check becomes.
const FailureCheckPrefix = "failure-"

// Queue binds the project's land queue: List prints it in order, Move and
// Drop change one ticket. Commands run from the main checkout through
// /bin/sh; {ticket} is a ticket List printed, {move} becomes --front,
// --before <ticket> or --after <ticket>. Red names a file whose presence
// means the target's own checks failed and the queue waits.
type Queue struct {
	List string `json:"list"`
	Move string `json:"move,omitempty"`
	Drop string `json:"drop,omitempty"`
	Red  string `json:"red,omitempty"`
}

// Modules the engine knows.
var knownModules = map[string]bool{"handoff": true, "changelog": true, "decisions": true, "books": true, "ui-review": true}

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// Load reads the section from root/.picode.json.
func Load(root string) (*Contract, error) {
	b, err := os.ReadFile(filepath.Join(root, File))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoContract
	}
	if err != nil {
		return nil, err
	}
	return FromProjectFile(b)
}

// FromProjectFile extracts and parses the section from a whole .picode.json.
func FromProjectFile(b []byte) (*Contract, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(b, &top); err != nil {
		return nil, fmt.Errorf("%s: %w", File, err)
	}
	raw, ok := top["extensions"]
	if !ok {
		return nil, ErrNoContract
	}
	var exts map[string]json.RawMessage
	if err := json.Unmarshal(raw, &exts); err != nil {
		return nil, fmt.Errorf("%s: extensions: %w", File, err)
	}
	sec, ok := exts[ID]
	if !ok {
		return nil, ErrNoContract
	}
	return Parse(sec)
}

// Parse parses and validates one section.
func Parse(section []byte) (*Contract, error) {
	dec := json.NewDecoder(bytes.NewReader(section))
	dec.DisallowUnknownFields()
	var c Contract
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("extensions.%s: %w", ID, err)
	}
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("extensions.%s: %w", ID, err)
	}
	c.defaults()
	var compact bytes.Buffer
	if err := json.Compact(&compact, section); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(compact.Bytes())
	c.digest = hex.EncodeToString(sum[:])
	return &c, nil
}

// Digest is the SHA-256 of the section's compact JSON.
func (c *Contract) Digest() string { return c.digest }

// CheckNames lists the checks in name order.
func (c *Contract) CheckNames() []string {
	names := make([]string, 0, len(c.Checks))
	for n := range c.Checks {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// HasModule reports whether a module is on.
func (c *Contract) HasModule(m string) bool {
	for _, x := range c.Modules {
		if x == m {
			return true
		}
	}
	return false
}

func (c *Contract) defaults() {
	if c.Main == "" {
		c.Main = "main"
	}
	if c.Worktrees == "" {
		c.Worktrees = ".worktrees"
	}
	if c.BranchPrefix == "" {
		c.BranchPrefix = "feat/"
	}
	if c.NoteMaxLines == 0 {
		c.NoteMaxLines = 25
	}
	d := &c.Docs
	def := func(s *string, v string) {
		if *s == "" {
			*s = v
		}
	}
	def(&d.Notes, "docs/handoff")
	def(&d.Topics, "docs/handoff/open")
	def(&d.Changelog, "docs/changelog.d")
	def(&d.Decisions, "docs/decisions")
	def(&d.Books, "docs/books")
	// A layer's check is one more check, selected when its paths change.
	if c.Failure != nil {
		for _, l := range c.Failure.Layers {
			if l.Check == "" {
				continue
			}
			if c.Checks == nil {
				c.Checks = map[string]Check{}
			}
			c.Checks[FailureCheckPrefix+l.Name] = Check{Run: l.Check, When: append([]string(nil), l.Paths...)}
		}
	}
}

func oneLine(s string) bool { return strings.TrimSpace(s) != "" && !strings.ContainsAny(s, "\r\n") }

func globs(field string, ps []string) error {
	for _, p := range ps {
		if !glob.Valid(p) {
			return fmt.Errorf("%s: %q is not a valid path pattern (relative, ** only as a whole segment)", field, p)
		}
	}
	return nil
}

func (c *Contract) validate() error {
	if c.Flow != 1 {
		return fmt.Errorf("flow must be 1 (got %d); a newer Dev flow is needed for other versions", c.Flow)
	}
	if len(c.Checks) == 0 && c.Full == "" {
		// A project with no stack yet: process validators only. Valid.
	}
	for name, ch := range c.Checks {
		if !namePattern.MatchString(name) {
			return fmt.Errorf("checks: %q is not a valid name (a-z, 0-9, dashes, at most 40)", name)
		}
		if !oneLine(ch.Run) {
			return fmt.Errorf("checks.%s.run must be one non-empty line", name)
		}
		if ch.Only != "" && ch.Only != "full" {
			return fmt.Errorf("checks.%s.only must be \"full\" or absent", name)
		}
		if ch.Affected != "" && !oneLine(ch.Affected) {
			return fmt.Errorf("checks.%s.affected must be one line", name)
		}
		if ch.TimeoutMin < 0 || ch.TimeoutMin > 240 {
			return fmt.Errorf("checks.%s.timeoutMin must be between 1 and 240", name)
		}
		if err := globs("checks."+name+".when", ch.When); err != nil {
			return err
		}
	}
	if c.Land != "" && (!oneLine(c.Land) || !strings.Contains(c.Land, "{branch}")) {
		return errors.New("land must be one line naming {branch}")
	}
	if q := c.Queue; q != nil {
		if !oneLine(q.List) {
			return errors.New("queue.list must be one non-empty line")
		}
		if q.Move != "" && (!oneLine(q.Move) || !strings.Contains(q.Move, "{ticket}") || !strings.Contains(q.Move, "{move}")) {
			return errors.New("queue.move must be one line naming {ticket} and {move}")
		}
		if q.Drop != "" && (!oneLine(q.Drop) || !strings.Contains(q.Drop, "{ticket}")) {
			return errors.New("queue.drop must be one line naming {ticket}")
		}
		if q.Red != "" && (!oneLine(q.Red) || filepath.IsAbs(q.Red) || strings.Contains(q.Red, "..")) {
			return errors.New("queue.red must be a path relative to the repository")
		}
	}
	if c.Full != "" && !oneLine(c.Full) {
		return errors.New("full must be one line")
	}
	if err := globs("everything", c.Everything); err != nil {
		return err
	}
	for i, r := range c.Rules {
		at := fmt.Sprintf("rules[%d]", i)
		if strings.TrimSpace(r.Why) == "" {
			return fmt.Errorf("%s.why is required: it is the sentence a refusal shows", at)
		}
		need, refuse := len(r.Need) > 0, len(r.Refuse) > 0
		if need == refuse {
			return fmt.Errorf("%s needs exactly one of need or refuse", at)
		}
		if need && len(r.When) == 0 {
			return fmt.Errorf("%s: need requires when", at)
		}
		for _, f := range []struct {
			n string
			p []string
		}{{"when", r.When}, {"need", r.Need}, {"refuse", r.Refuse}, {"unless", r.Unless}} {
			if err := globs(at+"."+f.n, f.p); err != nil {
				return err
			}
		}
	}
	for i, g := range c.Generated {
		if !oneLine(g.Run) || len(g.Files) == 0 {
			return fmt.Errorf("generated[%d] needs run and files", i)
		}
		if err := globs(fmt.Sprintf("generated[%d].files", i), g.Files); err != nil {
			return err
		}
	}
	for _, m := range c.Modules {
		if !knownModules[m] {
			return fmt.Errorf("modules: %q is unknown (handoff, changelog, decisions, books, ui-review)", m)
		}
	}
	if c.HasModule("ui-review") && !c.HasModule("handoff") {
		return errors.New("modules: ui-review records its verdict in the handoff note, so it needs handoff too")
	}
	if err := globs("ui", c.UI); err != nil {
		return err
	}
	if c.NoteMaxLines < 0 {
		return errors.New("noteMaxLines must be positive")
	}
	if f := c.Failure; f != nil {
		if !c.HasModule("handoff") {
			return errors.New("failure: its review lines live in the handoff note, so it needs the handoff module")
		}
		if len(f.Layers) == 0 {
			return errors.New("failure.layers must name at least one layer")
		}
		seen := map[string]bool{}
		for i, l := range f.Layers {
			at := fmt.Sprintf("failure.layers[%d]", i)
			if !namePattern.MatchString(l.Name) || len(l.Name) > 32 {
				return fmt.Errorf("%s.name: %q is not a valid name (a-z, 0-9, dashes, at most 32)", at, l.Name)
			}
			if seen[l.Name] {
				return fmt.Errorf("%s.name: %q appears twice", at, l.Name)
			}
			seen[l.Name] = true
			if len(l.Paths) == 0 || len(l.Tests) == 0 {
				return fmt.Errorf("%s needs paths and tests", at)
			}
			if !oneLine(l.Boundary) {
				return fmt.Errorf("%s.boundary must be one non-empty line: the boundary this layer keeps", at)
			}
			if err := globs(at+".paths", l.Paths); err != nil {
				return err
			}
			if err := globs(at+".tests", l.Tests); err != nil {
				return err
			}
			if l.Check != "" {
				if !oneLine(l.Check) {
					return fmt.Errorf("%s.check must be one line", at)
				}
				if _, clash := c.Checks[FailureCheckPrefix+l.Name]; clash {
					return fmt.Errorf("%s.check becomes the check %q, which checks already names", at, FailureCheckPrefix+l.Name)
				}
			}
		}
	}
	return nil
}
