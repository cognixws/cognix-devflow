---
name: handoff-note
description: Write the session's handoff note from the Dev flow close summary — at most 25 lines, facts only — and put durable items in a topic file, where a debt is paid by marking it, never deleted.
---

# Handoff note

Every change that lands leaves one note: `docs/handoff/<date>-<branch>.md`
(the folder and limit come from the project's contract; the close names the
exact file). Write it **from the close summary**, not from memory, and if
your CLI has subagents, in a subagent — never at the peak of a long session.

## The note

```markdown
# <branch> — <one line: what changed>

- What landed: <commits or the behavior, in a line or two>
- Verified: <the checks, and the effect you saw>
- Not verified: <honestly, what nobody looked at>

## Next up
- <one bullet each, only what the next person needs this week>

## Debts
- <one bullet each>
```

At most 25 lines (the close refuses more). Shipped work is the git log; the
note says what someone needs to continue.

## Topics: durable items

A thing that outlives this week goes to `docs/handoff/open/<topic>.md`, under
`## Next` or `## Debts`. Use the Dev flow **debt** tool to add one. A debt is
paid by marking it — `- [ ]` becomes `- [x] … — paid <date>` (the debt tool
does it) — **never by deleting the line**, and never delete another
session's line: the close refuses a deleted open item.
