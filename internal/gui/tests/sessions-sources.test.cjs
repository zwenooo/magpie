// Custom session roots are scanned before registration. All APIs are fixtures;
// the real app UI is used without touching a local session or configuration.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");

const assets = process.env.MAGPIE_SOURCES_ASSETS || path.resolve(__dirname, "../assets");
const directory = String.raw`C:\profiles\claude\config\projects`;
const source = { path: directory, agent: "claude-desktop" };
const title = "Desktop history <not markup>";
const sessions = Array.from({ length: 180 }, (_, i) => ({
  agent: "claude-desktop", id: `desk-${i + 1}`, title: i ? `Desktop history ${i + 1}` : title,
  cwd: "D:\\code\\project", models: [], read_only: true, last: "2026-10-31T08:00:00Z", start: "2026-10-31T07:00:00Z", messages: 2, size: 500, files: 1,
}));
const preview = sessions.slice(0, 50).map(({ id, title, cwd, last }) => ({ id, title, cwd, last }));
const keys = ["Import sessions", "Import all", "Session directory", "Session type", "Scan", "Allow and scan", "Scan directories", "Remove directory", "Remove directory: {path}", "Found {n} sessions", "No sessions found", "Imported {n} sessions", "unsupported session source type", "session source must be an absolute folder path", "session source must be a folder", "no sessions found in this folder", "an overlapping folder is already imported with another type", "invalid session source", "invalid session sources", "could not lock session sources"];

