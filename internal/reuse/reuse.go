// Package reuse remembers which checks passed on which content, so close
// does not run again what already passed on the same files. A stamp is
// keyed by the check's name and command, the contract's digest and the
// content of the files its globs cover at a commit; it is written only for
// a clean checkout, so uncommitted edits never ride on an old pass.
package reuse

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Stamp is one recorded pass.
type Stamp struct {
	Key   string    `json:"key"`
	Check string    `json:"check"`
	Rev   string    `json:"rev"`
	At    time.Time `json:"at"`
}

// Store is a JSON-lines file under the repository's common git directory,
// shared by every worktree.
type Store struct{ path string }

// Open opens the store of a repository.
func Open(commonDir string) *Store {
	return &Store{path: filepath.Join(commonDir, "picode-devflow", "green.jsonl")}
}

// Key builds the stamp key.
func Key(check, command, contractDigest, contentKey string) string {
	h := sha256.New()
	for _, s := range []string{check, command, contractDigest, contentKey} {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Find returns the newest stamp with key.
func (s *Store) Find(key string) (Stamp, bool) {
	f, err := os.Open(s.path)
	if err != nil {
		return Stamp{}, false
	}
	defer f.Close()
	var hit Stamp
	found := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var st Stamp
		if json.Unmarshal(sc.Bytes(), &st) == nil && st.Key == key {
			hit, found = st, true
		}
	}
	return hit, found
}

// Record appends a stamp.
func (s *Store) Record(st Stamp) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, _ := json.Marshal(st)
	_, err = f.Write(append(b, '\n'))
	return err
}
