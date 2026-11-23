---
name: quality-gate
description: Before you say a change is done — run the Dev flow close, then the judgement checklist (one change, tests, decision table, dependencies, effect named, docs), then one independent review by a fresh-context agent that did not write the code, and report PASS or FAIL honestly with its findings verbatim.
---

# Quality gate

Run this before you call any change done. A failure reported honestly beats
a pass reported falsely.

## The mechanical part is one call

Call the Dev flow **close** tool (in a shell: `flow close`). It refuses an
uncommitted tree, runs the checks this change needs (reusing passes on the
same content), applies the project's rules and lists what is still owed:
the handoff note, a changelog fragment, an index row. Do not run the
project's tests one by one in twenty turns; when close fails, fix the cause
and close again.

## The judgement part (read the diff against the main branch)

1. **One logical change?** If not, split it.
2. **New code without tests?** Write them before finishing.
3. **Conditions that change the outcome** (delete, restore, auth, cascade,
   mode, permissions)? A decision table exists and every row is tested or
   recorded as a debt (`decision-table` skill).
4. **New dependency?** Say why in the commit message; no reason, no dependency.
5. **Effect named for every automated step.** "The command exited 0" is a
   return, not a verdict: name how the effect was verified (the field holds
   the text, the event arrived, the file changed).
6. **Docs travel with the code**: behavior changed → the project's
   architecture notes; a boundary crossed (protocol, persistence, security
   model, process) → a decision record (`decision-record` skill).
7. **User-visible change** → a fragment in the changelog folder, never the
   changelog file itself.
8. **Handoff**: one note (`handoff-note` skill).
9. **Screens changed?** Look at them on a test instance, not in production;
   reading source or DOM JSON is not a visual verdict.

## Independent review before the report

Your own review misses what a fresh reader finds. Before you report a
change to product code as done, start **one** independent review yourself.
This is not a new gate: it replaces waiting for whoever assigned the task
(a coordinator, a lead, the person) to send a reviewer. They may still ask
for another, and should for anything that touches a security boundary,
real user data or money.

1. **Reviewer**: an agent that did not write the code — a subagent with a
   fresh context, on the strongest model your CLI offers. Not this
   conversation, and not a fork of it.
2. **Given**: the diff range and the brief below, unchanged but for the
   placeholders. Not your conclusions, test claims or summary.
3. **Read-only**: it edits, commits and runs nothing that changes state.
4. **After**: fix each finding with a test that fails first, then report
   with the verdict and findings attached **verbatim**, not summarised. A
   finding you believe is wrong is never fixed silently or dropped: it
   stays in the report with your reason, and the work is not done until
   whoever assigned it decides.
5. **Docs-only changes** get a fact-check review in the same shape: in the
   brief, "Hunt" becomes "each claim in the diff, checked against the
   source it cites and against the code it describes; a claim with no
   source is a finding."
6. **No subagents in your CLI?** Ask the person or the coordinator for the
   review, and wait for it. Reviewing your own change in the same
   conversation is not independent; never call it that.

The brief:

```text
Review <base>..<head> in <checkout>. Read-only: edit, commit and run
nothing that changes state.

Read: git diff <base>..<head>; each changed file in full, not only the
hunks; the callers and the tests of what changed; the architecture notes
and decision records the changed code cites.

Do not trust the builder's claims, commit messages or comments; check the
code. A test passing is not proof: read what it asserts.

Hunt, layer by layer:
- Authorization and scope: who may call this, on which data; a path that
  skips the check; one person's or agent's data reaching another.
- Data loss and silent drops: a write that can be lost, an error that is
  swallowed, a list that is cut without saying so, a migration or restore
  that leaves something behind.
- Double effects: a retry, a second click, a replayed event or two writers
  doing the same thing twice.
- What a person is shown vs what happens: copy, state or a success message
  that does not match the effect.
- Tests that assert mocks: a test that would pass with the code removed,
  or that checks the stub instead of the behaviour.
- Docs that claim more than the code does.

Return:
verdict: PASS | FIX | FAIL
findings, most severe first, each with: file:line, the scenario (input or
state -> wrong result), the fix.
not verified: what you could not check, and why.
```

## Report

```
quality-gate: PASS (close ✓ tests +4 decision-table 6/6 changelog +1; review FIX → 2 fixed, tests +2)
quality-gate: FAIL (close: test failed — CartTotals.half_cent)
```

Below the line, the reviewer's verdict and findings, verbatim, and any
finding you dispute with your reason.
