// The fork's CodeZ preset uses the ordinary relay flow. Rendering or opening
// its links must not turn it into a partner or send partner statistics.
// All API requests and links stay inside this fixture; no key is saved.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");
const assets = path.resolve(__dirname, "../assets");
const codez = {
  id: "codez", name: "CodeZ", icon: "generic", kind: "relay",
  chat: "https://api.codezsy.com/v1", responses: "https://api.codezsy.com/v1",
  anthropic: "https://api.codezsy.com", website: "https://codezsy.com",
  keysUrl: "https://codezsy.com/console/token",
};

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  test(`${engine}: CodeZ is an ordinary relay without partner traffic`, async (t) => {
    const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
    t.after(() => browser.close());
    for (const lang of ["en", "zh", "zh-TW", "ja", "de"]) {
      await t.test(lang, async (sub) => {
        const page = await browser.newPage({ viewport: { width: 1100, height: 900 } });
        sub.after(() => page.close());
        const partnerRequests = [], opened = [], writes = [], external = [], errors = [];
        page.on("pageerror", e => errors.push(e.message));
        await page.route("**/*", async (route) => {
          const u = new URL(route.request().url());
          const json = data => route.fulfill({ json: data });
          if (u.hostname !== "magpie.test") {
            external.push(u.href);
            return route.fulfill({ status: 404, body: "fixture blocks external requests" });
          }
          if (u.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs={lang:${JSON.stringify(lang)},theme:"light",web:false};` });
          if (u.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
          if (u.pathname === "/api/state") return json({ agents: [], profiles: [], settings: { lang, theme: "light" } });
          // An existing provider keeps the sheet closed until Add is clicked;
          // the empty-provider flow opens it automatically and hides Add.
          if (u.pathname === "/api/providers") return json({ providers: [{
            id: "existing", name: "Existing relay", icon: "generic", models: [], agents: [],
            chat: "https://existing.example/v1", key: { set: true, masked: "fixture" },
          }], presets: [
            { id: "openai", name: "OpenAI", kind: "vendor", icon: "openai", chat: "https://api.openai.example/v1" }, codez,
          ], excluded: [], gateway: { running: true, window: true } });
          if (u.pathname === "/api/groups") return json({ groups: [], pools: [] });
          if (u.pathname === "/api/gateway/trace") return json({ routes: [] });
          if (u.pathname === "/api/partner") {
            partnerRequests.push(route.request().postDataJSON());
            return route.fulfill({ status: 204, body: "" });
          }
          if (u.pathname === "/api/open") { opened.push(route.request().postDataJSON().url); return json({}); }
          if (u.pathname.startsWith("/api/")) {
            if (route.request().method() !== "GET" && /^\/api\/providers?(\/|$)/.test(u.pathname)) writes.push(u.pathname);
            return json({});
          }
          const file = path.join(assets, u.pathname === "/" ? "index.html" : u.pathname);
          const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
          try { return route.fulfill({ body: await fs.readFile(file), contentType }); }
          catch { return route.fulfill({ status: 404, body: "" }); }
        });
        await page.goto("http://magpie.test/?view=providers");
        await page.locator("#addProvider").click();
        const sheet = page.locator("#addSheet");
        await sheet.locator(".tile").first().waitFor();
        const labels = await page.evaluate(() => ({ partners: t("Partners"), sponsors: t("magpie's sponsors"), relays: t("Relays"), key: t("Get a key ↗") }));
        const headings = await sheet.locator(".kind").allTextContents();
        assert.ok(headings.every(s => !s.includes(labels.partners) && !s.includes(labels.sponsors)), "no partner section before searching");
        assert.equal(await page.locator("#addProvider").evaluate(el => el.classList.contains("has-new")), false);
        await sheet.locator("input").first().fill("cOdEz");
        const tile = sheet.locator(".tile", { hasText: "CodeZ" });
        await tile.waitFor();
        assert.deepEqual(await sheet.locator(".tile .n").allTextContents(), ["CodeZ"]);
        assert.deepEqual(await sheet.locator(".kind b").allTextContents(), [labels.relays]);
        assert.equal(await tile.locator(".badge").count(), 0);
        assert.match(await tile.getAttribute("title"), /api\.codezsy\.com/);
        assert.equal(await tile.locator('img[src*="codez"]').count(), 0, "generic icon needs no new logo");
        await tile.click();
        const editor = page.locator("#modal .editor");
        await editor.waitFor();
        assert.equal(await editor.locator(".ehead b").innerText(), "CodeZ");
        const site = editor.locator(".ehead .link.site");
        assert.equal(await site.getAttribute("data-url"), codez.website);
        assert.equal(await site.innerText(), "codezsy.com ↗");
        assert.deepEqual(await editor.locator(".eps .ep").evaluateAll(rows => rows.map(row => [row.querySelector(".pl").textContent, row.querySelector("code").textContent])), [
          ["OpenAI", codez.chat], ["Responses", codez.responses], ["Anthropic", codez.anthropic],
        ]);
        assert.equal(await editor.locator('input[type="password"]').inputValue(), "");
        const siteOpened = page.waitForResponse(r => r.url().endsWith("/api/open"));
        await site.click();
        await siteOpened;
        const keyOpened = page.waitForResponse(r => r.url().endsWith("/api/open"));
        await editor.getByRole("button", { name: labels.key, exact: true }).click();
        await keyOpened;
        assert.deepEqual(opened, [codez.website, codez.keysUrl]);
        assert.deepEqual(partnerRequests, [], "ordinary relay views and links do not count partner statistics");
        assert.deepEqual(writes, [], "reading the preset does not save or test credentials");
        assert.deepEqual(external, [], "links and catalog requests stay inside the fixture");
        assert.deepEqual(errors, []);
      });
    }
  });
}
