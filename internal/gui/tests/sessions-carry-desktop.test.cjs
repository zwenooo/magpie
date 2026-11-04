// A read-only Desktop transcript can be copied into another agent without
// gaining a direct resume or delete action for its original session.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");

const assets = path.resolve(__dirname, "../assets");
const targets = [
  { agent: "claude", name: "Claude Code", command: "cd '/work/project' && claude --resume copied-claude" },
  { agent: "codex", name: "Codex", command: "cd '/work/project' && codex resume copied-codex" },
];
const original = {
  agent: "claude-desktop", id: "desktop-original", title: "Desktop conversation", cwd: "/work/project",
  start: "2026-10-31T08:00:00Z", last: "2026-10-31T08:15:00Z", models: [], messages: 4, size: 2048, files: 1,
  path: "/profiles/desktop/config/projects/project/desktop-original.jsonl", read_only: true, deletable: false,
  carry: targets.map(({ agent }) => ({ agent, convert: true })),
};

function serve(lang, calls, withCatalog, state) {
  const snapshot = JSON.stringify(original);
  return async (route) => {
    const req = route.request(), url = new URL(req.url());
    const json = (data) => route.fulfill({ json: data });
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs={lang:${JSON.stringify(lang)},theme:"light",web:false};` });
    if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/state") return json({
      agents: withCatalog ? targets.map(({ agent, name }) => ({ id: agent, name, icon: agent, wired: false, fields: [] })) : [],
      profiles: [], settings: { lang, theme: "light" },
    });
    if (url.pathname === "/api/plugins") return json({ plugins: [] });
    if (url.pathname === "/api/groups") return json({ groups: [] });
    if (url.pathname === "/api/providers") return json({ providers: [], presets: [], excluded: [], gateway: { running: true, window: true } });
    if (url.pathname === "/api/sessions/manage") return json({
      agents: [{ agent: "claude-desktop", name: "Claude Desktop", icon: "claude", count: 1, deletable: false }],
      agent: "claude-desktop", sessions: [original], terminal: false, trash: [], trashDir: "/magpie/trash",
      reads: { asked: true, all: true, found: [] },
    });
    if ((url.pathname.startsWith("/api/sessions/") || url.pathname === "/api/copy") && req.method() === "POST") {
      const body = req.postDataJSON();
      calls.push({ path: url.pathname, body });
      assert.equal(JSON.stringify(original), snapshot, "the fixture's source stays unchanged");
      if (url.pathname === "/api/sessions/carry") {
        if (state.error) return route.fulfill({ status: 400, json: { error: state.error } });
        const target = targets.find(({ agent }) => agent === body.to);
        assert(target);
        assert.deepEqual(body, { agent: "claude-desktop", id: original.id, to: target.agent, open: false });
        return json({ agent: target.agent, id: `copied-${target.agent}`, command: target.command });
      }
      if (url.pathname === "/api/copy") return json({});
      return route.fulfill({ status: 400, json: { error: "unexpected mutation" } });
    }
    if (url.pathname.startsWith("/api/")) return json({});
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    try { await route.fulfill({ body: await fs.readFile(file), contentType }); }
    catch { await route.fulfill({ status: 404, body: "" }); }
  };
}

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const lang of ["en", "zh", "zh-TW", "ja", "de"]) {
    for (const width of [1100, 420]) {
      test(`${engine} ${lang} ${width}px: Desktop history continues through a new Claude Code or Codex session`, async (t) => {
        const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
        t.after(() => browser.close());
        const page = await browser.newPage({ viewport: { width, height: 760 }, reducedMotion: "reduce" });
        page.setDefaultTimeout(5000);
        page.setDefaultNavigationTimeout(60000);
        const calls = [], errors = [], fixture = { error: "" };
        page.on("pageerror", (err) => errors.push(err.message));
        await page.route("**/*", serve(lang, calls, width === 1100, fixture));
        await page.goto("http://magpie.test/?view=sessions");
        const tr = (key, vars) => page.evaluate(([key, vars]) => t(key, vars), [key, vars]);
        const view = page.locator("#view-sessions");
        const row = view.locator('.sm-sess[data-id="desktop-original"]');
        await row.waitFor();
        assert.equal(await row.locator(".sess-resume, .sess-term, .sm-del").count(), 0, "no original resume, terminal or delete action");
        const carry = row.locator(".sess-carry");
        await carry.waitFor();
        assert.deepEqual(calls, [], "listing reads only");
        const before = await view.evaluate((el) => el.scrollTop);
        const menu = page.locator(".sess-carry-menu");
        for (const target of targets) {
          await carry.click();
          await menu.waitFor();
          assert.deepEqual(await menu.locator(".pm-name").allTextContents(), targets.map(({ name }) => name), "destinations are named even without their own sessions");
          const note = await tr("Convert, then copy the command");
          assert.deepEqual(await menu.locator(".pm-note").allTextContents(), [note, note]);
          assert.equal(await page.locator("select").count(), 0);
          const action = menu.locator(".pm-item").filter({ has: page.locator(".pm-name", { hasText: new RegExp(`^${target.name}$`) }) });
          assert.equal(await action.getAttribute("title"), await tr("A new {agent} session is made from this conversation", { agent: target.name }));
          const copied = page.waitForResponse((r) => r.url().endsWith("/api/copy"));
          await action.click();
          await copied;
          assert.deepEqual(calls.slice(-2), [
            { path: "/api/sessions/carry", body: { agent: "claude-desktop", id: original.id, to: target.agent, open: false } },
            { path: "/api/copy", body: { text: target.command } },
          ]);
          assert.equal(await view.evaluate((el) => el.scrollTop), before);
        }
        await row.locator(".who").click();
        const details = view.locator(".sess-detail");
        for (const target of targets) {
          const label = await tr("Continue in {agent}", { agent: target.name });
          const line = details.locator(".sess-line").filter({ has: page.locator(".k", { hasText: label }) });
          assert.equal(await line.count(), 1);
          assert((await line.textContent()).includes(await tr("A new {agent} session is made from this conversation", { agent: target.name })));
        }
        assert.equal(calls.length, 4, "opening details does not create another copy");
        assert.equal(await row.locator(".sess-resume, .sess-term, .sm-del").count(), 0);
        for (const error of ["target session directory is read only", "start the WSL distribution before writing sessions"]) {
          fixture.error = error;
          const prior = calls.length;
          await carry.click();
          await menu.locator(".pm-item").first().click();
          const translated = await tr(error);
          await page.locator("#status").filter({ hasText: translated }).waitFor();
          assert.equal(await page.locator("#status").textContent(), translated);
          if (lang !== "en") assert.notEqual(translated, error);
          assert.deepEqual(calls.slice(prior).map(({ path }) => path), ["/api/sessions/carry"], "failed conversion never copies a command");
        }
        assert.deepEqual(errors, []);
        if (process.env.ARTIFACT_DIR && lang === "zh") {
          await fs.mkdir(process.env.ARTIFACT_DIR, { recursive: true });
          await page.screenshot({ path: path.join(process.env.ARTIFACT_DIR, `${engine}-${width}-desktop-carry.png`) });
        }
      });
    }
  }
}
