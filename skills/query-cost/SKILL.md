---
name: query-cost
description: Optional. When a project has a database and wants a check that catches expensive queries (full scans, a missing index, a correlated subquery that reads a table per row) — write it in the project's own terms, with named exceptions that carry a reason, and declare it as an ordinary check. SQLite, Postgres and MySQL, and what differs between them.
---

# Query cost

A missing index costs nothing in review and a lot in production: on a
database billed per row read (Cloudflare D1), a correlated
`(SELECT MAX(…) FROM t WHERE t.k = outer.id)` with no index on `t(k, …)`
read the table once per outer row and cost real money on 2026-10-22. A
check can see that before it ships, **without data**, by asking the
database for its plan.

This is a suggestion, never a rule: the Dev flow knows no database. You give
the project **a script of its own** and declare it as an ordinary check
(step 4). The person reviews and trusts it like any other.

Marks below: **[measured]** = run on 2026-11-09 against SQLite 3.46.1,
PostgreSQL 16.15 and MySQL 8.4.13 (commands in each section). **[docs]** =
from the engine's documentation or the owner's note, not seen here. Other
versions can differ; re-run the command before relying on a line.

## 1. What to check

Read the project: its schema (migrations, `schema.sql`, ORM models) and the
queries it runs (query files, repository functions, the ORM calls that
matter). Ask the person which engine and which queries are the hot ones; do
not guess a list from a grep.

The check has three parts, all in the project's language and layout:

1. **The real schema**, applied to a scratch database: an in-memory SQLite,
   the container the project's tests already start, or its test database.
   Never a shared or production one.
2. **The queries**, as a small list kept in the repo (name → SQL) that the
   project's tests or code also import, so the list cannot drift from what
   runs. A query written only for the check proves nothing.
3. **The plan of each**, read from the engine, failing on the nodes that
   mean "reads many rows to find few" (the sections below name them) unless
   the query is a named exception.

## 2. Named exceptions

A table that stays small (settings, a list of roles, 50 rows) may scan
forever. Keep exceptions in a file in the repo, one entry per
`query/target` with a **reason**:

```json
{ "setting/settings": "settings stays under 50 rows", "last_seen/u": "the admin list reads every user by design" }
```

- An entry without a reason fails the check.
- An entry that nothing matches any more (the query was fixed or removed)
  fails too, so the list only shrinks.
- Key by query *and* target, never by query alone: excusing a whole query
  hides the next missing index in it.
- A project with scans it already has takes a **baseline** instead: today's
  findings recorded with a reason each; the check fails on a new one.

## 3. Engines

### SQLite — `EXPLAIN QUERY PLAN`, no data needed

**[measured]** The plan on the real schema over **empty tables** already
shows scan versus index, because SQLite plans from the schema:

| Plan detail | Meaning |
|---|---|
| `SEARCH t USING INDEX i (k=?)` | indexed lookup |
| `SEARCH t USING INTEGER PRIMARY KEY (rowid=?)` | primary key lookup |
| `SCAN t` | reads every row |
| `SCAN t USING COVERING INDEX i` | reads every index entry: still a full scan |
| `SEARCH e` with **no** `USING` | **a scan in disguise** |

The last row is the one to know. The correlated `MAX` subquery above, with
no index, printed `SCAN u`, `CORRELATED SCALAR SUBQUERY 1`, `SEARCH e` and
nothing else: no `SCAN` on the table that hurts. Over 2000 users and 200,000
events it took 9.33 s; with `CREATE INDEX ev_user_at ON ev(user_id, at)` the
plan became `SEARCH e USING COVERING INDEX ev_user_at (user_id=?)` and the
same query took 0.002 s. **A check that only greps for `SCAN` misses it.**
Flag `SCAN`, and flag `SEARCH` that has no `USING`.

Other plans that scan **[measured]**: `LIKE '%x'` on an indexed column;
a function on an indexed column (`abs(user_id) = 5`); `ORDER BY` on an
unindexed column also prints `USE TEMP B-TREE FOR ORDER BY`. The plan names
the **alias** when the query has one (`SCAN u`, not `SCAN users`): key
exceptions by what the plan prints.

The text of plan details is not a stable interface across SQLite versions
**[docs]**; pin the check to the version the project runs, and test it with
one known-bad query so a format change fails loudly instead of passing
silently. A complete example (SQLite from Python), measured to
exit 1 on the missing index, 0 once added, and 1 on an exception with no
reason or a stale one:

