const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const {test} = require("node:test");
const {chromium, webkit} = require("playwright-core");
const {strings} = require("./strings.cjs");

const assets = path.resolve(__dirname, "../assets"), {I18N} = strings();
for (const engine of process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"]) {
  test(`${engine}: program repositories are opt-in and apply only a previewed commit`, async (suite) => {
    const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({channel: process.env.CHROMIUM_CHANNEL || "chromium"}));
    suite.after(() => browser.close());
    for (const lang of process.env.LANG_UNDER_TEST ? [process.env.LANG_UNDER_TEST] : ["en", "zh", "zh-TW", "de", "ja"]) await suite.test(lang, async (subtest) => {
      const tr = (key) => I18N[lang]?.[key] || key;
      const ctx = await browser.newContext({viewport: {width: 440, height: 850}});
      subtest.after(() => ctx.close());
      await ctx.addInitScript(() => localStorage.setItem("magpie.libTab", "mcp"));
      const page = await ctx.newPage();
      page.setDefaultTimeout(5000);
      page.setDefaultNavigationTimeout(60000);
      const calls = [], starts = [], errors = [];
      page.on("pageerror", (error) => errors.push(error.message));
      const server = {name: "kept", transport: "stdio", command: "node", args: ["{{program}}/index.js"], agents: []};
      const lib = {home: "/home/test", dir: "/home/test/library", backups: "/home/test/backups", agents: [{id: "codex", name: "Codex", mcp: "/home/test/codex.json"}],
        instructions: {agents: [], sets: []}, foundServers: [], foundSkills: [], projects: [], skills: [], servers: [server,
          {...server, name: "ordinary", args: [], env: {DIR: "{{program}}"}}, {...server, name: "remote", transport: "http", url: "https://example.test"}]};
      const first = "a".repeat(40), second = "b".repeat(40);
      let latest = first, current = "";
      await page.route("**/*", async (route) => {
        const req = route.request(), url = new URL(req.url()), pathname = url.pathname;
        assert.equal(url.hostname, "magpie.test");
        if (pathname === "/boot.js") return route.fulfill({contentType: "text/javascript", body: `window.bootPrefs={lang:"${lang}",theme:"light",web:true};`});
        if (pathname === "/wails/runtime.js") return route.fulfill({contentType: "text/javascript", body: "export const Window = {};"});
        if (pathname === "/api/state") return route.fulfill({json: {agents: [], profiles: [], settings: {lang, theme: "light"}}});
        if (pathname === "/api/library") return route.fulfill({json: lib});
        if (pathname === "/api/library/mcp/check") starts.push(...req.postDataJSON().names);
        if (pathname.startsWith("/api/library/servers/repo-")) {
          const body = req.postDataJSON();
          calls.push([pathname.split("/").pop(), body]);
          if (pathname.endsWith("check")) return route.fulfill({json: {current, latest}});
          if (body.expected !== latest) return route.fulfill({status: 400, json: {error: "ref moved"}});
          current = latest;
          server.repoSource = body.repoSource || server.repoSource;
          server.agents = ["codex"];
          return route.fulfill({json: {...lib, result: {}}});
        }
        if (["/api/library/native", "/api/usage/quotas"].includes(pathname)) return route.fulfill({json: []});
        if (pathname.startsWith("/api/")) return route.fulfill({json: {plugins: [], providers: [], groups: [], gateway: {running: true}}});
        const file = path.join(assets, pathname === "/" ? "index.html" : pathname);
        try { return await route.fulfill({body: await fs.readFile(file), contentType: {".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml"}[path.extname(file)]}); }
        catch (error) { if (error.code !== "ENOENT") throw error; return route.fulfill({status: 404}); }
      });
      await page.goto("http://magpie.test/", {waitUntil: "domcontentloaded"});
      await page.locator('button[data-view="library"]').click();
      const modal = page.locator("#modal .lib-editor"), box = modal.locator(".lib-program-source");
      const action = (key) => box.getByRole("button", {name: tr(key), exact: true});
      const open = async (name) => {
        try { await page.locator(`.lib-server[data-server="${name}"] .name`).click(); }
        catch (error) { throw Error(`${error.message}\n${errors.join("\n")}\n${await page.locator("#view-library").innerText()}`); }
      };
      const close = async () => { await page.keyboard.press("Escape"); await modal.waitFor({state: "detached"}); };
      for (const name of ["ordinary", "remote"]) {
        await open(name); await modal.waitFor(); assert.equal(await box.count(), 0); await close();
      }
      await page.locator(".after-list").getByRole("button", {name: tr("＋ Add server")}).click();
      await modal.waitFor(); assert.equal(await box.count(), 0); await close();
      await open("kept");
      await action("Choose a program repository…").click();
      assert.deepEqual(calls, []);
      const source = {repo: "https://github.com/example/project", ref: "stable", path: "packages/one"};
      for (const [index, value] of Object.values(source).entries()) await box.locator("input").nth(index).fill(value);
      const renderPreview = async () => { await action("Preview commit").click(); await box.getByText(latest, {exact: false}).first().waitFor(); };
      await renderPreview();
      assert.deepEqual(calls, [["repo-check", {name: "kept", repoSource: source}]]);
      source.ref = "main";
      await box.locator("input").nth(1).fill(source.ref);
      assert.equal(await action("Apply this commit").count(), 0);
      await renderPreview();
      latest = second;
      await action("Apply this commit").click();
      await page.getByText("ref moved", {exact: false}).first().waitFor();
      assert.deepEqual(calls.at(-1), ["repo-update", {name: "kept", repoSource: source, expected: first}]);
      assert.equal(await action("Apply this commit").count(), 0);
      await renderPreview(); await action("Apply this commit").click();
      await action("Check for updates").waitFor();
      assert.deepEqual(calls.at(-1), ["repo-update", {name: "kept", repoSource: source, expected: second}]);
      await close(); await open("kept");
      await action("Check for updates").click();
      await box.getByText(tr("Up to date"), {exact: true}).waitFor();
      assert.deepEqual(calls.at(-1), ["repo-check", {name: "kept", repoSource: null}]);
      assert.equal(await action("Apply this commit").count(), 0);
      latest = first;
      await renderPreview();
      assert.ok((await box.innerText()).includes(second));
      await action("Apply this commit").click(); await action("Check for updates").waitFor();
      assert.deepEqual(calls.at(-1), ["repo-update", {name: "kept", repoSource: null, expected: first}]);
      assert.deepEqual(starts, []); assert.deepEqual(errors, []);
    });
  });
}
