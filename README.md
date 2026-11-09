# Dev flow

The development flow of [COGNIX[WS]](https://cognix.ws) — one worktree per
agent, scoped checks while you work, a close that says what is still owed,
handoff notes, debts that are paid instead of deleted, decision records —
bound to **any stack** by a short contract in your project's `.picode.json`.

The engine never calls `go`, `npm` or `pytest` on its own: your project says
which commands are its checks and which paths make each one run. Everything
else is the same for every repository.

Status: **0.2** — the `flow` engine, the agent side (tools for the eight
agent CLIs, craft skills, the background process) and the **Dev flow**
workspace page (Board, Checks, Activity), with "Set up with an agent" for a
repository that has no contract yet, and **git protection** per clone
(`flow hooks on|off|check`; turned on from the page). Merges follow. Design:
ADR-0401 in the COGNIX[WS] repository.

## In COGNIX[WS]

Install from Extensions (this repository's URL), turn it on in a workspace,
and choose **Dev flow** in an agent's tools. The agent gets these tools —
each answer ends with the next step:

| Tool | Does |
|---|---|
| `status` | Branch, ahead/behind, uncommitted files, the checks this change needs, what is owed, the next step |
| `check` | The checks this change needs, or `full=true`; waits up to 40 s, else returns a run id |
| `close` | Checks (reusing passes), the project's rules, what is owed; `strict=true` before landing |
| `land` | First runs the checks the contract **on the target branch** selects (inside COGNIX[WS], only a contract the person trusted counts), then the project's own land command (contract `land`) or COGNIX[WS]'s Merge where the workspace lets agents merge; otherwise the person merges in Merge |
| `wait` | Keep waiting for a run that answered "still running" |
| `debt` | Add a debt to a topic file, or mark one paid |
| `adr` | Seed a decision record with the next free number |
| `guide` | A craft guide's text, for CLIs that load no skills at launch |
| `agents` | The Dev flow block in `AGENTS.md`: missing, older, current or edited by hand; `write` puts the current one in place, never over a person's edit without `force` |

Skills (`quality-gate`, `handoff-note`, `decision-record`, `decision-table`)
ride the launch on the CLIs that take them; nothing is written to your
repository. One more is optional: `query-cost` teaches an agent to give a
project a check of its own that catches expensive queries (SQLite, Postgres,
MySQL); setup offers it only when the repository holds a database, and never
adds the check by itself. Long runs happen in the extension's background process
(`flow serve`), one per checkout at a time, two at once; results stay in the
extension's data folder.

The **Dev flow page** in the workspace has four tabs. **Board**: the
branches in flight, with a ⋯ menu per branch to run its checks, close it,
land it or open its agent. **Queue**: what runs now, the land queue in
order (move a waiting ticket earlier, later or first, or stop it; nothing
passes the ticket holding the target) and the recent verdicts. **Checks**
and **Activity**. The same queue is a **Queue** tab of the bottom dock. A
project without `land` lands through COGNIX[WS]'s Merge, and the tab shows
that queue read only (ordered and stopped on the Merge cards); a project with
`land` but no `queue` shows only the Dev flow's own runs.

The package carries prebuilt engines for Linux and macOS (`bin/`);
elsewhere `bin/flow.sh` builds one with Go on first use.

## Install

```sh
go install github.com/cognixws/cognix-devflow/cmd/flow@latest
```

## The contract

Add a section to `.picode.json` at the root of your repository:

```json
{
  "extensions": {
    "devflow": {
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
| `checks.<name>.affected` | Optional command reading changed paths on stdin; its output replaces `{affected}` in `run`. `@go-importers` is built in: the changed Go packages and every package of the module that imports them (tests included); nothing resolved leaves `{affected}` empty |
| `checks.<name>.junit` | Optional JUnit XML report (glob): failing tests are named, a test that passes on the retry is recorded as flaky |
| `checks.<name>.only` | `"full"`: runs only with the full gate |
| `checks.<name>.timeoutMin` | Default 45 |
| `full` | The full gate's command; without it the full gate is every check |
| `everything` | Paths whose change runs the full gate (lock files, the build file, this contract) |
| `rules` | `when` + `need` (touching these needs one of those; owed before landing) or `refuse` + `unless` (these may not be committed); `why` is the sentence shown |
| `generated` | A command whose output files must be committed |
| `docs` | `notes`, `topics`, `changelog`, `decisions`, `books` folders (defaults under `docs/`) |
| `modules` | `handoff`, `changelog`, `decisions`, `books`, `ui-review` |
| `ui` | Paths whose change owes a UI review (module `ui-review`, which needs `handoff`); default: `.jsx`, `.tsx`, `.vue`, `.svelte`, `.css`, `.scss`, `.html` files. The note carries `uiux-review:` and `visual-review:` lines; `FAIL` stops the close |
| `failure` | The failure-review gate (needs `handoff`): `layers`, each with `name`, `paths` (its code), `boundary` (one line: where its failures stop), `tests` (the files that prove it) and an optional `check` (a command, best a ratchet, that runs as the check `failure-<name>` when the layer is touched). A change to a layer's code owes `failure-review: <layer> PASS (<test>)` in the note with a changed test of that layer, or `n/a — <reason>`; `FAIL` stops the close. Absent, nothing changes |
| `noteMaxLines` | Default 25 |
| `quarantine` | A file of test names (one per line) whose failures do not fail a check |
| `land` | The project's own land command, run from the main checkout with `{branch}` filled in; without it, changes land through Merge |
| `queue` | The project's own land queue, for the person: `list` prints `{"tickets":[{"ticket","branch","state","ageSeconds"}]}` in order; `move` (with `{ticket}` and `{move}`, which becomes `--front`, `--before <ticket>` or `--after <ticket>`) and `drop` (with `{ticket}`) change one ticket; `red` names a file whose presence means the target's own checks failed and the queue waits. Commands run from the main checkout |

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
flow debt     --topic t [--pay] <text>       add or pay a debt
flow adr      [--title t] <name>             seed a decision record
flow guide    [topic]                        a craft guide
flow agents   [--write [--force]] [--json]   the Dev flow block in AGENTS.md
flow land                                    the project's own land command
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
