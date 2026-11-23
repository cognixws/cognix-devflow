---
name: ui-review
description: Review a screen change before the first edit and judge it from screenshots you actually read after — empty, blocked and error states included; every state once, both themes at each width only for layout-distinct states. The handoff note carries "uiux-review:" and "visual-review:" verdict lines; a FAIL never lands.
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
3. **Capture** the happy state, **and** every empty, blocked or error state
   you changed, a long/many state for lists (100+ rows, the longest real
   name), and every overlay you touched, open. How many:
   - **Every state once**, at the surface's primary width and theme (the
     ones most of its users see). Force the theme: a headless browser often
     reports light, so an app that follows the system theme renders light.
   - **Both themes at each width the surface has** (desktop and phone,
     390 px, when it has both) only for a **layout-distinct** state — a
     different arrangement, not only different words: the surface's main
     populated state; each dialog or sheet; any state whose CSS or
     structure your change touches.
   - **List the states you treated as layout-distinct, with the reason**
     for each, in the reply and the note. The reader can disagree.
   - **After a fix, re-capture only the states the fix changed.**

   Example: a settings page on desktop and phone with 20 states, 15 of
   which differ only in words. The populated list, 2 dialogs and 2 states
   whose CSS the change touched are layout-distinct: 15 × 1 + 5 × 4 =
   **35 captures**; the full matrix would be 20 × 4 = 80. A fix to one
   dialog re-captures that dialog's 4.
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
visual-review: PASS (subagent read 35 shots: 15 states once at 1280 dark; 5 layout-distinct × 1280/390 × dark/light — list: main state; 2 dialogs; empty + error: CSS changed)
```
