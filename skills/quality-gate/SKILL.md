---
name: quality-gate
description: Before you say a change is done — run the Dev flow close, then the judgement checklist (one change, tests, decision table, dependencies, effect named, docs) and report PASS or FAIL honestly.
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

## Report

```
quality-gate: PASS (close ✓ tests +4 decision-table 6/6 changelog +1)
quality-gate: FAIL (close: test failed — CartTotals.half_cent)
```
