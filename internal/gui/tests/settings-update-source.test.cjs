// Run with Node's test runner and Playwright on the module path; see README.md.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");

const assets = path.resolve(__dirname, "../assets");
function server(lang, posts) {
  let cur = {
    theme: "light", lang, tray: "panel", currency: "usd", textSize: 100,
    proxy: "direct", proxyNow: "", proxySource: "off", pluginProxy: "", updateProxy: "",
    trayUsageEvery: 3, version: "test", dir: "/test", lanURLs: [],
    visionModels: [], imageGenModels: [], workbuddyCheckins: [],
    fx: { rate: 7.2, at: new Date().toISOString(), stale: false },
  };
  return async (route) => {
    const req = route.request(), url = new URL(req.url());
    const json = (data) => route.fulfill({ json: data });
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = ${JSON.stringify({ lang, theme: "light", web: true })};` });
    if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/state") return json({ agents: [], profiles: [], settings: { lang, theme: "light" }, fx: cur.fx });
    if (url.pathname === "/api/settings") {
      if (req.method() === "POST") {
        const body = req.postDataJSON();
        posts.push(body);
        // as settings.Save: the custom copy is the server's, set from the value
        cur = { ...cur, ...body };
      }
      return json(cur);
    }
    if (url.pathname === "/api/plugins") return json({ plugins: [] });
    if (url.pathname === "/api/usage/quotas") return json([]);
    if (url.pathname === "/api/groups") return json({ groups: [], models: [] });
    if (url.pathname.startsWith("/api/")) return json({});
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    return route.fulfill({ body: await fs.readFile(file), contentType });
  };
}

const languages = ["en", "zh", "zh-TW", "ja", "de"];

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const lang of languages) {
    for (const width of [1100, 420]) {
      test(`${engine} ${lang} ${width}px: the fork offers GitHub downloads, not the upstream release feed`, async (t) => {
        const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
        t.after(() => browser.close());
        const context = await browser.newContext({ viewport: { width, height: 760 }, reducedMotion: "reduce" });
        const page = await context.newPage();
        page.setDefaultTimeout(4000);
        page.setDefaultNavigationTimeout(60000);
        const posts = [], errors = [];
        page.on("pageerror", (e) => errors.push(e.message));
        await page.route("**/*", server(lang, posts));
        await page.goto("http://magpie.test/?view=settings&tab=about");
        const row = page.locator("#about .update-mirror-row");
        await row.locator("code").waitFor({ state: "attached" });
        assert.equal(await row.locator("code").textContent(), "GitHub");
        assert.equal(await row.locator(".sub").textContent(), await page.evaluate(() => t("Updates are downloaded from GitHub; a mirror can be used where GitHub is slow")));
        assert.equal(await page.locator("#updateSourceRow, #updateSourcePick").count(), 0);
        assert.deepEqual(posts, []);
        const over = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
        assert(over <= 0, `the page scrolls sideways by ${over}px`);
        assert.deepEqual(errors, []);
      });
    }
  }
}