function fixture(lang) {
  const calls = [];
  let sources = [], allowed = false, scanned = false, scanWait = null;
  const reads = () => ({ asked: true, all: false, found: [{ agent: "claude-desktop", name: "Claude Desktop", icon: "claude", on: allowed }] });
  return {
    calls,
    holdScan: () => {
      let release;
      scanWait = new Promise((resolve) => { release = resolve; });
      return () => { release(); scanWait = null; };
    },
    route: async (route) => {
      const req = route.request(), url = new URL(req.url());
      const json = (data, status = 200) => route.fulfill({ status, json: data });
      if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = {lang:${JSON.stringify(lang)},theme:"light",web:true};` });
      if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
      if (url.pathname === "/api/state") return json({ agents: [], profiles: [], settings: { lang, theme: "light" } });
      if (url.pathname === "/api/plugins") return json({ plugins: [] });
      if (url.pathname === "/api/groups") return json({ groups: [] });
      if (url.pathname === "/api/providers") return json({ providers: [], presets: [], excluded: [], gateway: { running: true, window: true } });
      if (url.pathname === "/api/sessions/manage") return json({
        agents: sources.length ? [{ agent: "claude-desktop", name: "Claude Desktop", icon: "claude", count: sessions.length, deletable: false }] : [],
        agent: sources.length ? "claude-desktop" : "", reads: reads(), terminal: false, trash: [], trashDir: "~/magpie/trash",
        sessions: sources.length ? sessions : [],
      });
      if (url.pathname === "/api/sessions/reads") {
        if (req.method() === "POST") {
          const body = req.postDataJSON();
          calls.push({ api: "reads", body });
          allowed = body.agents?.["claude-desktop"] === true;
        }
        return json(reads());
      }
      if (url.pathname === "/api/sessions/sources") return json({ sources });
      if (url.pathname === "/api/sessions/scan") {
        const body = req.postDataJSON();
        calls.push({ api: "scan", body });
        if (scanWait) await scanWait;
        if (!allowed) return json({ error: "session reading is disabled" }, 400);
        if (body.path.endsWith("missing")) return json({ error: "directory does not exist" }, 400);
        scanned = true;
        return json({ source: body, count: body.path.endsWith("empty") ? 0 : sessions.length, sessions: body.path.endsWith("empty") ? [] : preview });
      }
      if (url.pathname === "/api/sessions/import") {
        const body = req.postDataJSON();
        calls.push({ api: "import", body });
        assert(scanned, "import follows scan");
        sources = [body];
        return json({ source: body, count: sessions.length, sessions: preview });
      }
      if (url.pathname === "/api/sessions/sources/remove") {
        calls.push({ api: "remove", body: req.postDataJSON() });
        sources = [];
        return json({ sources });
      }
      if (url.pathname.startsWith("/api/")) return json({});
      const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
      const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
      try { await route.fulfill({ body: await fs.readFile(file), contentType }); }
      catch { await route.fulfill({ status: 404, body: "" }); }
    },
  };
}

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const lang of ["en", "zh", "zh-TW", "ja", "de"]) {
    for (const width of [1100, 420]) {
      test(`${engine} ${lang} ${width}px: custom Desktop directory scans, imports and unregisters`, async (t) => {
        const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: process.env.CHROMIUM_CHANNEL || "chromium" }));
        t.after(() => browser.close());
        const page = await browser.newPage({ viewport: { width, height: 760 }, reducedMotion: "reduce" });
        page.setDefaultTimeout(6000);
        page.setDefaultNavigationTimeout(60000);
        const errors = [];
        page.on("pageerror", (err) => errors.push(err.message));
        const f = fixture(lang);
        await page.route("**/*", f.route);
        await page.goto("http://magpie.test/?view=sessions");
        const tr = (key, vars) => page.evaluate(([key, vars]) => t(key, vars), [key, vars]);
        const button = page.locator("#view-sessions .sm-import-btn");
        await button.waitFor();
        if (lang !== "en") assert.deepEqual(await page.evaluate(([lang, keys]) => keys.filter((key) => !I18N[lang][key]), [lang, keys]), []);
        const before = await page.locator("#view-sessions").evaluate((e) => e.scrollTop);
        await button.click();
        const dialog = page.locator("#modal .sm-import");
        const input = dialog.getByLabel(await tr("Session directory"));
        const pick = dialog.getByRole("button", { name: await tr("Session type"), exact: true });
        const take = dialog.getByRole("button", { name: await tr("Import all"), exact: true });
        assert.equal(await take.isVisible(), false);
        assert.equal(await dialog.locator("select").count(), 0);
        assert.match(await pick.textContent(), /Claude Desktop/);
        await input.fill(directory);
        assert.equal(f.calls.length, 0, "typing never grants consent or starts a scan");
        const releaseScan = f.holdScan();
        t.after(releaseScan);
        const firstScan = dialog.getByRole("button", { name: await tr("Allow and scan"), exact: true });
        await firstScan.click();
        assert(await input.isDisabled(), "a running scan holds its input");
        assert(await pick.isDisabled(), "a running scan holds its type");
        assert(await dialog.locator(".bar button.busy").isDisabled(), "a running scan cannot start twice");
        releaseScan();
        await take.waitFor();
        assert.deepEqual(f.calls, [{ api: "reads", body: { agents: { "claude-desktop": true } } }, { api: "scan", body: source }]);
        assert.equal(await dialog.locator(".sm-import-preview li").count(), 50, "only 50 samples are previewed");
        assert.equal(await dialog.locator(".sm-import-preview li").first().textContent(), title);
        assert.equal(await dialog.locator(".sm-import-info").textContent(), await tr("Found {n} sessions", { n: 180 }), "scan count includes every session in the directory");
        assert.equal(await page.locator("#view-sessions").evaluate((e) => e.scrollTop), before);
        assert.equal(await dialog.evaluate((e) => e.scrollWidth > e.clientWidth || e.getBoundingClientRect().right > innerWidth), false);
        if (process.env.ARTIFACT_DIR && lang === "zh") {
          await fs.mkdir(process.env.ARTIFACT_DIR, { recursive: true });
          await page.screenshot({ path: path.join(process.env.ARTIFACT_DIR, `${engine}-${width}-session-import.png`) });
        }
        await input.fill(directory + "-empty");
        assert.equal(await take.isVisible(), false, "editing invalidates preview");
        await dialog.getByRole("button", { name: await tr("Scan"), exact: true }).click();
        await dialog.getByText(await tr("No sessions found"), { exact: true }).waitFor();
        assert.equal(await take.isVisible(), false, "empty scan cannot import");
        await input.fill(directory + "-missing");
        await dialog.getByRole("button", { name: await tr("Scan"), exact: true }).click();
        await dialog.getByText("directory does not exist", { exact: true }).waitFor();
        assert.equal(await take.isVisible(), false, "failed scan cannot import");
        await input.fill(directory);
        await dialog.getByRole("button", { name: await tr("Scan"), exact: true }).click();
        await take.waitFor();
        await pick.click();
        await page.getByRole("menuitemradio", { name: "Claude Code", exact: true }).click();
        assert.equal(await take.isVisible(), false, "type change invalidates scan");
        await pick.click();
        await page.getByRole("menuitemradio", { name: "Claude Desktop", exact: true }).click();
        await dialog.getByRole("button", { name: await tr("Scan"), exact: true }).click();
        await take.click();
        await page.locator("#view-sessions .sm-sess").filter({ hasText: title }).waitFor();
        assert.deepEqual(f.calls.filter((x) => x.api === "import"), [{ api: "import", body: source }], "one directory-level import, not selected preview IDs");
        assert.equal(await page.locator("#status").textContent(), await tr("Imported {n} sessions", { n: 180 }), "success uses the full directory count, not the preview size");
        assert.equal(await page.locator("#view-sessions .sm-agents .on em").textContent(), "180", "all directory sessions appear in the agent count");
        await button.click();
        await dialog.locator(".sm-import-source-path").waitFor();
        await dialog.locator(".sm-import-source-path").click();
        assert.equal(await input.inputValue(), directory);
        await dialog.getByRole("button", { name: await tr("Remove directory: {path}", { path: directory }), exact: true }).click();
        await page.waitForFunction(() => document.querySelector(".sm-import-sources")?.hidden);
        assert.deepEqual(f.calls.filter((x) => x.api === "remove"), [{ api: "remove", body: source }]);
        await dialog.getByRole("button", { name: await tr("Cancel"), exact: true }).click();
        assert.deepEqual(errors, []);
      });
    }
  }
}
