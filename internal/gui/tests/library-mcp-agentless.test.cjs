// Run with Node's test runner and Playwright on the module path; see README.md.
// treeman_26897 on Discord: magpie run as an MCP entry point for remote
// clients has no agents, and the MCP router could only be turned on with
// one to write to: the Library said "No agents to write to", and in
// gateway mode (magpie web on such a server) it wasn't there at all.
// With no agent, or in gateway mode, the Library is its MCP tab (beside
// Instructions and Skills, for a Remote magpie's clients): the servers with
// no agent chips, nothing from agents' own configs, and the
// router's switch, there before the first server too. Its lines say any
// app connects at the address; a click posts servers/hub on, says the
// router is on, counts what it serves and shows the address, and Connect…
// has Other apps alone, with the gateway key a remote app needs. In every
// language, both engines, the app with no agents at 1100px and a browser
// in gateway mode at 420px: no sideways scroll, no native select.
// No backend: the API is faked here.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright-core");
const { strings } = require("./strings.cjs");

const assets = path.resolve(__dirname, "../assets");
const HOME = "/srv/mg";
const fixture = (st) => ({
  dir: `${HOME}/.magpie/library`, backups: `${HOME}/.magpie/backups`, home: HOME,
  mcpHub: st.on, hubPath: "/mcp/magpie/hub/",
  // in gateway mode this computer's agents are there, and not shown
  agents: st.gateway ? [{ id: "claude", name: "Claude Code", icon: "claudecode-color", skills: `${HOME}/.claude/skills`, mcp: `${HOME}/.claude.json`, ...(st.on ? { hub: "on" } : {}) }] : [],
  instructions: { agents: [], sets: [] }, foundSkills: [], skills: [], problems: [],
  foundServers: st.gateway ? [{ name: "agent-own", transport: "stdio", command: "npx", args: ["agent-own"], agents: ["claude"] }] : [],
  projects: st.gateway ? [{ path: `${HOME}/code/app`, servers: { notes: ["claude"] } }] : [],
  servers: st.none ? [] : [{ name: "notes", transport: "stdio", command: "uvx", args: ["notes-mcp"], agents: st.gateway ? ["claude"] : [] }],
});

let I18N;
function tr(lang, key, vars = {}) {
  let s = key;
  if (lang !== "en") {
    assert.equal(typeof I18N[lang][key], "string", `${lang} has no ${JSON.stringify(key)}`);
    s = I18N[lang][key];
  }
  return s.replace(/\{(\w+)\}/g, (m, k) => (k in vars ? String(vars[k]) : m));
}

