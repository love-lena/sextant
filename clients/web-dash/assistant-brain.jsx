/* assistant-brain.jsx — the floating assistant's local answering (TASK-203).

   The assistant is a DE-NAMED helper ("Assistant · always here") — never a person,
   never an agent on the bus. It answers quick questions about the state of YOUR
   workspace from the data the dash has already loaded (goals, criteria, artifacts,
   agents, conversations) — no model call, no bus round-trip. It is distinct from
   work agents: it can't do work, it just tells you where things stand.

   Post-pivot (S20.3 adapted): answers embed [[wikilinks]] that resolve to the named
   goal / run / artifact / surface and navigate on click. The renderer in
   sidebar.jsx (renderMessageHTML) already turns [[name]] into a clickable artlink
   when `name` is a known target — so the brain just emits [[name]] and the panel
   wires the click. We pass the union of artifact + goal + surface names as the
   "known" set so a goal/run/surface wikilink resolves too.

   With NO data (a blank-slate workspace) the assistant SAYS SO and points you at
   defining a goal — it never fabricates state.

   Exports (window): SxAssistant.answer(query, data) → { text } and
   SxAssistant.knownLinks(data) → string[] (the wikilink target allow-list). */
(function () {
  // classify(q): which intent the question is closest to. Keyword buckets, lower-
  // cased; first match wins in priority order (waiting > goals > agents > artifacts
  // > greeting > help). Deliberately simple — this is a local helper, not an LLM.
  function classify(q) {
    const s = " " + q.toLowerCase().trim() + " ";
    const has = (...ws) => ws.some((w) => s.indexOf(w) >= 0);
    if (has("wait", "blocked", "stuck", "need", "my turn", "on me", "review", "sign off", "sign-off")) return "waiting";
    if (has("goal", "criteri", "objective", "north star", "north-star", "progress", "on track", "where do", "where are", "stand")) return "goals";
    if (has("agent", "who is", "who's", "working on", "workstream", "crew", "running")) return "agents";
    if (has("artifact", "doc", "document", "draft", "file")) return "artifacts";
    if (has("hello", "hi ", "hey", "yo ")) return "greeting";
    return "summary";
  }

  // The set of names a [[wikilink]] in an answer may resolve to: artifact names,
  // goal names, goal.<id> ids, and the five surface labels. The panel's renderer
  // makes a wikilink clickable only when its name is in this set.
  function knownLinks(data) {
    const out = [];
    for (const a of (data.artifacts || [])) if (a && a.name) out.push(a.name);
    for (const g of (data.goals || [])) {
      if (g && g.name) out.push(g.name);
      if (g && g.id) out.push("goal." + g.id);
    }
    for (const s of ["Home", "Goals", "Work engine", "Artifacts", "Bus"]) out.push(s);
    return out;
  }

  function isEmpty(data) {
    return (!(data.goals || []).length) && (!(data.artifacts || []).length) && (!(data.agents || []).length);
  }

  // The blank-slate answer: say there's nothing here, and point at defining a goal.
  function emptyAnswer() {
    return { text:
      "There's nothing in your workspace yet — no goals, no artifacts, no agents on the bus.\n\n" +
      "The best place to start is a **goal**: a north-star sentence plus the acceptance criteria that say when it's met. Open [[Goals]] to define one, and I'll be able to tell you where things stand from there." };
  }

  // ---- intent answers. Each returns { text } with [[wikilinks]] woven in. ----

  function answerWaiting(data) {
    const goals = data.goals || [];
    const waitingCrit = [];
    const blockedCrit = [];
    for (const g of goals) for (const c of (g.criteria || [])) {
      if (c.status === "waiting-on-you") waitingCrit.push({ g, c });
      else if (c.status === "blocked") blockedCrit.push({ g, c });
    }
    const reviewGoals = goals.filter((g) => g.review === "review");
    const reviewArts = (data.artifacts || []).filter((a) => a.status === "review");
    const lines = [];
    if (waitingCrit.length) {
      lines.push("**" + waitingCrit.length + (waitingCrit.length > 1 ? " criteria are" : " criterion is") + " waiting on you:**");
      for (const { g, c } of waitingCrit.slice(0, 6)) lines.push("• " + (c.label || c.id || "a criterion") + " — on [[" + g.name + "]]");
    }
    if (reviewGoals.length) lines.push((lines.length ? "\n" : "") + reviewGoals.length + " goal" + (reviewGoals.length > 1 ? "s" : "") + " awaiting your sign-off: " + reviewGoals.map((g) => "[[" + g.name + "]]").join(", ") + ".");
    if (reviewArts.length) lines.push((lines.length ? "\n" : "") + reviewArts.length + " artifact" + (reviewArts.length > 1 ? "s" : "") + " need review: " + reviewArts.slice(0, 6).map((a) => "[[" + a.name + "]]").join(", ") + ".");
    if (blockedCrit.length) lines.push((lines.length ? "\n" : "") + "**Blocked:** " + blockedCrit.slice(0, 5).map(({ g }) => "[[" + g.name + "]]").join(", ") + ".");
    if (!lines.length) return { text: "Nothing is waiting on you right now — no criteria flagged, no goals or artifacts pending your review. You're clear. ✓\n\nIf you want to see the full picture, open [[Goals]]." };
    lines.push("\nOpen [[Goals]] to act on these.");
    return { text: lines.join("\n") };
  }

  function answerGoals(data) {
    const goals = data.goals || [];
    if (!goals.length) return { text: "You don't have any goals defined yet. A goal is a north-star sentence plus the criteria that say when it's met — open [[Goals]] to define your first one." };
    const lines = ["You have **" + goals.length + " goal" + (goals.length > 1 ? "s" : "") + "**:"];
    for (const g of goals.slice(0, 8)) {
      const crits = g.criteria || [];
      const met = crits.filter((c) => c.status === "met").length;
      const waiting = crits.filter((c) => c.status === "waiting-on-you").length;
      const blocked = crits.some((c) => c.status === "blocked");
      let where;
      if (!g.northstar || crits.length === 0) where = "not defined yet";
      else if (met === crits.length) where = "met ✓";
      else if (blocked) where = "blocked";
      else if (waiting) where = waiting + " waiting on you";
      else where = met + " of " + crits.length + " criteria met";
      lines.push("• [[" + g.name + "]] — " + where);
    }
    lines.push("\nOpen [[Goals]] for the full portfolio and the per-criterion detail.");
    return { text: lines.join("\n") };
  }

  function answerAgents(data) {
    const agents = data.agents || [];
    if (!agents.length) return { text: "No agents are connected to the bus right now. When work is mobilized, the agents running it show up on [[Bus]] and in the workstreams here." };
    const working = agents.filter((a) => a.state === "working");
    const lines = [];
    if (working.length) {
      lines.push("**" + working.length + " agent" + (working.length > 1 ? "s are" : " is") + " working:**");
      for (const a of working.slice(0, 6)) lines.push("• " + a.name + (a.headline ? " — " + a.headline : ""));
    } else {
      lines.push("No agents are actively working right now (" + agents.length + " connected, idle or done).");
    }
    lines.push("\nSee the live traffic on [[Bus]].");
    return { text: lines.join("\n") };
  }

  function answerArtifacts(data) {
    const arts = data.artifacts || [];
    if (!arts.length) return { text: "There are no artifacts in your workspace yet. Artifacts are the shared docs and outputs agents produce — open [[Artifacts]] once there are some." };
    const review = arts.filter((a) => a.status === "review");
    const lines = ["You have **" + arts.length + " artifact" + (arts.length > 1 ? "s" : "") + "**."];
    if (review.length) lines.push(review.length + " need" + (review.length === 1 ? "s" : "") + " your review: " + review.slice(0, 6).map((a) => "[[" + a.name + "]]").join(", ") + ".");
    else lines.push("A few recent ones: " + arts.slice(0, 5).map((a) => "[[" + a.name + "]]").join(", ") + ".");
    lines.push("\nBrowse them all on [[Artifacts]].");
    return { text: lines.join("\n") };
  }

  // The catch-all "where do things stand" summary — the most useful default.
  function answerSummary(data) {
    const goals = data.goals || [];
    const arts = data.artifacts || [];
    const agents = data.agents || [];
    const working = agents.filter((a) => a.state === "working").length;
    let waiting = 0, blocked = 0;
    for (const g of goals) for (const c of (g.criteria || [])) {
      if (c.status === "waiting-on-you") waiting++;
      else if (c.status === "blocked") blocked++;
    }
    const reviewGoals = goals.filter((g) => g.review === "review").length;
    const reviewArts = arts.filter((a) => a.status === "review").length;
    const lines = ["Here's where things stand:"];
    lines.push("• **" + goals.length + "** goal" + (goals.length === 1 ? "" : "s") + (goals.length ? " — open [[Goals]]" : ""));
    if (waiting || reviewGoals || reviewArts) lines.push("• **" + (waiting + reviewGoals + reviewArts) + "** thing" + (waiting + reviewGoals + reviewArts === 1 ? "" : "s") + " waiting on you");
    if (blocked) lines.push("• **" + blocked + "** blocked");
    lines.push("• **" + working + "** agent" + (working === 1 ? "" : "s") + " working · **" + arts.length + "** artifact" + (arts.length === 1 ? "" : "s"));
    lines.push("\nAsk me \"what's waiting on me?\" or \"how are my goals doing?\" for the detail.");
    return { text: lines.join("\n") };
  }

  // answer(query, data) — the one entry point. `data` = { goals, artifacts, agents }
  // in the shapes the dash already holds (goalViews / artItems / agents).
  function answer(query, data) {
    data = data || {};
    if (isEmpty(data)) return emptyAnswer();
    switch (classify(query || "")) {
      case "waiting":   return answerWaiting(data);
      case "goals":     return answerGoals(data);
      case "agents":    return answerAgents(data);
      case "artifacts": return answerArtifacts(data);
      case "greeting":  return { text: "Hi — I'm your workspace assistant. I can see your goals, artifacts, and the agents on the bus. Ask me what's waiting on you, how your goals are doing, or where a workstream stands." };
      default:          return answerSummary(data);
    }
  }

  window.SxAssistant = { answer, knownLinks, classify, isEmpty };
})();
