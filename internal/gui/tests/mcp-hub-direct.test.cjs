// Run with Node's test runner and Playwright on the module path; see README.md.
// Each server can bypass both the shared MCP router and local Codex stdio
// startup. Both switches are off by default and saved by separate routes.
// Every language, Chromium and
// WebKit, 1100 and 390 wide; nothing scrolls sideways. No backend: the
// API is faked here.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");

const assets = path.resolve(__dirname, "../assets");
const { I18N } = require("./strings.cjs").strings(); // i18n.js's and the plugins'
const tr = (lang, s, vars = {}) => ((lang === "en" ? s : I18N[lang][s]) || s).replace(/\{(\w+)\}/g, (_, k) => vars[k]);
const HOME = "/Users/xv";
const agents = [{ id: "claude", name: "Claude Code", icon: "", skills: `${HOME}/.claude/skills`, mcp: `${HOME}/.claude.json`, hub: "on" }];
const DIRECT = "Always connect this server directly", TAG = "Direct connection";
const HINT = "Each client gets this server’s own entry. Command servers are started by the client, bypassing both the MCP router and Codex on-demand startup.";
const START = "Start Codex stdio servers on demand", SAVED = "Saved — reload Codex to read its MCP configuration";
const WORDS = [DIRECT, TAG, HINT, START, SAVED];