```python
import json, re, sqlite3, sys
db = sqlite3.connect(":memory:")
db.executescript(open("schema.sql").read())                   # the project's real schema
queries = json.load(open("queries.json"))                     # {"name": "SQL, ? for params"}
excused = json.load(open("query-cost.exceptions.json"))       # {"name/target": "why it may scan"}
bad, seen = [], set()
for name, sql in queries.items():
    for row in db.execute("EXPLAIN QUERY PLAN " + sql, [0] * sql.count("?")):
        m = re.match(r"(SCAN|SEARCH) (\w+)(.*)", row[3])
        if m and (m[1] == "SCAN" or "USING" not in m[3]):     # a SEARCH with no USING is a scan too
            key = f"{name}/{m[2]}"                            # the plan prints the alias if there is one
            seen.add(key)
            if not excused.get(key, "").strip():
                bad.append(f"{key}: {row[3]}")
bad += [f"exception {k!r}: no reason, or nothing scans there any more" for k, why in excused.items() if not why.strip() or k not in seen]
print("\n".join(bad) or "query-cost: ok")
sys.exit(1 if bad else 0)
```

Adapt it to the project's language and layout; it is an illustration, not
something to copy in.

**Hosted SQLite (Cloudflare D1)** **[docs]**: D1 is SQLite and bills per row
read, so the same plan applies; run the check against a local SQLite built
from the same migrations rather than against the hosted database. That the
local plan equals the hosted one, and what D1's own per-query row counts
report, were **not** verified here.

### Postgres — the planner needs volume, or a switch

A naive "fail on `Seq Scan`" on a fresh test database gives **false
alarms**, and in the other direction a fresh one can hide a real problem.
**[measured]** on `orders(user_id)` with an index on `user_id`,
`EXPLAIN SELECT * FROM orders WHERE user_id = 5`:

| State of the table | Plan |
|---|---|
| empty, never analyzed | `Bitmap Heap Scan` + `Bitmap Index Scan` (index used) |
| empty, after `ANALYZE` | **`Seq Scan`** (false alarm) |
| 100 rows, analyzed | **`Seq Scan`** (false alarm) |
| 10,000 rows, analyzed | `Index Scan` |
| 200,000 rows, `ANALYZE` | `Bitmap Heap Scan` + `Bitmap Index Scan` |
| 200,000 rows, `ANALYZE`, **no index** | `Seq Scan`, cost 3582 (the real finding) |
| 200,000 rows, index on a column where every row has the same value | `Seq Scan` (right: the index would not help) |

So the planner's choice depends on the table's size and statistics, not
only on whether an index exists. Two ways to get a trustworthy answer:

1. **Seed volume, then `ANALYZE`, then `EXPLAIN (FORMAT JSON)`.** Enough
   rows that a missing index shows (the 200,000-row case above), realistic
   spread in the filtered columns, `ANALYZE` after the insert. Without
   `ANALYZE` the 200,000-row case also chose the index here, but the row
   estimate was 611 instead of 10: do not rely on that.
2. **No volume: turn sequential scans off for the session.** **[measured]**
   `SET enable_seqscan = off` before `EXPLAIN`: on an empty, analyzed table
   *with* the index the plan was `Index Scan`; *without* it, still
   `Seq Scan` but with a start and total cost of `10000000000.00`, the
   marker that no index was available. This tells "an index exists the
   planner can use" from "none does", on an empty schema. It says nothing
   about whether an index is *worth* using. (On 16 the JSON has no
   "disabled" field, the cost is the marker; newer versions may differ.)

Read the JSON by walking the tree: nested nodes sit under `Plans`, and a
correlated subquery sits under a node with `"Parent Relationship":
"SubPlan"` **[measured]**. Flag `"Node Type": "Seq Scan"` (with its
`"Relation Name"`; the alias is a separate `"Alias"`). `psql -At -c "SET
enable_seqscan=off" -c "EXPLAIN (FORMAT JSON) …"` runs both in one session,
and the first line of output is `SET`, so drop it before parsing.

The correlated `MAX` case **[measured]** (2000 users, 200,000 events): no
index, outer `Seq Scan on users` with a `SubPlan` doing a `Seq Scan on ev`
per user, total cost 7,164,551. With `ev(user_id, at)`: the subplan became a
`Limit` over `Index Only Scan Backward using ev_user_at`, total cost 8,690.

