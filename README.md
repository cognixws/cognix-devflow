# Dev flow

The development flow of [COGNIX[WS]](https://cognix.ws) — one worktree per
agent, scoped checks while you work, a close that says what is still owed,
handoff notes, debts that are paid instead of deleted, decision records —
bound to **any stack** by a short contract in your project's `.picode.json`.

The engine never calls `go`, `npm` or `pytest` on its own: your project says
which commands are its checks and which paths make each one run. Everything
else is the same for every repository.

Status: **F1** — the `flow` engine (this repository). The COGNIX[WS]
extension (agent tools for the eight agent CLIs, the Dev flow workspace
page, git protection, merges) follows. Design: ADR-0401 in the COGNIX[WS]
repository.

## Install

```sh
go install github.com/cognixws/picode-devflow/cmd/flow@latest
```

## The contract

Add a section to `.picode.json` at the root of your repository:

```json
{
  "extensions": {
    "picode-devflow": {
      "flow": 1,
      "checks": {
        "typecheck": { "run": "npm run typecheck", "when": ["apps/**", "packages/**"] },
        "test": { "run": "npm test", "when": ["apps/**", "packages/**"], "junit": "var/junit/*.xml" },
        "e2e": { "run": "npm run e2e", "only": "full" }
      },
      "full": "make ci",
      "everything": ["package-lock.json", "Makefile", ".picode.json"],
      "rules": [
        { "when": ["db/migrations/**"], "need": ["docs/database.md"], "why": "Schema changes update the database notes" },
        { "refuse": ["**/*.xlsx"], "unless": ["fixtures/**"], "why": "Real client data never enters git" }
      ],
      "generated": [{ "run": "npm run openapi", "files": ["docs/openapi.json"] }],
      "modules": ["handoff", "changelog", "decisions"]
    }
  }
}
```

| Field | Meaning |
|---|---|
| `flow` | Contract version; `1` |
| `main` | The branch changes land on (default `main`) |
| `checks.<name>.run` | One shell line, run at the repository root |
| `checks.<name>.when` | Path patterns that make the check run; none means every change |
| `checks.<name>.affected` | Optional command reading changed paths on stdin; its output replaces `{affected}` in `run` |
| `checks.<name>.junit` | Optional JUnit XML report (glob): failing tests are named, a test that passes on the retry is recorded as flaky |
| `checks.<name>.only` | `"full"`: runs only with the full gate |
| `checks.<name>.timeoutMin` | Default 45 |
| `full` | The full gate's command; without it the full gate is every check |
| `everything` | Paths whose change runs the full gate (lock files, the build file, this contract) |
| `rules` | `when` + `need` (touching these needs one of those; owed before landing) or `refuse` + `unless` (these may not be committed); `why` is the sentence shown |
| `generated` | A command whose output files must be committed |
| `docs` | `notes`, `topics`, `changelog`, `decisions`, `books` folders (defaults under `docs/`) |
| `modules` | `handoff`, `changelog`, `decisions`, `books`, `ui-review` |
| `noteMaxLines` | Default 25 |
| `quarantine` | A file of test names (one per line) whose failures do not fail a check |

Patterns are relative to the repository root; `**` matches any number of
folders and `*` never crosses a `/`: `*.md` is a root file, `**/*.md` any.
Unknown fields are refused.

**Scope fails safe.** An empty change, a path in `everything`, or a path no
check covers runs the full gate.

## Commands

```text
flow status   [--json]                       where this checkout stands and what is next
flow check    [--full] [--only n] [--fresh]  run the checks this change needs
flow close    [--strict] [--json]            checks, process rules, summary; --strict fails on owed documents
flow validate [--json]                       process rules only
flow scope    [--contract f] [--batch]       the checks a list of paths (stdin) selects
```

Exit codes: `0` passed, `1` a check or a rule failed (or close refused),
`3` `--strict` and something is owed, `2` usage.

Process rules (no stack involved): whitespace errors and conflict markers;
the project's rules; the handoff note `docs/handoff/<date>-<branch>.md` and
its length; an open debt (`- [ ]`) deleted instead of marked paid; a direct
edit of `CHANGELOG.md`; a new decision record without its Boundary line, its
index row or with a duplicate number; generated files left stale.

A check that passed is not run again on the same content: passes are keyed
by the check, the contract's digest and the content of the files its
patterns cover, and recorded only for a clean checkout. Run history lives in
`<git common dir>/picode-devflow/` (logs, passes, flaky tests), shared by
every worktree and never committed.

## License

Apache-2.0.
