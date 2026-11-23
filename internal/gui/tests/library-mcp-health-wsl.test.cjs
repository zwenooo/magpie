// Run with Node's test runner and Playwright on the module path; see README.md.
// A command that agents in WSL start is checked where they run, in their
// distro (loong on Discord: a WSL program checked on Windows "exited (1)"
// while his agents started it fine). The row's tooltip says where it was
// checked; a failure names the place that failed on the row itself; a
// distro that isn't running, which a check doesn't start, is said to be
// unknown, not broken. A remote server signed in to in Claude Code in WSL
// itself says so, not "needs sign-in" (CyberCaius on Discord), and one no
// agent signed in to still needs it. In every language, in both engines. No backend:
// the API is faked here.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");

const assets = path.resolve(__dirname, "../assets");
const HOME = "C:\\Users\\loong";
const WSL = "claude@wsl:Ubuntu";
const agent = (id, name) => ({ id, name, icon: "claudecode-color", skills: `${HOME}\\.claude\\skills`, mcp: `${HOME}\\.claude.json` });
const fixture = () => ({
  dir: `${HOME}\\.magpie\\library`, backups: `${HOME}\\.magpie\\backups`, home: HOME,
  agents: [agent("claude", "Claude Code"), { ...agent(WSL, "Claude Code (Ubuntu)"), wsl: true },
    { ...agent("claude@wsl:Arch", "Claude Code (Arch)"), wsl: true }],
  instructions: { agents: [], sets: [] }, foundServers: [], foundSkills: [], skills: [], projects: [],
  servers: [
    { name: "inwsl", transport: "stdio", command: "node", args: ["/home/loong/tools/xxx.js"], agents: [WSL] },
    { name: "both", transport: "stdio", command: "npx", args: ["x-mcp"], agents: ["claude", WSL] },
    { name: "down", transport: "stdio", command: "node", args: ["/home/loong/tools/y.js"], agents: ["claude@wsl:Arch"] },
    { name: "here", transport: "stdio", command: "npx", args: ["h-mcp"], agents: ["claude"] },
    { name: "signed", transport: "http", url: "https://mcp.linear.app/mcp", agents: ["claude", WSL] },
    { name: "unsigned", transport: "http", url: "https://mcp.notion.com/mcp", agents: ["claude", WSL] },
  ],
});
const found = {
  inwsl: { state: "ok", tools: 2, checked: ["Ubuntu"] },
  both: { state: "error", why: "exited", code: 1, detail: "Node.js v24.9.0", checked: ["", "Ubuntu"] },
  down: { state: "error", why: "wsldown", detail: "Arch" },
  here: { state: "ok", tools: 3 },
  signed: { state: "auth", code: 401, oauth: true, agentSignIn: [WSL] },
  unsigned: { state: "auth", code: 401, oauth: true },
};