function server(lang, st) {
  return async (route) => {
    const req = route.request(), url = new URL(req.url());
    const json = (data) => route.fulfill({ json: data });
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = {lang:"${lang}",theme:"light",web:${st.web}${st.gateway ? ",gateway:true" : ""}};` });
    if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/state") return json({ agents: [], profiles: [], settings: { lang, theme: "light", gateway: "http://127.0.0.1:3425", publicURL: "https://mcp.example.net" } });
    if (st.detailRequests && url.pathname.startsWith("/api/library")) st.detailRequests.push([req.method(), url.pathname]);
    if (url.pathname === "/api/library") return json(st.library || fixture(st));
    if (st.library && url.pathname.startsWith("/api/library/") && req.method() !== "GET") st.sent.push(url.pathname);
    if (url.pathname === "/api/library/servers/hub") {
      const body = req.postDataJSON();
      st.sent.push(body);
      st.on = body.on;
      return json({ ...fixture(st), result: { changed: [] } });
    }
    if (url.pathname === "/api/library/mcp/check") {
      const names = req.postDataJSON().names;
      st.checked.push(...names);
      return json({ servers: Object.fromEntries(names.map((n) => [n, { state: "ok", tools: 2 }])) });
    }
    if (url.pathname === "/api/plugins") return json({ plugins: [] });
    if (url.pathname === "/api/usage/quotas") return json([]);
    if (url.pathname === "/api/groups") return json({ groups: [] });
    if (url.pathname === "/api/providers") return json({ providers: [], gateway: { running: true } });
    if (url.pathname.startsWith("/api/")) return json({});
    if (url.host !== "magpie.test") return route.fulfill({ status: 404, body: "" });
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    try { await route.fulfill({ body: await fs.readFile(file), contentType }); } catch { await route.fulfill({ status: 404, body: "" }); }
  };
}

const sideways = (page) => page.evaluate(() => {
  const over = [];
  const w = document.documentElement.clientWidth;
  if (document.documentElement.scrollWidth > w) over.push("page " + document.documentElement.scrollWidth);
  for (const e of document.querySelectorAll("#view-library .lib-hub, #view-library .lib-hub .lib-switch, #view-library .lib-server, #view-library .lib-hubat, #view-library .lib-hubat .lib-hubconnect, .lib-hubsheet, .lib-hubsheet .lib-hubapp, .lib-hubsheet .lib-hubaddr, .lib-hubsheet .bar button")) {
    const r = e.getBoundingClientRect();
    if (r.width && (r.right > w + 0.5 || r.left < -0.5)) over.push((e.className || e.tagName) + " " + Math.round(r.left) + "–" + Math.round(r.right));
  }
  return over;
});

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  test(engine + ": local usage on MCP and Skill rows is a passive filter", async (t) => {
    I18N = strings().I18N;
    const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: process.env.PLAYWRIGHT_CHANNEL || "chromium" }));
    t.after(() => browser.close());
    const stats = { calls30: 0, failed30: 0, reads30: 0, lastUsed: 0, coverage: "observed", complete30: true, observedDays: 95, candidate: false };
    const cases = {
      active: { ...stats, calls30: 7, failed30: 2, reads30: 3, lastUsed: 1720000000, byAgent: [
        { agent: "claude", calls30: 5, failed30: 2, reads30: 0, lastUsed: 1720000000 },
        { agent: "cursor", calls30: 2, failed30: 0, reads30: 1, lastUsed: 1719999901 },
        { agent: "other", calls30: 0, failed30: 0, reads30: 2, lastUsed: 1719999802 },
      ] },
      quiet: { ...stats, calls30: 1, reads30: 1 }, idle: stats, review: { ...stats, candidate: true },
      reader: { ...stats, reads30: 3 }, recent: { ...stats, observedDays: 29, complete30: false }, partialQuiet: { ...stats, coverage: "partial", calls30: 1, complete30: false },
      partial: { ...stats, coverage: "partial", reads30: 4 }, unknown: { ...stats, coverage: "unknown" }, missing: undefined,
    };
    for (const lang of ["en", "zh", "zh-TW", "ja", "de"]) for (const tab of ["mcp", "skills"]) {
      await t.test(`${lang} ${tab}`, async () => {
        const st = { web: true, sent: [] }, library = fixture(st);
        const rows = Object.entries(cases).map(([name, useStats]) => ({ name, useStats, agents: [] }));
        library.skills = rows.map((r) => ({ ...r, kind: "folder" }));
        library.servers = rows.map((r) => ({ ...r, hidden: r.name === "review", transport: "stdio", command: "example" }));
        st.library = library;
        const page = await browser.newPage({ viewport: { width: 420, height: 950 }, timezoneId: "Asia/Kathmandu", reducedMotion: "reduce" });
        try {
          page.setDefaultTimeout(5000);
          page.setDefaultNavigationTimeout(60000);
          await page.addInitScript((tab) => localStorage.setItem("magpie.libTab", tab), tab);
          await page.route("**/*", server(lang, st));
          await page.goto("http://magpie.test/");
          await page.locator('button[data-view="library"]').click();
          const v = page.locator("#view-library"), list = v.locator(tab === "skills" ? ".lib-skill" : ".lib-server");
          const summary = (name) => list.filter({ has: page.locator(".name", { hasText: new RegExp("^" + name + "$") }) }).locator(".lib-usestats");
          const count = (ok, failed, reads) => tr(lang, "30 days: {ok} succeeded · {failed} failed · {reads} reads", { ok, failed, reads });
          await summary("active").waitFor();
          const lastUsed = await summary("active").locator("span").nth(1).innerText();
          assert.match(lastUsed, /2024/);
          assert.match(lastUsed, /15:31:40/);
          assert.equal(await summary("active").locator("span").first().textContent(), count(5, 2, 3));
          assert.equal(await summary("partial").locator("span").first().textContent(), count("—", "—", "≥4"));
          assert.ok((await summary("partial").innerText()).includes(tr(lang, "Partial coverage")));
          assert.equal(await summary("unknown").locator("span").first().textContent(), count("—", "—", "—"));
          assert.equal(await summary("idle").locator("span").first().textContent(), count(0, 0, 0));
          assert.equal(await summary("missing").locator("span").first().textContent(), tr(lang, "No local usage data yet"));
          const active = summary("active"), dialog = page.getByRole("dialog");
          st.detailRequests = [];
          if (tab === "skills") {
            const vr = active.locator("xpath=parent::*");
            assert.ok(await vr.evaluate((r) => r.parentElement.matches(".lib-vl") && Math.abs(r.getBoundingClientRect().height - parseFloat(r.style.height)) < 1));
          }
          await active.locator("span").first().click(); // even on the count, not only on Details
          await page.getByRole("dialog", { name: tr(lang, "Usage details: {name}", { name: "active" }) }).waitFor();
          const parts = dialog.locator("section");
          assert.deepEqual(await parts.locator("b").allTextContents(), ["Claude Code", "Cursor", tr(lang, "Unattributed")]);
          for (const [i, a] of cases.active.byAgent.entries()) {
            assert.equal(await parts.nth(i).locator("div").first().textContent(), tr(lang, "{calls} calls · {failed} failed · {reads} reads", { calls: a.calls30, failed: a.failed30, reads: a.reads30 }));
            assert.ok((await parts.nth(i).innerText()).includes(["15:31:40", "15:30:01", "15:28:22"][i]));
          }
          assert.ok(await dialog.evaluate((d) => d.clientWidth >= d.scrollWidth && d.getBoundingClientRect().right <= innerWidth));
          await page.keyboard.press("Escape");
          await dialog.waitFor({ state: "hidden" });
          for (const [name, key] of [["idle", "Enter"], ["missing", "Space"]]) {
            const b = summary(name);
            await b.focus();
            await page.keyboard.press(key);
            await dialog.getByText(tr(lang, "No per-agent usage records."), { exact: true }).waitFor();
            assert.equal(await parts.count(), 0);
            await dialog.getByRole("button", { name: tr(lang, "Done"), exact: true }).press("Enter");
            await dialog.waitFor({ state: "hidden" });
            assert.ok(await b.evaluate((n) => n === document.activeElement));
          }
          assert.ok(await summary("active").evaluate((box) => {
            const row = box.closest(".lib-row").getBoundingClientRect(), b = box.getBoundingClientRect();
            return b.left >= row.left && b.right <= row.right && b.bottom <= row.bottom && row.right <= window.innerWidth;
          }));
          assert.deepEqual(st.detailRequests, [], "viewing usage asks nothing of the library");
          const names = () => list.locator(".name").allTextContents();
          const filter = (f) => v.locator(`.lib-usefilter[data-filter="${f}"]`).click();
          await filter("low");
          assert.deepEqual((await names()).sort(), ["idle", "partialQuiet", "quiet", "review"]);
          await filter("candidate");
          assert.deepEqual(await names(), ["review"]);
          assert.ok(await summary("review").isVisible());
          assert.ok((await summary("review").innerText()).includes(tr(lang, "Cleanup candidate")));
          if (tab === "skills") {
            await v.locator(".lib-skillq").fill("quiet");
            await v.getByText(tr(lang, "No items match this filter."), { exact: true }).waitFor();
            assert.deepEqual(await names(), []);
            await v.locator(".lib-skillq").fill("");
            await summary("review").waitFor();
          }
          await filter("all");
          assert.equal((await names()).length, rows.length);
          assert.deepEqual(st.sent, []);
        } finally { await page.close(); }
      });
    }
  });

  test(engine + ": usage details on a newly drawn virtual Skill row", async (t) => {
    const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: process.env.PLAYWRIGHT_CHANNEL || "chromium" }));
    t.after(() => browser.close());
    const st = { web: true, sent: [] }, library = fixture(st);
    library.skills = Array.from({ length: 160 }, (_, i) => ({ kind: "folder", agents: [], name: `skill-${String(i).padStart(3, "0")}`,
      useStats: i % 2 ? undefined : { calls30: i, reads30: 0, failed30: i ? 1 : 0, lastUsed: 1720000000, byAgent: [{ agent: "codex", calls30: i, reads30: 0, failed30: i ? 1 : 0, lastUsed: 1720000000 }] } }));
    st.library = library;
    const page = await browser.newPage({ viewport: { width: 375, height: 800 }, reducedMotion: "reduce" });
    page.setDefaultTimeout(5000);
    page.setDefaultNavigationTimeout(60000);
    await page.addInitScript(() => {
      localStorage.setItem("magpie.libTab", "skills");
      localStorage.setItem("magpie.libSkillFolds", JSON.stringify({ local: false }));
      // Keep offscreen rows pending until scrolling actually demands them.
      window.requestIdleCallback = () => 1;
    });
    await page.route("**/*", server("en", st));
    await page.goto("http://magpie.test/");
    await page.locator('button[data-view="library"]').click();
    const view = page.locator("#view-library"), row = view.locator(".lib-vl .lib-skill").filter({ has: page.getByText("skill-120", { exact: true }) });
    const vl = view.locator(".lib-vl").first();
    await vl.locator(".lib-skill").first().waitFor();
    assert.equal(await row.count(), 0);
    await page.mouse.move(180, 540);
    await page.mouse.wheel(0, await vl.evaluate((list) => list.getBoundingClientRect().top + list.clientHeight * 0.75 - 400));
    const trigger = row.getByRole("button", { name: /Details/ });
    await trigger.waitFor();
    await trigger.scrollIntoViewIfNeeded();
    const bounds = () => row.evaluate((r) => {
      const s = r.querySelector(".lib-usestats").getBoundingClientRect(), line = r.getBoundingClientRect();
      return { y: r.closest("#view-library").scrollTop, height: line.height, fits: s.left >= line.left && s.right <= line.right && s.bottom <= line.bottom && s.top >= r.querySelector(".who").getBoundingClientRect().bottom };
    });
    const before = await bounds();
    assert.ok(before.fits);
    st.detailRequests = [];
    await trigger.getByText("Details", { exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "Usage details: skill-120" });
    await dialog.getByText("120 calls · 1 failed · 0 reads").waitFor();
    await dialog.getByRole("button", { name: "Done" }).click();
    await dialog.waitFor({ state: "hidden" });
    assert.deepEqual(await bounds(), before);
    assert.deepEqual(st.detailRequests, []);
    assert.deepEqual(st.sent, []);
  });

  test(engine + ": the MCP router with no agent", async (t) => {
    assert(["chromium", "webkit"].includes(engine), "BROWSER must be chromium or webkit");
    I18N = strings().I18N;
    const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: process.env.PLAYWRIGHT_CHANNEL || "chromium" }));
    t.after(() => browser.close());

    for (const width of [1100, 420]) for (const lang of ["en", "zh", "zh-TW", "ja", "de"]) {
      await t.test(`${lang} ${width}px`, async () => {
        const w = (key, vars) => tr(lang, key, vars);
        // the app with no agents; a browser in gateway mode, its agent not shown
        const web = width < 800;
        const st = { on: false, sent: [], checked: [], web, gateway: web, none: false };
        const where = web ? "gateway mode" : "no agents";
        const url = (web ? "https://mcp.example.net" : "http://127.0.0.1:3425") + "/mcp/magpie/hub";
        const errors = [];
        const ctx = await browser.newContext({ viewport: { width, height: 900 }, reducedMotion: "reduce" });
        // the Hooks tab last left: there are only Instructions, MCP and
        // Skills now, and the page opens on MCP
        await ctx.addInitScript(() => { try { localStorage.setItem("magpie.libTab", "hooks"); } catch {} });
        const page = await ctx.newPage();
        page.setDefaultTimeout(5000);
        page.setDefaultNavigationTimeout(60000);
        page.on("pageerror", (e) => errors.push(e.message));
        await page.route("**/*", server(lang, st));
        await page.goto("http://magpie.test/");
        // the Library is a page in gateway mode too
        const nav = page.locator('button[data-view="library"]');
        assert.ok(await nav.isVisible(), `${where}: no Library tab`);
        await nav.click();
        const v = page.locator("#view-library");
        const hub = v.locator(".lib-hub");
        await hub.waitFor();

        // the MCP tab, beside the prompts and skills a Remote magpie's
        // client takes (library-agentless-skills), and nothing of agents
        assert.deepEqual((await v.locator(".lib-tabs .opt").allTextContents()).map((x) => x.trim().replace(/ · \d+$/, "")), [w("Instructions"), w("MCP servers"), w("Skills")], `${where}: the tabs`);
        assert.equal((await v.locator(".lib-tabs .opt.on").textContent()).trim().replace(/ · \d+$/, ""), w("MCP servers"), `${where}: the tab open`);
        assert.equal((await v.locator(".lib-intro").first().textContent()).trim(), w("Add the servers other apps should reach, and turn on the MCP router: they connect at its address, from another computer with a gateway key."), where);
        assert.equal(await v.locator(".lib-server").count(), 1, where);
        assert.equal(await v.locator(".lib-ag, .lib-agents").count(), 0, `${where}: agent chips`);
        const text = await v.textContent();
        for (const gone of [w("In your agents"), w("Project servers"), "Claude Code", "agent-own"]) assert.ok(!text.includes(gone), `${where}: ${gone} is shown`);
        assert.equal(await page.locator("select").count(), 0);

        // off: what it would do
        assert.equal((await hub.locator(".name").textContent()).trim(), w("MCP router"));
        assert.equal((await hub.locator(".sub.wraps").textContent()).trim(), w("Serve every server in the library at one address, for any app to connect to."));
        const sw = hub.locator(".lib-switch");
        assert.equal(await sw.getAttribute("aria-checked"), "false");
        assert.equal(await v.locator(".lib-hubat").count(), 0);
        assert.deepEqual(await sideways(page), [], `${where}: sideways scroll, off`);

        // on: the router is on, what it serves, and its address
        await sw.click();
        await page.waitForFunction((x) => document.querySelector("#status")?.textContent === x, w("The MCP router is on: apps can connect at its address"));
        assert.deepEqual(st.sent, [{ on: true }]);
        await page.waitForFunction(() => document.querySelector("#view-library .lib-hub .lib-switch")?.getAttribute("aria-checked") === "true");
        assert.equal((await v.locator(".lib-hub .sub.wraps").textContent()).trim(), w("Any app connects at the address below for every server in the library, as server__tool."));
        await page.waitForFunction((x) => document.querySelector("#view-library .lib-hubstats")?.textContent === x, [w("1 server"), w("{n} tools", { n: 2 })].join(" · "));
        assert.ok(st.checked.includes("notes"), `${where}: the router's server isn't checked`);
        assert.equal((await v.locator(".lib-hubURL").textContent()).trim(), url);
        assert.deepEqual(await sideways(page), [], `${where}: sideways scroll, on`);

        // Connect…: other apps alone, with the gateway key
        await v.locator(".lib-hubconnect").click();
        const sheet = page.locator(".lib-hubsheet");
        await sheet.waitFor();
        assert.equal(await sheet.locator(".ebody > .segs").count(), 0, `${where}: the sheet has tabs`);
        assert.equal(await sheet.locator(".lib-hubagent").count(), 0);
        assert.equal((await sheet.locator(".lib-hubaddr code").textContent()).trim(), url);
        assert.ok(await sheet.locator(".lib-hubapp").count() >= 3);
        assert.equal((await sheet.locator(".lib-hubcap").textContent()).trim(), w("From another computer, use the address magpie is reached at there, with a gateway key: Authorization: Bearer <key>."));
        assert.deepEqual(await sideways(page), [], `${where}: sideways scroll, sheet`);
        await sheet.locator(".bar button.primary").click();
        await sheet.waitFor({ state: "hidden" });

        // off again
        await v.locator(".lib-hub .lib-switch").click();
        await page.waitForFunction((x) => document.querySelector("#status")?.textContent === x, w("The MCP router is off"));
        assert.deepEqual(st.sent, [{ on: true }, { on: false }]);

        // no server yet: the switch is there to turn on first
        st.none = true;
        await page.reload();
        await page.locator('button[data-view="library"]').click();
        await v.locator(".lib-hub").waitFor();
        assert.equal(await v.locator(".lib-server").count(), 0);
        assert.ok((await v.textContent()).includes(w("Paste a server's JSON from its README, or fill it in.")), where);
        assert.equal(await v.locator(".lib-hub .lib-switch").getAttribute("aria-checked"), "false");
        assert.deepEqual(await sideways(page), [], `${where}: sideways scroll, no server`);

        assert.deepEqual(errors, []);
        await ctx.close();
      });
    }
  });
}
