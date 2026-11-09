---
name: dev-flow-setup
description: Set up the Dev flow in a repository — read how it builds and tests, write the extensions.devflow section of .picode.json with checks the project already runs, add the Dev flow block to AGENTS.md, and commit it on a branch for the person to review.
---

# Set up the Dev flow

You bind the Dev flow to this repository's own commands. Never invent a
command: every check runs something the project already runs (its Makefile,
package.json scripts, pyproject, go.mod, Cargo.toml, CI workflow).

## 1. Work on a branch

Start a branch (`feat/dev-flow-setup`) in a worktree of your own. Nothing
here is committed on the main branch.

## 2. Find the checks

Read the build and CI files. For each command the project runs to verify a
change (typecheck, lint, unit tests, build), note the paths whose change
makes it relevant. Run each command once to confirm it passes today; if one
fails on a clean checkout, leave it out and say so.

Hints, never rules:

| Files | Usual checks |
|---|---|
| `go.mod` | `go vet ./...`, `go test ./...` when `**/*.go` changes |
| `package.json` | its `typecheck`, `lint`, `test`, `build` scripts, for the source folders |
| `pyproject.toml` | `pytest`, `ruff check .` for `**/*.py` |
| `Cargo.toml` | `cargo test`, `cargo clippy` for `**/*.rs` |
| `Makefile` | the targets the CI already calls |

## 3. Write the contract

Add (or extend) `.picode.json` at the repository root:

```json
{
  "extensions": {
    "devflow": {
      "flow": 1,
      "checks": {
        "test": { "run": "npm test", "when": ["src/**"] }
      },
      "full": "make ci",
      "everything": ["package-lock.json", "Makefile", ".picode.json"],
      "modules": ["handoff", "changelog", "decisions"]
    }
  }
}
```

- `when` patterns are relative to the root; `**` matches folders, `*` never
  crosses a `/`. A path no check covers runs the full gate, so cover the
  source folders.
- `full` is the command the CI runs, if there is one; without it the full
  gate is every check.
- `everything`: lock files, the build file, `.picode.json` itself.
- Add `rules` only for what the project's own docs already demand.
- Add `land` only when the project already lands with a command of its own,
  and `queue` only when that command keeps a queue it can list as JSON
  (`list`, plus `move` and `drop` if it can change one ticket): the person
  then sees and orders it in the Queue tab. Never write a queue script for
  the project.
- Optionally offer `failure` (the failure-review gate) when the project
  has clear layers and already contains failures somewhere (an error
  boundary, an error writer, a guarded background helper): one entry per
  layer with `name`, `paths`, `tests`, and a one-line `boundary` in the
  project's own words, e.g.
  `{"name":"api","paths":["src/server/**"],"tests":["src/server/**/*.test.ts"],"boundary":"a handler answers a readable status; nothing throws past the error middleware"}`.
  Ask the person before adding it: every change to a layer then owes a
  `failure-review:` line in the note (see the failure-review guide).
- Offer the `query-cost` guide only when the repository holds a database:
  run this from the root and open what it lists.

  ```sh db-signals
  { git ls-files | grep -Ei '\.(sql|sqlite3?|db)$|(^|/)(migrations?|prisma)/'; git grep -lEi 'd1_databases|image: *.?(postgres|mysql|mariadb)|sqlite3?|psycopg|sqlx|knex|prisma|drizzle' -- ':!*.md' ':!*.txt'; } | sort -u
  ```

  A hit is a lead, not proof: a file that only mentions a database does not
  make one. Nothing listed, or nothing real behind it: say nothing about
  it. Found one: say in one line what the guide does (a check that reads
  the query plans against the project's real schema and fails on a full
  scan unless the query is a named exception) and ask. **Never add the
  check yourself, and take a "no" as the answer**; the guide is read with
  `flow guide query-cost`.
- Keep the file's other keys (`setup`, `run`, `teardown`) as they are.

Call the Dev flow **status** tool: it must read the contract without an
error and name the checks your diff needs.

## 4. Add the block to AGENTS.md

Call the Dev flow **agents** tool with `write=true` (outside COGNIX[WS]:
`flow agents --write`). It appends the Dev flow block to `AGENTS.md` at the
root (creating the file if needed), between markers that carry the block's
version and a hash of its text:

```markdown
<!-- picode-devflow:begin v2 sha256=… -->
## How work flows here (Dev flow)
…
<!-- picode-devflow:end -->
```

Never write or edit the block by hand. Later Dev flows say when the block is
older (status warns) and replace it on `write`; a block a person edited is
never replaced without `force`. A project that keeps words of its own marks
its block `<!-- picode-devflow:begin custom -->`, and the flow leaves it
alone.

## 5. Commit and stop

Commit `.picode.json` and `AGENTS.md` with a message that lists the checks
you declared and any command you left out. Do not merge: the person reviews
the new checks in Settings › Scripts and trusts them before anything runs.
