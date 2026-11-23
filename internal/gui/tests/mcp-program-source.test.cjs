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
      const standalone = {repo: "https://github.com/zwenooo/ops-mcp.git", ref: "main", path: ""};
      const toolkit = {repo: "https://github.com/zwenooo/magpie-agent-toolkit", ref: "main", path: "mcp/ops-mcp"};
      const installed = {...server, command: "python", args: ["{{program}}/server.py"], repoSource: standalone, repoLocalSource: standalone, repoHash: "c".repeat(64), repoLocalHash: "c".repeat(64), repoLocalCommit: second, repoCommit: second};
      lib.servers.push({...installed, name: "my-tools", agents: ["codex"]}, {...installed, name: "old-receipt", repoLocalSource: toolkit},
        {...installed, name: "legacy", repoLocalSource: undefined});
      for (const [name, src] of [["ops-mcp", toolkit], ["subfolder", {...standalone, path: "mcp/ops-mcp"}], ["other-owner", {...standalone, repo: "https://github.com/example/ops-mcp"}]]) {
        lib.servers.push({...installed, name, repoSource: src, repoLocalSource: src});
      }
      const saved = lib.servers.find(s => s.name === "my-tools");
      saved.webEntryKey = 'fixture-v1'; saved.rememberedWebEntries = [{kind:'console',url:'https://example.test/remembered'}, {kind:'logs',url:'https://example.test/logs'}];
      const shelf = {...saved, name:'remembered-tools', rememberedTools:['alpha','beta'], rememberedToolCount:2};
      lib.servers.push(shelf);
      let latest = first, current = "", bundle, restored, gateway;
      const managed = [];
      await page.route("**/*", async (route) => {
        const req = route.request(), url = new URL(req.url()), pathname = url.pathname;
        assert.equal(url.hostname, "magpie.test");
        if (pathname === "/boot.js") return route.fulfill({contentType: "text/javascript", body: `window.bootPrefs={lang:"${lang}",theme:"light",web:true};`});
        if (pathname === "/wails/runtime.js") return route.fulfill({contentType: "text/javascript", body: "export const Window = {};"});
        if (pathname === "/api/state") return route.fulfill({json: {agents: [], profiles: [], settings: {lang, theme: "light"}}});
        if (pathname === "/api/library") {
          if (url.searchParams.has('glance')) return route.fulfill({json:{...lib, servers:lib.servers.map(s => ({...s, rememberedWebEntries:undefined, rememberedTools:undefined}))}});
          return route.fulfill({json: lib});
        }
        if (pathname === "/api/library/mcp/check") starts.push(...req.postDataJSON().names);
        if (/\/servers\/(bundle-preview|bundle-restore|managed-verify)$/.test(pathname)) {
          managed.push([pathname.split("/").pop(), req.postDataJSON()]);
          const json = pathname.endsWith("preview") ? bundle : pathname.endsWith("restore") ? restored : {ok: true, gateway, health:{state:"ok", tools:2, webEntries:[{kind:"console", url:"https://example.test/manage"},{kind:"logs", url:"https://example.test/logs"}]}};
          return route.fulfill(json ? {json} : {status: 400, json: {error: "private diagnostic"}});
        }
        if (pathname.startsWith("/api/library/servers/repo-")) {
          const body = req.postDataJSON();
          calls.push([pathname.split("/").pop(), body]);
          if (pathname.endsWith("check")) return route.fulfill({json: {current, latest}});
          if (body.expected !== latest) return route.fulfill({status: 400, json: {error: "ref moved"}});
          current = latest;
          server.repoSource = server.repoLocalSource = body.repoSource || server.repoSource;
          server.repoLocalCommit = server.repoCommit = current;
          server.repoLocalHash = server.repoHash = "e".repeat(64);
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
      const remembered = page.locator('.lib-server[data-server="my-tools"] .lib-webentries button');
      await remembered.waitFor(); assert.equal(await remembered.getAttribute('title'), 'https://example.test/remembered');
      assert.equal(await remembered.count(), 1); assert.deepEqual(starts, []); assert.deepEqual(managed, []);
      await page.reload({waitUntil:'domcontentloaded'});
      await page.locator('button[data-view="library"]').click();
      await remembered.waitFor(); assert.equal(await remembered.count(), 1); assert.deepEqual(starts, []); assert.deepEqual(managed, []);
      const cachedRow = page.locator('.lib-server[data-server="remembered-tools"]');
      const badge = cachedRow.locator('.lib-health');
      assert.equal(await badge.textContent(), tr('Last listed: {n} tools').replace('{n}','2'));
      await badge.click();
      assert.deepEqual(await cachedRow.locator('.lib-toolname').allTextContents(), ['alpha', 'beta']);
      assert.equal(await cachedRow.locator('.lib-webentries button').count(), 1);
      assert.deepEqual(starts, []); assert.deepEqual(managed, []);
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
      await close();
      for (const name of ["ops-mcp", "other-owner", "subfolder", "old-receipt", "legacy", "kept"]) {
        await open(name); await box.waitFor(); assert.equal(await action("Check credentials").count(), 0); await close();
      }
      await open("legacy");
      assert.equal(await action("Probe a new local process").count(), 0);
      await action("Check for updates").click(); await action("Apply this commit").waitFor(); await close();
      assert.deepEqual(managed, []);
      const repoCalls = calls.length;
      await page.locator('.lib-health[data-server="my-tools"]').click();
      const consent = page.locator("#modal .lib-editor");
      assert.deepEqual(managed, []);
      await consent.getByRole("button", {name: tr("Cancel"), exact: true}).click();
      assert.deepEqual(managed, []);
      await page.locator('.lib-health[data-server="my-tools"]').click();
      await consent.getByRole("button", {name: tr("Probe a new local process"), exact: true}).click();
      const link = page.locator('.lib-server[data-server="my-tools"] .lib-webentries button');
      await link.waitFor(); assert.equal(await link.count(), 1); assert.equal(await link.getAttribute('title'), 'https://example.test/manage');
      assert.deepEqual(managed.splice(0), [["managed-verify", {name:"my-tools"}]]);
      await open('my-tools');
      const readiness = box.locator(".lib-managed-readiness");
      await readiness.waitFor();
      assert.ok((await readiness.innerText()).includes(standalone.repo));
      assert.ok((await readiness.innerText()).includes(second));
      assert.ok((await readiness.innerText()).includes("~/.config/ops-mcp"));
      assert.deepEqual(managed, []);
      const shows = (key, status) => readiness.getByText(tr(key).replace("{status}", tr(status)), {exact: true}).waitFor();
      const credentials = (key) => shows("Local credentials: {status}", key);
      await credentials("Not yet confirmed");
      for (const [state, shown] of [["conflict", "Credential conflict — no overwrite"], ["locked", "Credential bundle locked"], ["unsupported", "Credential bundle unsupported"], ["future", "Not yet confirmed"], ["missing", "Credentials missing"]]) {
        bundle = state === "missing" ? {state} : {state, token: "must-not-restore"};
        await action("Check credentials").click(); await credentials(shown);
        assert.equal(await action("Confirm credential restore").count(), 0);
      }
      bundle = {state: "missing", token: "preview-only"};
      await action("Check credentials").click(); await action("Confirm credential restore").waitFor();
      assert.ok(managed.every(([act, body]) => act === "bundle-preview" && body.name === "my-tools"));
      assert.ok(!(await readiness.innerText()).includes(bundle.token));
      await close(); await open("my-tools");
      assert.equal(await action("Confirm credential restore").count(), 0);
      await action("Check credentials").click(); await action("Confirm credential restore").click();
      await credentials("Not yet confirmed");
      assert.ok(!(await readiness.innerText()).includes("private diagnostic"));
      assert.deepEqual(managed.at(-1), ["bundle-restore", {name: "my-tools", expected: "preview-only"}]);
      assert.equal(await action("Confirm credential restore").count(), 0);
      restored = {state: "ready"};
      await action("Check credentials").click(); await action("Confirm credential restore").click(); await credentials("Credentials match");
      await shows("New local process: {status}", "Not yet confirmed");
      assert.ok(!managed.some(([act]) => act === "managed-verify"));
      for (const [reply, label] of [["stale", "Still records an older command or version"], ["matching", "Matches this installation; active processes may still differ"], ["future", "Not yet confirmed"]]) {
        gateway = reply;
        await action("Probe a new local process").click(); await shows("Gateway launch record: {status}", label);
        await shows("New local process: {status}", "Initialized and listed tools");
        assert.deepEqual(managed.at(-1), ["managed-verify", {name: "my-tools"}]);
      }
      assert.equal(calls.length, repoCalls);
      assert.deepEqual(starts, []); assert.deepEqual(errors, []);
    });
  });
}
