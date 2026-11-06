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
  const TABS = ["board", "queue", "checks", "activity"];
  const DOCK = !!window.DF_DOCK;
  let tab = DOCK ? "queue" : "board", data = {}, error = "", loading = true, timer = 0;
  let menuFor = "", confirmFor = "", note = "";

  const busy = new Map(); // ticket -> "Moving…" | "Stopping…"

  const SETUP_TASK = "Set up the Dev flow for this repository: follow the dev-flow-setup guide (the Dev flow guide tool, topic dev-flow-setup, or the skill of that name). Bind it only to commands the project already runs, add the Dev flow block to AGENTS.md, commit on a branch of your own, and stop: the person reviews the checks before anything runs.";

  async function request(path, body) {
    if (!proc) throw new Error("The Dev flow's process is not running.");
    const r = await proc.request(path, body ? { method: "POST", body } : { method: "GET" });
    let d; try { d = JSON.parse(r.body); } catch { throw new Error("The Dev flow returned an unreadable answer."); }
    if (r.status >= 400) throw new Error(d.error || "The Dev flow could not read this workspace.");
    return d;
  }

  // A read that a newer one overtook drops its answer (several triggers:
  // the timer, run events, the context, visibility).
  let seq = 0, shownWorkspace = "";
  async function load() {
    clearTimeout(timer);
    const mine = ++seq;
    const w = workspace();
    if (!w) {
      try {
        choices = (await request("/page/workspaces")).workspaces;
        if (choices.length === 1) { chosen = choices[0]; return load(); }
        error = "";
      } catch (e) { error = e.message; }
      loading = false; root.setAttribute("aria-busy", "false"); render(); return;
    }
    shownWorkspace = w.id;
    const want = tab;
    try {
      const d = await request("/page/" + want + "?workspace=" + encodeURIComponent(w.id));
      if (mine !== seq) return;
      data[want] = d;
      error = "";
    } catch (e) {
      if (mine !== seq) return;
      error = e.message;
    }
    loading = false;
    root.setAttribute("aria-busy", "false");
    render();
    // Git has no feed event (a commit, a new worktree): read again while
    // the page is visible. Runs arrive as extension events below.
    // The queue moves on its own (other lands): read it more often.
    const every = tab === "queue" ? 5000 : 15000;
    timer = setTimeout(() => { if (!document.hidden) load(); else timer = setTimeout(load, every); }, every);
  }
  events?.onChange((ev) => { if (ev && /\.runs$/.test(ev.type || "")) load(); });
  // The context door also announces changes that keep the same workspace
  // (the dock re-reads it often): only another workspace empties the page;
  // the same one reads again with the last answer still on screen.
  ctxDoor?.onChange(() => {
    const id = workspace()?.id || "";
    if (id === shownWorkspace) { load(); return; }
    shownWorkspace = id;
    data = {}; loading = true; render(); load();
  });
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
  const ICONS = {
    earlier: '<path d="M8 13V3M3.5 7.5 8 3l4.5 4.5"/>',
    later: '<path d="M8 3v10M3.5 8.5 8 13l4.5-4.5"/>',
    first: '<path d="M3 2.5h10M8 14V6M3.5 10.5 8 6l4.5 4.5"/>',
    more: '<circle cx="3.5" cy="8" r="1.2"/><circle cx="8" cy="8" r="1.2"/><circle cx="12.5" cy="8" r="1.2"/>',
  };
  const icon = (n) => `<svg viewBox="0 0 16 16" aria-hidden="true" class="${n === "more" ? "fill" : ""}">${ICONS[n]}</svg>`;
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
    const names = { board: "Board", queue: "Queue", checks: "Checks", activity: "Activity" };
    return `<div class="seg" role="tablist" aria-label="Dev flow">${TABS.map((t) => `<button type="button" role="tab" aria-selected="${t === tab}" data-tab="${t}">${names[t]}</button>`).join("")}</div>`;
  }

  function board(d) {
    const rows = d.inFlight || [];
    const count = d.checks ? d.checks + (d.checks === 1 ? " check" : " checks") : "No checks";
    const head = `<p class="summary">${esc(count)}${d.full ? ' + a full gate before landing' : ""} · lands on <span class="mono">${esc(d.main)}</span></p>`;
    const landChip = (r) => chip(r.land.is, r.land.text);
    const rowMenu = (r) => {
      const queued = r.land.is === "landing" || /^In queue/.test(r.land.text || "");
      const at = `data-path="${esc(r.path)}" data-branch="${esc(r.branch)}"`;
      return `<div class="menu" role="menu" aria-label="${esc(r.branch)}">
        <button type="button" role="menuitem" data-act="m-check" ${at}>Run checks</button>
        <button type="button" role="menuitem" data-act="m-close" ${at}>Close</button>
        ${queued ? '<button type="button" role="menuitem" data-act="m-queue">Show in queue</button>' : d.land ? `<button type="button" role="menuitem" data-act="m-land" ${at}>Land</button>` : ""}
        ${r.agentId ? `<hr><button type="button" role="menuitem" data-act="m-agent" data-agent="${esc(r.agentId)}">Open agent</button>` : ""}
      </div>`;
    };
    const table = rows.length
      ? `<div class="table board" role="table" aria-label="In flight">
          <div class="tr th" role="row"><span role="columnheader">Branch</span><span role="columnheader">Agent</span><span role="columnheader">vs ${esc(d.main)}</span><span role="columnheader">Checks</span><span role="columnheader">Close</span><span role="columnheader">Land</span><span role="columnheader"><span class="visually-hidden">Actions</span></span></div>
          ${rows.map((r) => `<div class="tr" role="row">
            <span class="mono branch" title="${esc(r.path)}">${esc(r.branch)}</span>
            <span class="agent">${r.agent ? esc(r.agent) : '<span class="muted">No agent</span>'}</span>
            <span class="muted">${esc(r.ahead)} ahead${r.behind ? " · " + esc(r.behind) + " behind" : ""}${r.uncommitted ? `<span class="sub">${esc(r.uncommitted)} uncommitted</span>` : ""}</span>
            <span data-label="Checks">${r.stalled ? chip("owed", "Stalled · " + ago(r.lastCommit)) : chip(r.checks.is, r.checks.text)}</span>
            <span data-label="Close">${chip(r.close.is, r.close.text, r.close.title)}</span>
            <span data-label="Land">${landChip(r)}</span>
            <span class="row-acts"><button type="button" class="icon" data-act="menu" data-branch="${esc(r.branch)}" aria-haspopup="menu" aria-expanded="${menuFor === r.branch}" aria-label="Actions: ${esc(r.branch)}" title="Actions">${icon("more")}</button>${menuFor === r.branch ? rowMenu(r) : ""}</span>
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

  // ---- queue ---------------------------------------------------------------
  const KIND = { check: "Check", close: "Close", land: "Land", merge: "Merge" };
  function runRow(v, done) {
    return `<li class="run">
        <span class="kind">${esc(KIND[v.kind] || v.kind)}</span>
        <span class="main-col"><span class="mono branch">${esc(v.branch || "—")}</span>${v.title ? `<span class="muted sub" title="${esc(v.title)}">${esc(v.title.split("\n")[0])}</span>` : ""}</span>
        ${chip(v.is, done && v.seconds ? v.text + " · " + dur(v.seconds) : v.text)}
        <span class="muted when">${esc(agoIso(done ? v.ended || v.started : v.started))}</span>
      </li>`;
  }
  function queue(d) {
    const red = d.red ? `<div class="tile warn" role="status"><span>${esc(d.red)}</span></div>` : "";
    const running = d.running.length ? `<h2>Running now</h2><ul class="list runs">${d.running.map((v) => runRow(v, false)).join("")}</ul>` : "";
    const MOVE = { earlier: "Move earlier", later: "Move later", first: "First" };
    let rows;
    if (!d.bound) rows = `<div class="tile"><span>This project's contract does not say how to read its queue, so only the Dev flow's own lands show here.</span>${ws ? '<button type="button" data-act="review">Open settings</button>' : ""}</div>`;
    else if (d.error) rows = `<div class="tile" role="alert"><span>The queue could not be read: ${esc(d.error)}</span><button type="button" data-act="retry">Try again</button></div>`;
    else if (!d.rows.length) rows = `<div class="tile"><span>Nothing waits to land on ${esc(d.main)}.</span></div>`;
    else rows = `<div class="table queue" role="table" aria-label="Land queue">
          <div class="tr th" role="row"><span role="columnheader">#</span><span role="columnheader">Branch</span><span role="columnheader">Agent</span><span role="columnheader">Waiting</span><span role="columnheader">State</span><span role="columnheader"><span class="visually-hidden">Actions</span></span></div>
          ${d.rows.map((r, i) => {
            const b = busy.get(r.ticket);
            const acts = b ? `<span class="muted busy">${esc(b)}</span>` : ["earlier", "later", "first"].map((m) => `<button type="button" class="icon" data-act="q-${m}" data-ticket="${esc(r.ticket)}" aria-label="${MOVE[m]}: ${esc(r.branch)}" title="${MOVE[m]}" ${r.actions.includes(m) ? "" : "disabled"}>${icon(m)}</button>`).join("") + (r.actions.includes("stop") ? `<button type="button" class="danger" data-act="q-stop" data-ticket="${esc(r.ticket)}" aria-label="Stop: ${esc(r.branch)}">Stop…</button>` : '<span class="stop-slot" aria-hidden="true"></span>');
            return `<div class="tr" role="row" aria-busy="${b ? "true" : "false"}">
            <span class="ord muted">${i + 1}</span>
            <span class="main-col"><span class="mono branch">${esc(r.branch)}</span>${r.reason ? `<span class="muted sub">${esc(r.reason)}</span>` : ""}</span>
            <span class="agent">${r.agent ? esc(r.agent) : '<span class="muted">No agent</span>'}</span>
            <span class="muted">${esc(r.age)}</span>
            <span data-label="State">${chip(r.is, r.text)}</span>
            <span class="q-acts${r.actions.length || b ? "" : " none"}">${acts}</span>
          </div>${confirmFor === r.ticket && !b ? `<div class="tr confirm" role="row"><span>Stop <span class="mono">${esc(r.branch)}</span>? It leaves the queue; the branch and its agent stay.</span><span class="confirm-acts"><button type="button" class="danger-fill" data-act="q-stop-yes" data-ticket="${esc(r.ticket)}">Stop</button><button type="button" data-act="q-stop-no">Keep</button></span></div>` : ""}`;
          }).join("")}
        </div>`;
    const verdicts = `<h2>Recent verdicts</h2>${d.verdicts.length ? `<ul class="list runs">${d.verdicts.map((v) => runRow(v, true)).join("")}</ul>` : `<div class="tile"><span>No verdict yet: checks, closes and lands show here when they end.</span></div>`}`;
    const name = workspace()?.name || "";
    const pick = DOCK && choices && choices.length > 1
      ? `<select class="wspick" data-act="dock-pick" aria-label="Workspace">${choices.map((c) => `<option value="${esc(c.id)}"${c.id === workspace()?.id ? " selected" : ""}>${esc(c.name)}</option>`).join("")}</select>`
      : esc(name);
    const landing = d.rows.filter((r) => r.state === "landing").length;
    const head = DOCK ? `<p class="summary">${pick} · lands on <span class="mono">${esc(d.main)}</span>${d.bound ? ` · ${landing ? landing + " landing · " : ""}${d.rows.length - landing} in line` : ""}</p>` : "";
    const foot = !DOCK && d.merge ? `<p class="muted foot">Changes land through Merge: order and stop them on their Merge cards.</p>` : !DOCK && d.bound && d.land ? `<p class="muted foot">Order kept by <span class="mono">${esc(d.land.replace("{branch}", "<branch>"))}</span>; a person may move a ticket.</p>` : "";
    return `${head}${red}${running}<h2>Land queue</h2>${rows}${verdicts}${foot}`;
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
    else body = gated(d) || (tab === "board" ? board(d) : tab === "queue" ? queue(d) : tab === "checks" ? checks(d) : activity(d));
    const ready = (d && d.state === "ready") || (!d && data.board && data.board.state === "ready");
    root.classList.toggle("in-dock", DOCK);
    const noteLine = note ? `<p class="stale" role="alert">${esc(note)}</p>` : "";
    if (DOCK) { root.innerHTML = `<h1 class="visually-hidden">Land queue</h1>${noteLine}${body}`; return; }
    // Inside a workspace the host's tab already says "Dev flow".
    const h1 = ctxDoor?.current()?.workspace ? '<h1 class="visually-hidden">Dev flow</h1>' : "<h1>Dev flow</h1>";
    root.innerHTML = `<header class="head">${h1}${ready || tab !== "board" ? tabs() : ""}</header>${noteLine}${error && d ? `<p class="stale" role="status">Showing the last answer: ${esc(error)}</p>` : ""}${body}`;
  }

  root.addEventListener("click", (e) => {
    const t = e.target.closest("[data-tab]");
    if (t) { tab = t.dataset.tab; render(); load(); return; }
    const p = e.target.closest("[data-pick]");
    if (p) { chosen = (choices || []).find((c) => c.id === p.dataset.pick) || null; data = {}; loading = true; render(); load(); return; }
    const a = e.target.closest("[data-act]");
    if (!a) { if (menuFor) { menuFor = ""; render(); } return; }
    const w = workspace();
    const run = (kind) => {
      menuFor = "";
      const verb = { check: "Checks", close: "Close", land: "Land" }[kind];
      request("/page/run?workspace=" + encodeURIComponent(w.id), { path: a.dataset.path, kind })
        .then(() => { live.textContent = verb + " started on " + a.dataset.branch + "."; load(); })
        .catch((e) => { note = e.message; render(); });
      note = "";
      render();
    };
    const change = (ticket, action) => {
      note = "";
      busy.set(ticket, action === "stop" ? "Stopping…" : "Moving…");
      confirmFor = "";
      render();
      request("/page/queue?workspace=" + encodeURIComponent(w.id), { ticket, action })
        .then(() => { live.textContent = action === "stop" ? "Stopped." : "Moved."; })
        .catch((e) => { note = e.message; })
        .finally(() => { busy.delete(ticket); load(); });
    };
    switch (a.dataset.act) {
      case "menu": menuFor = menuFor === a.dataset.branch ? "" : a.dataset.branch; render(); root.querySelector('.menu [role=menuitem]')?.focus(); return;
      case "m-check": run("check"); return;
      case "m-close": run("close"); return;
      case "m-land": run("land"); return;
      case "m-agent": menuFor = ""; render(); links?.open(location.origin + "/#/agent/" + encodeURIComponent(a.dataset.agent)); return;
      case "m-queue": menuFor = ""; tab = "queue"; render(); load(); return;
      case "q-earlier": change(a.dataset.ticket, "earlier"); return;
      case "q-later": change(a.dataset.ticket, "later"); return;
      case "q-first": change(a.dataset.ticket, "first"); return;
      case "q-stop": confirmFor = a.dataset.ticket; render(); root.querySelector('[data-act="q-stop-no"]')?.focus(); return;
      case "q-stop-no": confirmFor = ""; render(); return;
      case "q-stop-yes": change(a.dataset.ticket, "stop"); return;
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
  root.addEventListener("change", (e) => {
    const sel = e.target.closest("select.wspick");
    if (!sel) return;
    chosen = (choices || []).find((c) => c.id === sel.value) || chosen;
    data = {}; loading = true; render(); load();
  });
  root.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && (menuFor || confirmFor)) { const b = menuFor; menuFor = ""; confirmFor = ""; render(); if (b) root.querySelector(`[data-act="menu"][data-branch="${CSS.escape(b)}"]`)?.focus(); return; }
    const t = e.target.closest("[role=tab]");
    if (!t || (e.key !== "ArrowRight" && e.key !== "ArrowLeft")) return;
    const i = (TABS.indexOf(tab) + (e.key === "ArrowRight" ? 1 : TABS.length - 1)) % TABS.length;
    tab = TABS[i]; render(); load();
    root.querySelector(`[data-tab="${tab}"]`)?.focus();
  });

  render();
  load();
})();
