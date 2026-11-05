package scope

import (
	"strings"
	"testing"

	"github.com/cognixws/picode-devflow/internal/contract"
)

func mustParse(t *testing.T, s string) *contract.Contract {
	t.Helper()
	c, err := contract.Parse([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSelect(t *testing.T) {
	c := mustParse(t, `{"flow":1,"full":"make ci","everything":["go.mod"],
	 "checks":{"go":{"run":"go test ./...","when":["**/*.go"]},
	           "web":{"run":"npm test","when":["web/**"]},
	           "lint":{"run":"make lint"},
	           "e2e":{"run":"npm run e2e","only":"full"}}}`)
	cases := []struct {
		name  string
		paths []string
		full  bool
		want  string
	}{
		{"empty is full", nil, true, "full"},
		{"go only", []string{"internal/a.go"}, false, "go,lint"},
		{"web and go", []string{"web/x.js", "cmd/m.go"}, false, "go,lint,web"},
		{"everything", []string{"go.mod"}, true, "full"},
		{"lint covers all", []string{"README.md"}, false, "lint"},
	}
	for _, tc := range cases {
		s := Select(c, tc.paths)
		if s.Full != tc.full || strings.Join(s.Checks, ",") != tc.want {
			t.Errorf("%s: full=%v checks=%v", tc.name, s.Full, s.Checks)
		}
	}
}

func TestUncoveredIsFull(t *testing.T) {
	c := mustParse(t, `{"flow":1,"checks":{"go":{"run":"x","when":["**/*.go"]},"e2e":{"run":"y","only":"full"}}}`)
	s := Select(c, []string{"a.go", "notes.txt"})
	if !s.Full || len(s.Uncovered) != 1 || s.Uncovered[0] != "notes.txt" {
		t.Fatalf("%+v", s)
	}
	if strings.Join(s.Checks, ",") != "e2e,go" {
		t.Fatalf("without a full command the gate is every check: %v", s.Checks)
	}
}