function server(lang, calls, hubOn) {
  const lib = {
    dir: `${HOME}/.magpie/library`, backups: `${HOME}/.magpie/backups`, home: HOME,
    agents, mcpHub: hubOn, hubPath: "/mcp/magpie/hub/",
    instructions: { agents: [], sets: [] }, foundServers: [], projects: [], foundSkills: [], skills: [],
    servers: [
      { name: "index", transport: "stdio", command: "/opt/index/mcp", agents: ["claude"] },
      { name: "web", transport: "http", url: "https://example.com/mcp", agents: ["claude"] },
      { name: "magpie-image", transport: "stdio", command: "/Applications/magpie.app/Contents/MacOS/magpie", args: ["mcp", "image"], agents: ["claude"] },
    ],
  };
  return async (route) => {
    const req = route.request(), url = new URL(req.url());
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = {lang:"${lang}",theme:"light",web:true};` });
    if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/state") return route.fulfill({ json: { agents: [], profiles: [], settings: { lang, theme: "light", gateway: "http://127.0.0.1:3425" } } });
    if (url.pathname === "/api/plugins") return route.fulfill({ json: { plugins: [] } });
    if (url.pathname === "/api/library/native") return route.fulfill({ json: [] });
    if (url.pathname === "/api/library") return route.fulfill({ json: lib });
    if (url.pathname === "/api/library/mcp/check") return route.fulfill({ json: { servers: {} } });
    if (url.pathname === "/api/library/servers/codex-on-demand") {
      assert.equal(req.method(), "POST");
      const data = req.postDataJSON();
      calls.push(data);
      lib.codexMCPOnDemand = data.on;
      return route.fulfill({ json: { ...lib, result: {} } });
    }
    if (url.pathname === "/api/library/servers/save") {
      const { old, server: s } = req.postDataJSON();
      calls.push(`${s.name} ${JSON.stringify(s.hub)}`);
      const i = lib.servers.findIndex((x) => x.name === old);
      lib.servers[i] = { ...lib.servers[i], ...s };
      if (!s.hub) delete lib.servers[i].hub;
      return route.fulfill({ json: { ...lib, result: { changed: ["claude"] } } });
    }
    if (url.pathname === "/api/usage/quotas") return route.fulfill({ json: [] });
    if (url.pathname === "/api/groups") return route.fulfill({ json: { groups: [] } });
    if (url.pathname === "/api/providers") return route.fulfill({ json: { providers: [], gateway: { running: true } } });
    if (url.pathname.startsWith("/api/")) return route.fulfill({ json: {} });
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    await route.fulfill({ body: await fs.readFile(file), contentType });
  };
}

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  test(engine + ": an MCP server kept out of the hub", async (t) => {
    assert(["chromium", "webkit"].includes(engine), "BROWSER must be chromium or webkit");
    const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: process.env.PLAYWRIGHT_CHANNEL || "chromium" }));
    const errors = [];
    t.after(async () => {
      if (errors.length) console.log(errors);
      await browser.close();
    });
    for (const lang of ["en", "zh", "zh-TW", "ja", "de"]) {
      for (const width of [1100, 390]) {
        await t.test(`${lang} ${width}`, async () => {
          const w = (s, v) => tr(lang, s, v);
          if (lang !== "en") for (const s of WORDS) assert.ok(I18N[lang][s], `${lang}: ${s}`);
          for (const hubOn of [true, false]) {
            const calls = [];
            const ctx = await browser.newContext({ viewport: { width, height: 800 }, reducedMotion: "reduce" });
            await ctx.addInitScript(() => { try { localStorage.setItem("magpie.libTab", "mcp"); } catch {} });
            const page = await ctx.newPage();
            page.setDefaultTimeout(5000);
            page.setDefaultNavigationTimeout(60000);
            page.on("pageerror", (e) => errors.push(e.message));
            await page.route("**/*", server(lang, calls, hubOn));
            await page.goto("http://magpie.test/");
            await page.locator('button[data-view="library"]').click();
            const rowOf = (name) => page.locator("#view-library .lib-server", { has: page.locator(`.lib-health[data-server="${name}"]`) });
            const row = rowOf("index");
            const ed = page.locator("#modal .lib-editor");
            const sw = ed.getByRole("switch", { name: w(DIRECT), exact: true });
            const start = page.getByRole("switch", { name: w(START), exact: true });
            const router = page.getByRole("switch", { name: w("MCP router"), exact: true });
            const tag = row.locator(".lib-tag.lib-direct");
            const open = async (r = row) => { await r.locator(".name").click(); await ed.waitFor(); };
            const save = async () => { await ed.locator(":scope > .bar button.primary").click(); await ed.waitFor({ state: "detached" }); };
            const stats = () => page.locator("#view-library .lib-hubstats").textContent();
            await row.waitFor();

            assert.equal(await start.getAttribute("aria-checked"), "false");
            assert.equal(await router.getAttribute("aria-checked"), String(hubOn));
            if (!hubOn) {
              await open();
              assert.equal(await sw.count(), 0);
              await ed.getByRole("button", { name: w("Cancel"), exact: true }).click();
            }
            await start.click();
            await page.waitForFunction((msg) => document.querySelector("#status")?.textContent === msg, w(SAVED));
            assert.deepEqual(calls.splice(0), [{ on: true }]);
            assert.equal(await start.getAttribute("aria-checked"), "true");
            assert.equal(await router.getAttribute("aria-checked"), String(hubOn));
            assert.equal(await tag.count(), 0, "a shared server has no tag");
            if (hubOn) assert.ok((await stats()).startsWith(w("{n} servers", { n: 3 })), await stats());
            await open();
            assert.equal(await sw.getAttribute("aria-checked"), "false");
            assert.equal(await ed.getByText(w(HINT), { exact: true }).count(), 1);
            await sw.click();
            assert.equal(await sw.getAttribute("aria-checked"), "true");
            await save();
            assert.deepEqual(calls, ['index "direct"']);
            // the row says so, the hub counts it given directly, and the editor shows it off again
            await tag.waitFor();
            assert.equal((await tag.textContent()).trim(), w(TAG));
            assert.equal(await tag.getAttribute("title"), w(HINT));
            if (hubOn) await page.waitForFunction((x) => document.querySelector("#view-library .lib-hubstats")?.textContent.startsWith(x[0]) &&
              document.querySelector("#view-library .lib-hubstats")?.textContent.endsWith(x[1]),
            [w("{n} servers", { n: 2 }), w("{name} is given to each agent directly", { name: "index" })]);
            await start.click();
            await page.locator(".lib-codex-on-demand [aria-checked=false]").waitFor();
            assert.equal(await router.getAttribute("aria-checked"), String(hubOn));
            assert.deepEqual(calls.splice(0), ['index "direct"', { on: false }]);
            await open();
            assert.equal(await sw.getAttribute("aria-checked"), "true", "a saved direct override survives both switches");
            await sw.click();
            assert.equal(await sw.getAttribute("aria-checked"), "false", "the override can still be changed back before saving");
            await save();
            assert.deepEqual(calls, ['index ""']);
            await tag.waitFor({ state: "detached" });
            if (hubOn) {
              await open(rowOf("magpie-image"));
              assert.equal(await sw.getAttribute("aria-checked"), "false");
              await sw.click();
              await ed.locator(":scope > .bar button", { hasText: w("Cancel") }).click();
              assert.deepEqual(calls, ['index ""']);
            }
            assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, "no sideways scroll");
            await ctx.close();
          }
        });
      }
    }
    assert.deepEqual(errors, []);
  });
}
