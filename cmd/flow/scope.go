package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/cognixws/cognix-devflow/internal/contract"
	"github.com/cognixws/cognix-devflow/internal/scope"
)

// cmdScope prints the selection for a list of paths read from stdin (one per
// line), or, with --batch, one selection per input line holding a JSON array
// of paths. --contract reads a section file instead of the project's.
func cmdScope(args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("scope", flag.ContinueOnError)
	file := fs.String("contract", "", "a file holding only the extensions.devflow section")
	batch := fs.Bool("batch", false, "one JSON array of paths per line in, one selection per line out")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := loadContract(*file)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(stdout)
	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	if *batch {
		for sc.Scan() {
			var paths []string
			if err := json.Unmarshal(sc.Bytes(), &paths); err != nil {
				return fmt.Errorf("batch line: %w", err)
			}
			if err := enc.Encode(scope.Select(c, paths)); err != nil {
				return err
			}
		}
		return sc.Err()
	}
	var paths []string
	for sc.Scan() {
		if p := strings.TrimSpace(sc.Text()); p != "" {
			paths = append(paths, p)
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return enc.Encode(scope.Select(c, paths))
}

func loadContract(file string) (*contract.Contract, error) {
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		return contract.Parse(b)
	}
	root, err := repoRoot(".")
	if err != nil {
		return nil, err
	}
	c, err := contract.Load(root)
	if errors.Is(err, contract.ErrNoContract) {
		return nil, err
	}
	return c, err
}
