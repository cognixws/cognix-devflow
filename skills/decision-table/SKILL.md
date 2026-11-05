---
name: decision-table
description: When conditions interact to change an outcome (delete, restore, auth, cascade, run mode, permissions) — write the decision table before claiming done, one row per combination, each row tested or recorded as a debt.
---

# Decision table

Use it when two or more conditions together decide what happens: delete vs
archive, who may do it, which mode, what exists already. Skip it for copy,
polish and single-path fixes.

1. List the conditions (columns) and their values.
2. One row per combination that can happen: conditions → action. Merge rows
   only when the outcome is truly the same.
3. Put the table where the behavior is documented (architecture notes or the
   plan), not only in the chat.
4. Every row has a test named after it, or a debt (Dev flow **debt** tool)
   that says which row is untested and why.
5. Two manual clicks are not a matrix: a row is covered when a test asserts
   its outcome.

| # | Condition A | Condition B | Outcome | Test |
|---|---|---|---|---|
| 1 | … | … | … | `TestX_row1` |
