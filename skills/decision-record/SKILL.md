---
name: decision-record
description: When a change crosses a boundary (protocol, persistence, security model, process) — seed a decision record with the Dev flow adr tool, fill the Boundary line first, argue context → decision → consequences.
---

# Decision record

A decision record is for a **boundary**: a protocol, persistence, the
security model, or the process. If the Boundary line has nothing honest to
say, it is not a decision record — write the paragraph in the project's
architecture notes instead. A screen refinement never needs one.

1. Seed it with the Dev flow **adr** tool (`flow adr <name>` in a shell): it
   takes the next number free in every worktree and the main branch, uses the
   project's template and adds the index row.
2. Fill **Boundary** first: which one, and what crosses it. The close
   refuses a new record whose Boundary line is the template's placeholder.
3. Context (facts and forces), Decision (one paragraph, present tense, no
   hedging), Consequences (what gets easier, harder, and who breaks if it is
   wrong), Alternatives (each, and why it lost).
4. A record is never edited after acceptance to say something else: a new
   record amends or supersedes it, and both index rows say so.
5. Changing a documented decision is the owner's call: name the decision,
   the trade it made and what changing it costs, then wait.