function server(lang, { web = true, library = fixture(), check = () => found, opened = [], external = [] } = {}) {
  return async (route) => {
    const req = route.request(), url = new URL(req.url());
    if (url.origin !== "http://magpie.test") { external.push(url.href); return route.abort(); }
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = {lang:"${lang}",theme:"light",web:${web}};` });
    if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/state") return route.fulfill({ json: { agents: [], profiles: [], settings: { lang } } });
    if (url.pathname === "/api/plugins") return route.fulfill({ json: { plugins: [] } });
    if (url.pathname === "/api/library/native") return route.fulfill({ json: [] });
    if (url.pathname === "/api/library") return route.fulfill({ json: library });
    if (url.pathname === "/api/library/mcp/check") {
      const body = req.postDataJSON(), replies = await check(body);
      const names = body.names || Object.keys(replies);
      return route.fulfill({ json: { servers: Object.fromEntries(names.map((n) => [n, replies[n]])) } });
    }
    if (url.pathname === "/api/open" || url.pathname.startsWith("/api/library/servers/open-")) {
      opened.push([url.pathname, req.postDataJSON()]);
      return route.fulfill({ json: {} });
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

const words = {
  en: { signed: "signed in in Claude Code (Ubuntu)", needs: "needs sign-in", checked: "Checked in: ", failed: "Failed in: ", down: "WSL Arch not running", downTip: "magpie can't check it: the agents given it run in WSL Arch, which isn't running, and a check doesn't start it" },
  zh: { signed: "已在 Claude Code (Ubuntu) 登录", needs: "需要登录", checked: "检查位置：", failed: "失败位置：", down: "WSL Arch 未运行", downTip: "magpie 无法检查：用它的智能体运行在 WSL Arch 里，而它没有在运行，检查不会去启动它" },
  "zh-TW": { signed: "已在 Claude Code (Ubuntu) 登入", needs: "需要登入", checked: "檢查位置：", failed: "失敗位置：", down: "WSL Arch 未執行", downTip: "magpie 無法檢查：使用它的智慧體執行在 WSL Arch 裡，而它沒有在執行，檢查不會去啟動它" },
  ja: { signed: "Claude Code (Ubuntu) でサインイン済み", needs: "サインインが必要", checked: "チェックした場所: ", failed: "失敗した場所: ", down: "WSL Arch は停止中", downTip: "magpie はチェックできません: これを使うエージェントが動く WSL Arch は起動しておらず、チェックのために起動はしません" },
  de: { signed: "in Claude Code (Ubuntu) angemeldet", needs: "Anmeldung nötig", checked: "Geprüft in: ", failed: "Fehlgeschlagen in: ", down: "WSL Arch läuft nicht", downTip: "magpie kann ihn nicht prüfen: Die Agenten, die ihn bekommen, laufen in WSL Arch, das nicht läuft, und eine Prüfung startet es nicht" },
};

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  test(engine + ": a server agents in WSL start says where it was checked", async (t) => {
    assert(["chromium", "webkit"].includes(engine), "BROWSER must be chromium or webkit");
    const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: process.env.PLAYWRIGHT_CHANNEL || "chromium" }));
    const errors = [];
    t.after(async () => {
      if (errors.length) console.log(errors);
      await browser.close();
    });
    for (const [lang, w] of Object.entries(words)) {
      await t.test(lang, async () => {
        const ctx = await browser.newContext({ viewport: { width: 980, height: 500 } });
        await ctx.addInitScript(() => { try { localStorage.setItem("magpie.libTab", "mcp"); } catch {} });
        const page = await ctx.newPage();
        page.setDefaultTimeout(5000);
        page.setDefaultNavigationTimeout(60000);
        page.on("pageerror", (e) => errors.push(e.message));
        await page.route("http://magpie.test/**", server(lang));
        await page.goto("http://magpie.test/");
        await page.locator('button[data-view="library"]').click();
        await page.locator("#view-library .lib-body:not(.lib-skel) .lib-row").first().waitFor();
        const status = (name) => page.locator(`#view-library .lib-health[data-server="${name}"]`);
        await page.waitForFunction(() => document.querySelectorAll("#view-library .lib-health:not(.checking):not(.idle)").length >= 3);
        const tip = async (n) => (await status(n).getAttribute("title")).split("\n");

        // checked in its distro: ok, and the tooltip says where
        assert.equal(await status("inwsl").evaluate((b) => b.classList.contains("ok")), true);
        assert.ok((await tip("inwsl")).includes(w.checked + "WSL Ubuntu"), (await tip("inwsl")).join(" | "));
        // failed in WSL after this computer: the row says which place
        assert.match(await status("both").textContent(), / · WSL Ubuntu$/);
        assert.equal(await status("both").evaluate((b) => b.classList.contains("err")), true);
        assert.ok((await tip("both")).includes(w.failed + "WSL Ubuntu"), (await tip("both")).join(" | "));
        assert.ok((await tip("both")).includes("Node.js v24.9.0"));
        // a stopped distro: not checked, and not painted as broken
        assert.equal(await status("down").textContent(), w.down);
        assert.equal(await status("down").evaluate((b) => b.classList.contains("unknown")), true);
        assert.equal((await tip("down"))[0], w.downTip);
        // a server only this computer's agents have says nothing of where
        const here = await tip("here");
        assert.ok(!here.some((l) => l.startsWith(w.checked) || l.startsWith(w.failed)), here.join(" | "));
        assert.doesNotMatch(await status("here").textContent(), / · /);
        // signed in in WSL's Claude Code: said, and not painted as wanting
        // a sign-in; the tooltip names the agent
        assert.equal(await status("signed").textContent(), w.signed);
        assert.equal(await status("signed").evaluate((b) => b.classList.contains("ok") && !b.classList.contains("auth")), true);
        assert.match((await tip("signed"))[0], /^Claude Code \(Ubuntu\)/);
        assert.equal(await status("unsigned").textContent(), w.needs);
        assert.equal(await status("unsigned").evaluate((b) => b.classList.contains("auth")), true);
        // one line high still
        for (const n of ["both", "signed"]) {
          const box = await status(n).boundingBox();
          assert.ok(box.height <= 18, n + " status wraps: " + box.height);
        }
        await ctx.close();
      });
    }
    for (const web of [false, true]) await t.test(web ? "remote viewer entries" : "desktop entries", async (t) => {
      const library = fixture(), opened = [], external = [], checks = [];
      const ctx = await browser.newContext({ viewport: { width: 780, height: 800 }, reducedMotion: "reduce" });
      t.after(() => ctx.close());
      await ctx.addInitScript(() => {
        localStorage.setItem("magpie.libTab", "mcp");
        window.openedLinks = [];
        window.open = (...args) => { window.openedLinks.push(args); return null; };
      });
      const site = "https://docs.example.org/start", next = "https://docs.example.org/new", loop = "http://127.0.0.1:8123/";
      const panel = "https://mcp.linear.app/panel", entry = (kind, url) => ({ kind, url });
      const ok = (...webEntries) => ({ state: "ok", tools: 1, webEntries });
      library.servers.push({ ...library.servers.find(s => s.name === "here"), localConsole: true, name: "managed" });
      const replies = { ...found,
        here: ok(entry("website", site), entry("website", next), entry("console", loop), entry("logs", "javascript:void(0)")),
        inwsl: { ...ok(entry("website", site), entry("console", loop)), checked: ["Ubuntu"] },
        managed: ok(entry("console", loop), entry("logs", loop + "logs"), entry("website", site)),
        signed: ok(entry("console", "https://wrong.example.org/panel"), entry("console", panel), entry("logs", panel + "?token=x")),
        unsigned: { ...found.unsigned, webEntries: [entry("website", site)] },
      };
      let refresh = 0;
      const check = body => {
        checks.push(body);
        if (!body.fresh) return replies;
        return { here: ++refresh === 1 ? ok(entry("logs", next)) : { state: "error", why: "protocol", webEntries: [entry("website", site)] } };
      };
      await ctx.route("**/*", server("en", { web, library, opened, external, check }));
      const page = await ctx.newPage();
      page.setDefaultTimeout(5000);
      page.setDefaultNavigationTimeout(60000);
      await page.goto("http://magpie.test/");
      await page.locator('#nav button[data-view="library"]').click();
      const row = name => page.locator(`.lib-server[data-server="${name}"]`);
      const buttons = name => row(name).locator(".lib-webentries button");
      const labels = name => buttons(name).allTextContents();
      await row("here").locator(".lib-health.ok").waitFor();
      assert.deepEqual(await labels("here"), ["Website · " + site, ...(web ? [] : ["Open console · " + loop])]);
      assert.deepEqual(await labels("inwsl"), ["Website · " + site]);
      assert.deepEqual(await labels("signed"), ["Open console · " + panel]);
      assert.deepEqual(await labels("unsigned"), []);
      assert.equal(await row("unsigned").locator(".lib-health.auth").textContent(), words.en.needs);
      assert.deepEqual(await labels("managed"), [...(web ? [] : ["Manage targets", "View server logs"]), "Website · " + site]);
      assert.deepEqual(opened, []);
      assert.deepEqual(await page.evaluate(() => window.openedLinks), []);
      assert.equal(checks.length, 1);
      if (!web) for (const [label, kind] of [["Manage targets", "console"], ["View server logs", "logs"]]) {
        await Promise.all([page.waitForResponse(r => r.url().endsWith("/servers/open-" + kind)), row("managed").getByRole("button", { name: label, exact: true }).click()]);
        assert.deepEqual(opened.at(-1), ["/api/library/servers/open-" + kind, { name: "managed" }]);
      }
      const health = row("here").locator(".lib-health");
      await health.click();
      await buttons("here").filter({ hasText: next }).waitFor();
      assert.deepEqual(await labels("here"), ["View server logs · " + next]);
      const click = () => buttons("here").click();
      if (web) {
        await click();
        assert.deepEqual(await page.evaluate(() => window.openedLinks), [[next, "_blank", "noopener"]]);
      } else {
        await Promise.all([page.waitForResponse(r => r.url().endsWith("/api/open")), click()]);
        assert.deepEqual(opened.at(-1), ["/api/open", { url: next }]);
      }
      assert.equal(await page.locator("#modal.lib:visible").count(), 0);
      await page.locator("#view-library .lib-select").click();
      assert.equal(await page.locator(".lib-webentries button").count(), 0);
      await row("here").locator(".name").click();
      assert.ok(await row("here").locator(".lib-pickbox").isChecked());
      await page.locator("#view-library .lib-pickdone").click();
      await health.click();
      await row("here").locator(".lib-health.err").waitFor();
      assert.deepEqual(await labels("here"), []);
      assert.deepEqual(checks.slice(1), [{ names: ["here"], fresh: true }, { names: ["here"], fresh: true }]);
      assert.equal(opened.length, web ? 0 : 3);
      assert.equal(await page.evaluate(() => window.openedLinks.length), web ? 1 : 0);
      assert.deepEqual(external, []);
    });
    assert.deepEqual(errors, []);
  });
}