**What is expensive in real use: `pg_stat_statements`** **[measured]**. It
needs `shared_preload_libraries = 'pg_stat_statements'` and a **server
restart**, then `CREATE EXTENSION pg_stat_statements`. Then
`SELECT query, calls, rows, total_exec_time, mean_exec_time,
shared_blks_hit + shared_blks_read AS blks FROM pg_stat_statements ORDER BY
total_exec_time DESC` listed the unindexed query first (2 calls, 2164
blocks) and the indexed one last (1 call, 5 blocks). Queries come back
normalized (`WHERE user_id = $1`); on 16, `EXPLAIN (GENERIC_PLAN, FORMAT
JSON)` accepts that text as is and returned the plan **[measured]**. This
is for a person to find candidates on a real database, then add them to the
query list; it is not a pass/fail check.

### MySQL — `EXPLAIN` and its `type` column

**[measured]** Plain `EXPLAIN` (or `EXPLAIN FORMAT=JSON`, where it is
`access_type`) on the real schema. Unlike Postgres, MySQL gave the same
answer over **empty tables**, so no seeding was needed for these:

| `type` | Seen | Meaning |
|---|---|---|
| `ALL` | `WHERE user_id = 5`, no index | full table scan: flag |
| `index` | `SELECT user_id FROM orders` (`Using index`) | full index scan: reads every entry, flag |
| `range` | `user_id BETWEEN 5 AND 50` | index range, fine |
| `ref` | `user_id = 5` with an index | indexed lookup, fine |

Also seen **[measured]**: with the index, a 100-row and a 5-row table still
chose `ref`; `WHERE id = 5` on an empty table printed `no matching row in
const table` with `type` NULL (nothing to flag). A column where every row has
the same value still used its index (`ref`, 99,918 estimated rows): `ref` is
not proof the query is cheap, so read `rows` as well. `const`, `eq_ref` and
`system` are the cheapest types **[docs]**, not seen here.

The correlated `MAX` case **[measured]**: no index gave `select_type`
`DEPENDENT SUBQUERY` with `type` `ALL`, 200,030 estimated rows, once per
outer row; with `ev(user_id, at)` it became `ref` / `Using index`. Flag
`DEPENDENT SUBQUERY` whose own `type` is `ALL` or `index`.

An index did not make it free: the `performance_schema` digest still
counted 202,000 rows examined for the indexed form (about 99 index entries
per user, since `MAX` is read by walking them), against 400,000 for two runs
of the unindexed single-table query. `EXPLAIN` shows the access path, not the
total; for a billed or slow case, look at the digest too.

**What is expensive in real use** **[measured]**:
`performance_schema.events_statements_summary_by_digest`, columns
`DIGEST_TEXT`, `COUNT_STAR`, `SUM_ROWS_EXAMINED`, `SUM_ROWS_SENT`,
`SUM_NO_INDEX_USED`, `SUM_NO_GOOD_INDEX_USED`. The unindexed query showed
`SUM_NO_INDEX_USED = 2` for 2 calls. Its `log_queries_not_using_indexes`
slow-log option is **[docs]**, not run.

## 4. Declare it

Put the script in the repo and declare it like any check, with the paths
that can change its answer (the schema and migrations, the query list, the
exceptions file):

```json
"query-cost": { "run": "python3 scripts/query_cost.py", "when": ["migrations/**", "src/**/queries/**", "query-cost.exceptions.json"] }
```

- Run it once on a clean checkout. If it already finds scans, record them
  as exceptions with their reason, or as a debt (Dev flow **debt** tool);
  do not leave a check that fails on day one.
- It needs a database only when the engine does (Postgres, MySQL): reuse
  what the project's tests already start; do not add a container the
  project does not run. If it cannot run where the checks run, say so and
  leave it out.
- Never add it unasked: this skill is the answer to "can we catch this?",
  not a gate to install. Do not ask the Dev flow to know which database
  the project uses; the command is the project's.

## 5. Not covered

Anything not seen above: other engines, ORMs that hide the SQL (find the
SQL the ORM sends, or log it in a test), the cost of writes and locks, and
the real latency of a query (a plan is a prediction, not a timing; do not
assert on milliseconds).
