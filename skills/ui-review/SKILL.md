---
name: ui-review
description: Review a screen change before the first edit and judge it from screenshots you actually read after — empty, blocked and error states included, both themes, phone width. The handoff note carries "uiux-review:" and "visual-review:" verdict lines; a FAIL never lands.
---

# UI review

For any change to a screen: web, phone, desktop, an extension panel, or the
copy on one. Code review cannot see pixels, and neither can a DOM dump. Two
verdicts close the change, both written in the handoff note:

```
uiux-review: PASS (what you checked, in one line)
visual-review: PASS (which screenshots, read where)
```

`FAIL` on either stops the close (module `ui-review`). Never write PASS from
memory, from a passing test, or from JSON.

## Before the first edit — uiux-review

Read the surface you are changing and the project's design rules (tokens,
components, the benchmark it follows). Then hold the change to these:

| Area | The bar |
|---|---|
| Copy | Buttons are a verb, one or two words. Empty, blocked and error states on a primary surface: a title, **one** sentence saying what is true and what the action does, and **one** action. A secondary tile: one line and one action. No essays, no setup tutorials, no file paths in chrome. |
| States | Every list and panel has its empty, loading, error and long/many states. Never a blank well, never a "0" badge. Loading keeps the last data or shows a skeleton of the real layout. |
| Controls | Adjacent controls share one height. Icon and label stay on one line. A choice that is unavailable is hidden or says why in visible text — not a disabled control with only a tooltip. |
| Primitives | The project's component library or native controls before a hand-made widget; say why when none fits. Dialogs become sheets on a phone. |
| Theme | Colours come from the project's tokens, never a new literal. Text contrast ≥ 4.5:1 (large 3:1) in light and dark, measured. |
| Slop | No coloured stripe down one side of a box, no card inside a card, no gradient text, no emoji as icons, no description under every menu item. |
| Motion | State that takes time shows it at once (optimistic or progress), then the result. Reduced motion removes movement, keeps the fades that mean something. |

## After the change — visual-review

1. **Run** the change on a throwaway instance, never production or the
   person's own session.
2. **Drive** it with a headless browser (snapshot refs, then clicks).
3. **Capture** at desktop width and at phone width (390 px), in light and
   dark: the happy state **and** the empty, blocked or error state you
   changed, and a long/many state for lists (100+ rows, the longest real
   name). Open every overlay you touched.
4. **Read** every screenshot as an image — in a subagent when your CLI has
   them, so images never fill the main session. Keep screenshots out of git.
5. **Judge**. Instant FAIL, on anything your change touched:
   - an overlay or menu cut by the viewport or its container;
   - text cut without an ellipsis, wrapped into sludge, or under 10 px;
   - a control covered so it cannot be used, or a double scrollbar;
   - a row of controls with uneven heights or tops;
   - a hover that paints the same colour as its parent;
   - contrast below the bar, or a page that scrolls sideways at 390 px.
   If the project ships a page audit (contrast, clipping, overflow), run it
   after each overlay opens; a finding on what you changed is a FAIL.
6. **Fix or FAIL.** Seeing a defect and calling the change done is the one
   thing this review exists to stop. A defect that is not in your change
   goes to the topic file as a debt.

Write both lines into the note, with what was checked:

```
uiux-review: PASS (empty state: title + sentence + Add; 36 px row; tokens only)
visual-review: PASS (subagent read 8 shots: 1280/390 × light/dark, empty + error)
```
