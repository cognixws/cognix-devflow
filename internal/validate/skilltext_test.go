package validate

import (
	"strings"
	"testing"

	"github.com/cognixws/cognix-devflow/skills"
)

// The verdict lines the ui-review guide tells an agent to write are the ones
// the close parses: every example "uiux-review:" / "visual-review:" line in
// the guide matches verdictLine, and both kinds appear with PASS.
func TestUIReviewGuideLinesParse(t *testing.T) {
	b, err := skills.FS.ReadFile("ui-review/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		l := strings.TrimSpace(line)
		if !strings.HasPrefix(l, "uiux-review:") && !strings.HasPrefix(l, "visual-review:") {
			continue
		}
		m := verdictLine.FindStringSubmatch(l)
		if m == nil {
			t.Errorf("the guide's line does not parse: %q", l)
			continue
		}
		got[m[1]+" "+m[2]] = true
	}
	for _, want := range []string{"uiux-review PASS", "visual-review PASS"} {
		if !got[want] {
			t.Errorf("the guide shows no %q line", want)
		}
	}
}
