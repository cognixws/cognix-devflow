---
name: failure-review
description: Before the first edit to a layer the contract declares (UI, API, store, jobs…), name the failure that layer can meet, the boundary that stops it and the test that forces it; the handoff note carries one "failure-review: <layer>" line per touched layer, backed by a changed test of that layer, or n/a with the reason.
---

# Failure review

Every layer of an application meets failures: a dependency that is down,
input that is wrong, a timeout, a panic, a conflict, a refused permission.
A **boundary** is where the layer contains that failure and hands the layer
above something predictable: an error state with one action, a readable
status, a rolled-back transaction, a job that ends in a known state. The
project names its layers and their boundaries in the contract
(`failure.layers`); this guide is how you hold your change to them.

The proof is **a test that forces the failure**, not your word. The close
looks for a changed test of the layer next to a PASS line.

## Before the first edit

For each layer whose paths your task will touch (`flow status` and the
contract's `failure.layers` name them), answer three questions:

1. **What can fail here?** Pick the likely ones for this change, not the
   whole catalogue: the call you added can time out, the field you read can
   be missing, the goroutine you start can panic.
2. **Where does it stop?** The layer's declared `boundary`. Your new code
   must sit inside it: a new route inside the route boundary, a new handler
   answering through the project's error writer, a new background task
   started through the project's guarded helper.
3. **Which test forces it?** A fake that returns the error, a failpoint, a
   fetch that rejects, a transaction that fails halfway. Assert what the
   layer above sees: the status, the error state, nothing written, no event.

## In the handoff note

One line per touched layer:

```
failure-review: api PASS (TestCreateRefusesWhenStoreFails)
failure-review: ui PASS (routeBoundary.test.js: a throwing panel shows its error state)
failure-review: store n/a — renames a column comment, no new failure path
```

- **PASS (<test>)** names the test, and the change must carry a new or
  changed test matching the layer's `tests`. A PASS without one is refused.
- **n/a — <reason>** when the change adds no failure path (copy, a rename,
  a pure refactor covered by existing tests). The reason stays in the note,
  where a reviewer sees it. Never n/a to save time.
- **FAIL (<what escapes>)** stops the close: fix the boundary and test it.

A layer the change touched with no line is owed; `close --strict` (what the
land runs) refuses.

## Checks that only move one way

What can be counted without judgement belongs in the layer's `check`: raw
goroutines outside the guarded helper, routes outside the boundary, handlers
that write errors by hand. Make it a **ratchet**: a committed baseline the
count may not exceed; when you lower it, lower the baseline in the same
change. Never demand zero on day one.
