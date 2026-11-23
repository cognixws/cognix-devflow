package validate

import (
	"regexp"
	"strings"
	"testing"

	"github.com/cognixws/cognix-devflow/skills"
)

// mentionsVerdict finds a line that looks like a verdict line, however
// mangled: a lost colon, a list bullet, a different case.
var mentionsVerdict = regexp.MustCompile(`(?i)(uiux|visual)[- ]?review`)

// The verdict lines the ui-review guide tells an agent to write are the ones
// the close parses: every line of a code block in the guide that mentions a
// verdict matches verdictLine, and the guide shows exactly two of each kind
// (the template and the worked example), both PASS.
func TestUIReviewGuideLinesParse(t *testing.T) {
	b, err := skills.FS.ReadFile("ui-review/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	count := map[string]int{}
	inCode := false
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inCode = !inCode
			continue
		}
		if !inCode || !mentionsVerdict.MatchString(line) {
			continue
		}
		m := verdictLine.FindStringSubmatch(line)
		if m == nil {
			t.Errorf("the guide's verdict line does not parse: %q", line)
			continue
		}
		count[m[1]+" "+m[2]]++
	}
	for _, want := range []string{"uiux-review PASS", "visual-review PASS"} {
		if count[want] != 2 {
			t.Errorf("the guide shows %d %q lines, want 2 (template and example)", count[want], want)
		}
	}
}
