package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/cognixws/picode-devflow/internal/agentsmd"
	"github.com/cognixws/picode-devflow/internal/gitx"
)

type agentsOut struct {
	File    string `json:"file"`
	State   string `json:"state"`
	Version int    `json:"version,omitempty"`
	Latest  int    `json:"latest"`
	Written bool   `json:"written"`
	Block   string `json:"block,omitempty"` // the current block, when it is not in place
}

// cmdAgents reports the Dev flow block in AGENTS.md and, with --write,
// puts the current one in place; a block a person edited stays unless
// --force. It writes the file and never commits it.
func cmdAgents(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("agents", flag.ContinueOnError)
	write := fs.Bool("write", false, "put the current block in place")
	force := fs.Bool("force", false, "with --write: replace a block a person edited")
	asJSON := fs.Bool("json", false, "machine output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	r, err := gitx.Open(".")
	if err != nil {
		return err
	}
	path := filepath.Join(r.Root, agentsmd.File)
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	st := agentsmd.Inspect(string(b))
	out := agentsOut{File: agentsmd.File, State: st.State, Version: st.Version, Latest: st.Latest}
	var werr error
	if *write {
		next, changed, err := agentsmd.Apply(string(b), *force)
		switch {
		case err != nil:
			werr = err
		case changed:
			if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
				return err
			}
			out.Written = true
			out.State = agentsmd.Current
			out.Version = agentsmd.Version
		}
	}
	if out.State != agentsmd.Current && out.State != agentsmd.Custom {
		out.Block = agentsmd.Block()
	}
	text := map[string]string{
		agentsmd.Missing: "AGENTS.md has no Dev flow block. Run with write to add it.",
		agentsmd.Current: fmt.Sprintf("AGENTS.md holds the current Dev flow block (v%d).", agentsmd.Version),
		agentsmd.Older:   fmt.Sprintf("AGENTS.md holds an older Dev flow block (v%d; current v%d). Run with write to replace it.", st.Version, agentsmd.Version),
		agentsmd.Edited:  "The Dev flow block in AGENTS.md was edited by hand. The flow leaves it alone: mark it yours with `<!-- picode-devflow:begin custom -->`, or run with write and force to replace it.",
		agentsmd.Custom:  "AGENTS.md keeps a Dev flow block of its own (custom); the flow leaves it alone.",
		agentsmd.Broken:  "The Dev flow block markers in AGENTS.md are broken: leave one begin and one end marker.",
	}[out.State]
	if out.Written {
		text = fmt.Sprintf("Wrote the Dev flow block (v%d) in AGENTS.md. Commit it on your branch.", agentsmd.Version)
	}
	if err := emit(w, *asJSON, out, text); err != nil {
		return err
	}
	if !*asJSON && out.Block != "" && !out.Written {
		fmt.Fprintf(w, "\nThe current block:\n\n%s", out.Block)
	}
	if werr != nil {
		if !errors.Is(werr, agentsmd.ErrEdited) && !*asJSON {
			fmt.Fprintln(w, werr)
		}
		return exitError{1}
	}
	return nil
}
