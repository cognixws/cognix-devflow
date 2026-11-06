package agentsmd

import (
	"errors"
	"strings"
	"testing"
)

const v1Block = "<!-- picode-devflow:begin v1 -->\n" + bodyV1 + "<!-- picode-devflow:end -->\n"

// Decision table: block found → state → what Apply does without and with force.
func TestInspectAndApply(t *testing.T) {
	edited := strings.Replace(Block(), "never merge by hand", "merge by hand on Fridays", 1)
	custom := "<!-- picode-devflow:begin custom -->\nOur own words.\n<!-- picode-devflow:end -->\n"
	cases := []struct {
		name, content, state string
		writes, forceWrites  bool
		err                  bool
	}{
		{"no file", "", Missing, true, true, false},
		{"a file without the block", "# Rules\n\nBe kind.\n", Missing, true, true, false},
		{"the current block", "# Rules\n\n" + Block(), Current, false, false, false},
		{"the current block with CRLF", "# Rules\r\n\r\n" + strings.ReplaceAll(Block(), "\n", "\r\n"), Current, false, false, false},
		{"the v1 block as the setup wrote it", "# Rules\n\n" + v1Block + "\nMore.\n", Older, true, true, false},
		{"a v1 block a person edited", "# Rules\n\n" + strings.Replace(v1Block, "never merge", "always merge", 1), Edited, false, true, true},
		{"the current block edited", "# Rules\n\n" + edited, Edited, false, true, true},
		{"a block the person keeps", "# Rules\n\n" + custom, Custom, false, true, true},
		{"a begin marker without its end", "<!-- picode-devflow:begin v2 sha256=abc -->\nx\n", Broken, false, false, true},
		{"two blocks", Block() + "\n" + Block(), Broken, false, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if st := Inspect(tc.content); st.State != tc.state {
				t.Fatalf("state = %s, want %s", st.State, tc.state)
			}
			out, changed, err := Apply(tc.content, false)
			if changed != tc.writes || (err != nil) != (tc.err && !tc.writes) {
				t.Fatalf("apply: changed=%v err=%v", changed, err)
			}
			if tc.state == Edited || tc.state == Custom {
				if !errors.Is(err, ErrEdited) || out != tc.content {
					t.Fatalf("an edited block must stay as it is: %v", err)
				}
			}
			fout, fchanged, ferr := Apply(tc.content, true)
			if fchanged != tc.forceWrites {
				t.Fatalf("force: changed=%v err=%v", fchanged, ferr)
			}
			for _, o := range []string{out, fout} {
				if o != tc.content && Inspect(o).State != Current {
					t.Fatalf("written content is not current:\n%s", o)
				}
			}
			if changed && !strings.Contains(out, "# Rules") && tc.content != "" {
				t.Fatalf("the rest of the file was lost:\n%s", out)
			}
			if strings.Contains(tc.content, "More.") && !strings.Contains(out, "\nMore.\n") {
				t.Fatalf("text after the block was lost:\n%s", out)
			}
		})
	}
}

func TestMarkerCarriesTheBodyHash(t *testing.T) {
	if !strings.HasPrefix(Block(), "<!-- picode-devflow:begin v2 sha256="+Hash(Body)+" -->\n") {
		t.Fatal(Block())
	}
	if Hash(Body) == Hash(bodyV1) {
		t.Fatal("v2 must differ from v1")
	}
}
