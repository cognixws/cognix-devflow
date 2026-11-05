// The Dev flow workspace page (door D): Board, Checks, Activity. Everything
// comes from the extension's process through the daemon's relay; the page
// only reads. Actions that need the person go through the workspace door:
// New agent with the setup task, or the workspace's Scripts settings.
(async function () {
  const root = document.getElementById("root");
  const live = document.getElementById("live");
  const [proc, ctxDoor, events, ws, links] = await Promise.all(["process", "context", "events", "workspace", "links"].map((d) => picode.use(d)));
  await picode.use("theme");

  const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]);
  // Opened from a workspace, the page follows it; opened from Apps, the
  // person picks one of the workspaces the Dev flow is on in.
  let chosen = null, choices = null;
  const workspace = () => ctxDoor?.current()?.workspace || chosen;
  const TABS = ["board", "checks", "activity"];
  let tab = "board", data = {}, error = "", loading = true, timer = 0;

  const SETUP_TASK = "Set up the Dev flow for this repository: follow the dev-flow-setup guide (the Dev flow guide tool, topic dev-flow-setup, or the skill of that name). Bind it only to commands the project already runs, add the Dev flow block to AGENTS.md, commit on a branch of your own, and stop: the person reviews the checks before anything runs.";

  async function request(path, body) {
    if (!proc) throw new Error("The Dev flow's process is not running.");
    const r = await proc.request(path, body ? { method: "POST", body } : { method: "GET" });
    let d; try { d = JSON.parse(r.body); } catch { throw new Error("The Dev flow returned an unreadable answer."); }
    if (r.status >= 400) throw new Error(d.error || "The Dev flow could not read this workspace.");
    return d;
  }

  async function load() {
    clearTimeout(timer);
    const w = workspace();
    if (!w) {
      try {
        choices = (await request("/page/workspaces")).workspaces;
        if (choices.length === 1) { chosen = choices[0]; return load(); }
        error = "";
      } catch (e) { error = e.message; }
      loading = false; root.setAttribute("aria-busy", "false"); render(); return;
    }
    try {
      data[tab] = await request("/page/" + tab + "?workspace=" + encodeURIComponent(w.id));
      error = "";
    } catch (e) { error = e.message; }
    loading = false;
    root.setAttribute("aria-busy", "false");
    render();
    // Git has no feed event (a commit, a new worktree): read again while
    // the page is visible. Runs arrive as extension events below.
    timer = setTimeout(() => { if (!document.hidden) load(); else timer = setTimeout(load, 15000); }, 15000);
  }
  events?.onChange((ev) => { if (ev && /\.runs$/.test(ev.type || "")) load(); });
  ctxDoor?.onChange(() => { data = {}; loading = true; render(); load(); });
  document.addEventListener("visibilitychange", () => { if (!document.hidden) load(); });

  // ---- pieces ------------------------------------------------------------
  const ago = (unix) => {
    if (!unix) return "";
    const s = Math.max(0, Date.now() / 1000 - unix);
    if (s < 90) return "just now";
    if (s < 3600) return Math.round(s / 60) + " min ago";
    if (s < 86400) return Math.round(s / 3600) + " h ago";
    return Math.round(s / 86400) + " days ago";
  };
  const agoIso = (iso) => (iso ? ago(Date.parse(iso) / 1000) : "");
  const dur = (s) => (s < 60 ? s + " s" : Math.floor(s / 60) + " min " + (s % 60) + " s");
  const chip = (is, text, title) => text ? `<span class="chip is-${esc(is)}"${title ? ` title="${esc(title)}"` : ""}><span class="dot" aria-hidden="true"></span>${esc(text)}</span>` : `<span class="muted">—</span>`;

  function empty(title, line, action, id) {
    return `<section class="empty"><h2>${esc(title)}</h2><p>${esc(line)}</p>${action ? `<button type="button" class="primary" data-act="${esc(id)}">${esc(action)}</button>` : ""}</section>`;
  }

  function gated(d) {
    switch (d.state) {
      case "no-contract":
        return empty("Set up the Dev flow", "This project has no checks yet. An agent reads it and proposes them; you review them before anything runs.", ws ? "Set up with an agent" : "", "setup");
      case "untrusted":
        return empty("Review this project's checks", "This project's .picode.json changed since it was trusted, so the Dev flow shows nothing until you review it.", ws ? "Review" : "", "review");
      case "invalid":
        return empty("The Dev flow settings need a fix", d.message || "The extensions.picode-devflow section of .picode.json could not be read.", ws ? "Open settings" : "", "review");
      case "not-git":
        return empty("This folder is not a git repository", "The Dev flow follows branches, worktrees and merges, so it needs git.", "", "");
    }
    return "";
  }

  function tabs() {
    const names = { board: "Board", checks: "Checks", activity: "Activity" };
    return `<div class="seg" role="tablist" aria-label="Dev flow">${TABS.map((t) => `<button type="button" role="tab" aria-selected="${t === tab}" data-tab="${t}">${names[t]}</button>`).join("")}</div>`;
  }

  function board(d) {
    const rows = d.inFlight || [];
    const count = d.checks ? d.checks + (d.checks === 1 ? " check" : " checks") : "No checks";
    const head = `<p class="summary">${esc(count)}${d.full ? ' + a full gate before landing' : ""} · lands on <span class="mono">${esc(d.main)}</span></p>`;
    const table = rows.length
      ? `<div class="table" role="table" aria-label="In flight">
          <div class="tr th" role="row"><span role="columnheader">Branch</span><span role="columnheader">Agent</span><span role="columnheader">vs ${esc(d.main)}</span><span role="columnheader">Checks</span><span role="columnheader">Close</span><span role="columnheader">Land</span></div>
          ${rows.map((r) => `<div class="tr" role="row">
            <span class="mono branch" title="${esc(r.path)}">${esc(r.branch)}</span>
            <span class="agent">${r.agent ? esc(r.agent) : '<span class="muted">No agent</span>'}</span>
            <span class="muted">${esc(r.ahead)} ahead${r.behind ? " · " + esc(r.behind) + " behind" : ""}${r.uncommitted ? `<span class="sub">${esc(r.uncommitted)} uncommitted</span>` : ""}</span>
            <span data-label="Checks">${r.stalled ? chip("owed", "Stalled · " + ago(r.lastCommit)) : chip(r.checks.is, r.checks.text)}</span>
            <span data-label="Close">${chip(r.close.is, r.close.text, r.close.title)}</span>
            <span data-label="Land">${chip(r.land.is, r.land.text)}</span>
          </div>`).join("")}
        </div>`
      : `<div class="tile"><span>No change in flight: agents' branches show here while they work.</span>${ws ? '<button type="button" data-act="agent">New agent</button>' : ""}</div>`;
    const topics = (d.topics || []).length
      ? (d.topics).map((t) => `<li><div class="t-head"><span class="mono">${esc(t.name)}</span><span class="muted">${esc(t.open)} open</span></div>${t.next.length ? `<ul>${t.next.map((n) => `<li>${esc(n)}</li>`).join("")}${t.open > t.next.length ? `<li class="more">+${t.open - t.next.length} more in ${esc(t.name)}.md</li>` : ""}</ul>` : ""}</li>`).join("")
      : `<li class="muted">Nothing open.</li>`;
    const notes = (d.notes || []).length
      ? d.notes.map((n) => `<li><div class="t-head"><span class="title">${esc(n.title)}</span><span class="muted">${esc(n.date)}</span></div></li>`).join("")
      : `<li class="muted">No notes this week.</li>`;
    const h = d.hooks;
    const guard = !h ? "" : h.on
      ? `<div class="tile"><span>The branch <span class="mono">${esc(d.main)}</span> is protected here: agents can't commit to it or rewrite its history.</span><button type="button" data-act="hooks-off">Turn off protection</button></div>`
      : `<div class="tile"><span>${h.reset ? "Protection of " + esc(d.main) + " was switched off by another tool. Agents can commit straight to it again." : "Agents can still commit straight to " + esc(d.main) + " in this project."}</span><button type="button" data-act="hooks-on">Protect ${esc(d.main)}</button></div>`;
    return `${head}${guard}<h2>In flight</h2>${table}
      <div class="cols2"><div><h2>Next up</h2><ul class="list">${topics}</ul></div><div><h2>Recent notes</h2><ul class="list">${notes}</ul></div></div>`;
  }

  function checks(d) {
    const rows = d.checks || [];
    if (!rows.length) return `<div class="tile"><span>No checks yet: only the Dev flow's own rules run until you add checks in Settings.</span>${ws ? '<button type="button" data-act="review">Open settings</button>' : ""}</div>`;
    return `<div class="table checks" role="table" aria-label="Checks">
      <div class="tr th" role="row"><span role="columnheader">Check</span><span role="columnheader">Runs</span><span role="columnheader">When these change</span><span role="columnheader">Last run</span><span role="columnheader">Flaky</span></div>
      ${rows.map((c) => `<div class="tr" role="row">
        <span class="name">${esc(c.name)}</span>
        <span class="mono cmd" title="${esc(c.run)}">${esc(c.run)}</span>
        <span>${c.only === "full" ? '<span class="muted">Full gate only</span>' : c.when.length ? c.when.map((g) => `<span class="glob mono">${esc(g)}</span>`).join("") : '<span class="muted">Every change</span>'}</span>
        <span>${c.last ? chip(c.last.ok ? "passed" : "failed", (c.last.ok ? "Passed" : c.last.timedOut ? "Timed out" : "Failed") + " · " + dur(c.last.seconds)) + `<span class="muted sub">${esc(agoIso(c.last.at))}${c.last.branch ? " on " + esc(c.last.branch) : ""}</span>` : '<span class="muted">Not run yet</span>'}</span>
        <span>${c.flaky ? chip("owed", String(c.flaky)) : '<span class="muted">—</span>'}</span>
      </div>`).join("")}
    </div>
    <p class="muted foot">Every close also applies the Dev flow's own rules${d.rules ? " and " + esc(d.rules) + " of this project's" : ""}: notes, debts and records stay complete.</p>`;
  }

  function activity(d) {
    const rows = d.runs || [];
    if (!rows.length) return `<div class="tile"><span>No runs yet. Checks, closes and lands show here when agents run them.</span></div>`;
    const word = { check: "Check", close: "Close", land: "Land" };
    return `<ul class="list runs">${rows.map((r) => `<li class="run">
      <span class="kind">${esc(word[r.kind] || r.kind)}</span>
      <span class="mono branch">${esc(r.branch || r.worktree)}</span>
      ${chip(r.is || (r.ok ? "passed" : "failed"), r.summary, r.title)}
      <span class="muted when">${esc(agoIso(r.started))}${r.seconds != null && r.state !== "running" ? " · " + dur(r.seconds) : ""}</span>
    </li>`).join("")}</ul>`;
  }

  function picker() {
    if (!choices || !choices.length) return empty("Turn the Dev flow on in a workspace", "It follows the branches, checks and notes of a workspace's repository. Turn it on from the extension's page.", "Open the extension", "ext");
    return `<section class="empty"><h2>Choose a workspace</h2><p>The Dev flow is on in ${choices.length} workspaces.</p><div class="pick">${choices.map((c) => `<button type="button" data-pick="${esc(c.id)}">${esc(c.name)}</button>`).join("")}</div></section>`;
  }

  function render() {
    if (!workspace() && !loading && !error) { root.innerHTML = `<header class="head"><h1>Dev flow</h1></header>${picker()}`; return; }
    const d = data[tab];
    let body;
    if (!d && !error) body = `<div class="skel" aria-label="Reading"><div class="skel-line w70"></div><div class="skel-line w55"></div><div class="skel-line w40"></div></div>`;
    else if (!d) body = `<section class="empty"><h2>The Dev flow is not answering</h2><p>${esc(error)}</p><button type="button" data-act="retry">Try again</button></section>`;
    else body = gated(d) || (tab === "board" ? board(d) : tab === "checks" ? checks(d) : activity(d));
    const ready = (d && d.state === "ready") || (!d && data.board && data.board.state === "ready");
    // Inside a workspace the host's tab already says "Dev flow".
    const h1 = ctxDoor?.current()?.workspace ? '<h1 class="visually-hidden">Dev flow</h1>' : "<h1>Dev flow</h1>";
    root.innerHTML = `<header class="head">${h1}${ready || tab !== "board" ? tabs() : ""}</header>${error && d ? `<p class="stale" role="status">Showing the last answer: ${esc(error)}</p>` : ""}${body}`;
  }

  root.addEventListener("click", (e) => {
    const t = e.target.closest("[data-tab]");
    if (t) { tab = t.dataset.tab; render(); load(); return; }
    const p = e.target.closest("[data-pick]");
    if (p) { chosen = (choices || []).find((c) => c.id === p.dataset.pick) || null; data = {}; loading = true; render(); load(); return; }
    const a = e.target.closest("[data-act]");
    if (!a) return;
    const w = workspace();
    switch (a.dataset.act) {
      case "setup": ws?.newAgent({ prompt: SETUP_TASK, name: "Dev flow setup" }); break;
      case "agent": ws?.newAgent({}); break;
      case "review": ws?.settings({ section: "scripts" }); break;
      case "retry": loading = true; render(); load(); break;
      case "hooks-on": case "hooks-off": {
        const on = a.dataset.act === "hooks-on";
        a.disabled = true; a.textContent = on ? "Turning on…" : "Turning off…";
        request("/page/hooks?workspace=" + encodeURIComponent(w.id), { on })
          .then(() => { live.textContent = on ? "Git protection is on." : "Git protection is off."; load(); })
          .catch(() => { error = on ? "Couldn't protect it. Try again." : "Couldn't turn protection off. Try again."; render(); });
        break;
      }
      case "ext": links?.open(location.origin + "/#/extensions/picode-devflow"); break;
    }
    if (w) live.textContent = "";
  });
  root.addEventListener("keydown", (e) => {
    const t = e.target.closest("[role=tab]");
    if (!t || (e.key !== "ArrowRight" && e.key !== "ArrowLeft")) return;
    const i = (TABS.indexOf(tab) + (e.key === "ArrowRight" ? 1 : TABS.length - 1)) % TABS.length;
    tab = TABS[i]; render(); load();
    root.querySelector(`[data-tab="${tab}"]`)?.focus();
  });

  render();
  load();
})();
