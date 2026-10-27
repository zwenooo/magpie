const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");
const assets = path.resolve(__dirname, "../assets");

for (const engine of ["chromium", "webkit"]) {
  test(`${engine}: bundled Cursor setup requires explicit consent`, async (t) => {
    const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
    t.after(() => browser.close());
    for (const lang of ["en", "zh", "zh-TW", "ja", "de"]) {
      await t.test(lang, async () => {
        const page = await browser.newPage();
        let approved = false, approvals = 0, connects = 0;
        await page.route("**/*", async (route) => {
          const u = new URL(route.request().url());
          if (u.pathname === "/boot.js") return route.fulfill({ contentType:"text/javascript", body:`window.bootPrefs={lang:${JSON.stringify(lang)},web:true};` });
          if (u.pathname === "/api/state") return route.fulfill({ json:{agents:[],profiles:[],settings:{lang}} });
          if (u.pathname === "/api/cursor-backend/approve") { approvals++; approved = true; return route.fulfill({ json:{ok:true} }); }
          if (u.pathname === "/api/agents/connect/cursor-byok") {
            connects++;
            return route.fulfill(approved ? {json:{ok:true}} : {status:400,json:{code:"cursor_setup_required",error:"setup required"}});
          }
          if (u.pathname.startsWith("/api/")) return route.fulfill({json:{}});
          const file=path.join(assets,u.pathname === "/" ? "index.html" : u.pathname);
          const contentType={".html":"text/html",".js":"text/javascript",".css":"text/css",".svg":"image/svg+xml"}[path.extname(file)];
          return route.fulfill({body:await fs.readFile(file),contentType});
        });
        await page.goto("http://magpie.test/");
        const start=()=>page.evaluate(()=>{window.setupResult=null;api("agents/connect/cursor-byok",{}).then(()=>window.setupResult="connected",()=>window.setupResult="cancelled");});
        await start();
        const dialog=page.getByRole("alertdialog");
        await dialog.waitFor();
        const expected=await page.evaluate(()=>t("Connect Cursor with Magpie?"));
        assert.equal(await dialog.locator("h2").innerText(),expected);
        assert.equal(approvals,0);
        await dialog.locator("button").first().click();
        await page.waitForFunction(()=>window.setupResult === "cancelled");
        assert.equal(approvals,0);
        assert.equal(connects,1);
        await start();
        await dialog.waitFor();
        await dialog.locator("button").last().click();
        await page.waitForFunction(()=>window.setupResult === "connected");
        assert.equal(approvals,1);
        assert.equal(connects,3);
        await page.close();
      });
    }
  });
}
