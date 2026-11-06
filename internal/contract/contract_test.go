package contract

import (
	"errors"
	"strings"
	"testing"
)

const good = `{"setup":["npm ci"],"extensions":{"picode-devflow":{
 "flow":1,
 "checks":{"test":{"run":"npm test","when":["src/**"],"junit":"var/junit.xml"},
           "e2e":{"run":"npm run e2e","only":"full"}},
 "full":"make ci",
 "everything":["package-lock.json"],
 "rules":[{"when":["db/**"],"need":["docs/db.md"],"why":"Schema changes are documented"},
          {"refuse":["**/*.xlsx"],"unless":["fixtures/**"],"why":"No client data"}],
 "modules":["handoff","changelog"]}}}`

func TestParseGood(t *testing.T) {
	c, err := FromProjectFile([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	if c.Main != "main" || c.Docs.Notes != "docs/handoff" || c.NoteMaxLines != 25 {
		t.Fatalf("defaults not applied: %+v", c)
	}
	if got := strings.Join(c.CheckNames(), ","); got != "e2e,test" {
		t.Fatalf("names %s", got)
	}
	if !c.Checks["e2e"].FullOnly() || len(c.Digest()) != 64 {
		t.Fatal("full-only or digest")
	}
}

func TestParseRefusals(t *testing.T) {
	cases := map[string]string{
		"unknown field":           `{"flow":1,"chekcs":{}}`,
		"wrong version":           `{"flow":2}`,
		"bad name":                `{"flow":1,"checks":{"Bad Name":{"run":"x"}}}`,
		"multiline run":           `{"flow":1,"checks":{"a":{"run":"x\ny"}}}`,
		"bad only":                `{"flow":1,"checks":{"a":{"run":"x","only":"sometimes"}}}`,
		"bad glob":                `{"flow":1,"checks":{"a":{"run":"x","when":["/abs"]}}}`,
		"rule without why":        `{"flow":1,"rules":[{"when":["a"],"need":["b"]}]}`,
		"rule both":               `{"flow":1,"rules":[{"when":["a"],"need":["b"],"refuse":["c"],"why":"x"}]}`,
		"need no when":            `{"flow":1,"rules":[{"need":["b"],"why":"x"}]}`,
		"unknown module":          `{"flow":1,"modules":["magic"]}`,
		"unknown in check":        `{"flow":1,"checks":{"a":{"run":"x","whn":["a"]}}}`,
		"failure without handoff": `{"flow":1,"failure":{"layers":[{"name":"api","paths":["api/**"],"tests":["api/**/*_test.go"],"boundary":"b"}]}}`,
		"failure no layers":       `{"flow":1,"modules":["handoff"],"failure":{"layers":[]}}`,
		"layer bad name":          `{"flow":1,"modules":["handoff"],"failure":{"layers":[{"name":"API","paths":["a"],"tests":["b"],"boundary":"x"}]}}`,
		"layer twice":             `{"flow":1,"modules":["handoff"],"failure":{"layers":[{"name":"api","paths":["api/**"],"tests":["api/**/*_test.go"],"boundary":"b"},{"name":"api","paths":["api/**"],"tests":["api/**/*_test.go"],"boundary":"b"}]}}`,
		"layer no tests":          `{"flow":1,"modules":["handoff"],"failure":{"layers":[{"name":"a","paths":["a"],"boundary":"x"}]}}`,
		"layer no boundary":       `{"flow":1,"modules":["handoff"],"failure":{"layers":[{"name":"a","paths":["a"],"tests":["b"]}]}}`,
		"layer bad glob":          `{"flow":1,"modules":["handoff"],"failure":{"layers":[{"name":"a","paths":["/a"],"tests":["b"],"boundary":"x"}]}}`,
		"layer check clash":       `{"flow":1,"modules":["handoff"],"checks":{"failure-a":{"run":"x"}},"failure":{"layers":[{"name":"a","paths":["a"],"tests":["b"],"boundary":"x","check":"y"}]}}`,
		"layer unknown field":     `{"flow":1,"modules":["handoff"],"failure":{"layers":[{"name":"a","paths":["a"],"tests":["b"],"boundary":"x","chek":"y"}]}}`,
	}
	for name, sec := range cases {
		if _, err := Parse([]byte(sec)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestNoContract(t *testing.T) {
	for _, f := range []string{`{"setup":[]}`, `{"extensions":{"other":{}}}`} {
		if _, err := FromProjectFile([]byte(f)); !errors.Is(err, ErrNoContract) {
			t.Errorf("%s: %v", f, err)
		}
	}
}

func TestDigestIgnoresWhitespace(t *testing.T) {
	a, _ := Parse([]byte(`{"flow":1,"full":"make ci"}`))
	b, _ := Parse([]byte("{ \"flow\": 1,\n  \"full\": \"make ci\" }"))
	if a.Digest() != b.Digest() {
		t.Fatal("digest depends on whitespace")
	}
}

func TestFailureLayerCheckBecomesACheck(t *testing.T) {
	c, err := Parse([]byte(`{"flow":1,"modules":["handoff"],"failure":{"layers":[
 {"name":"api","paths":["api/**"],"tests":["api/**/*_test.go"],"boundary":"b","check":"node ratchet.mjs"},
 {"name":"ui","paths":["ui/**"],"tests":["ui/**/*.test.js"],"boundary":"b"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	ch, ok := c.Checks["failure-api"]
	if !ok || ch.Run != "node ratchet.mjs" || strings.Join(ch.When, ",") != "api/**" {
		t.Fatalf("%+v", c.Checks)
	}
	if _, ok := c.Checks["failure-ui"]; ok {
		t.Fatal("a layer without check became a check")
	}
}

func TestFromProjectFileReadsTheFormerKey(t *testing.T) {
	min := `{"flow":1,"checks":{"t":{"run":"true"}}}`
	for _, c := range []struct {
		name, file string
		want       error
	}{
		{"new key", `{"extensions":{"devflow":` + min + `}}`, nil},
		{"former key", `{"extensions":{"picode-devflow":` + min + `}}`, nil},
		{"neither", `{"extensions":{"other":{}}}`, ErrNoContract},
	} {
		if _, err := FromProjectFile([]byte(c.file)); err != c.want {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
}
