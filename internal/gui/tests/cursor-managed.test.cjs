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
        const copy = await page.evaluate(() => {
          const a = { id:"cursor-byok", name:"Cursor (Magpie)", icon:"cursor", wired:false, menu:true,
            fields:[{key:"provider",label:"provider",value:"",options:[{value:"magpie",label:"magpie"}]}],
            models:{shown:1,listed:1} };
          const off = connectLine(a, "ok");
          const on = {...a,wired:true};
          const drift = connectLine({...on,drift:{kind:"unwired",detail:"fixture backend stopped"}}, "ok");
          state.agents = [a]; showAllAgents = true; renderAgents();
          return {
            off:off.querySelector(".ag-st-t").textContent,
            compact:connectSaid(a), on:connectLine(on,"ok").querySelector(".ag-st-t").textContent,
            wantOff:t("Not connected · switch on to use Magpie's models in Cursor"),
            wantOn:t("Connected · choose Magpie's models in Cursor"),
            tip:connectSwitch(a).title,
            wantTip:t("Magpie includes and manages the Cursor backend; no separate Cursor BYOK installation is needed."),
            drift:drift.classList.contains("bad") && drift.querySelector(".ag-st-t").title === "fixture backend stopped",
            sibling:connectSaid({...a,id:"claude",name:"Claude Code"}),
            siblingExpected:t("Not connected · {agent} uses its own settings",{agent:"Claude Code"}),
          };
        });
        assert.equal(copy.off,copy.wantOff);
        assert.equal(copy.compact,copy.wantOff);
        assert.equal(copy.on,copy.wantOn);
        assert.equal(copy.tip,copy.wantTip);
        assert.equal(copy.drift,true,"backend errors must retain priority over connected copy");
        assert.equal(copy.sibling,copy.siblingExpected);
        if(lang === "zh") assert.equal(copy.off,"未接入 · 开启后可在 Cursor 中使用 Magpie 的模型");
        for(const width of [1100,420]) {
          await page.setViewportSize({width,height:700});
          assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth <= window.innerWidth),"copy overflow");
          if(process.env.ARTIFACT_DIR && lang === "zh" && engine === "chromium") {
            await fs.mkdir(process.env.ARTIFACT_DIR,{recursive:true});
            await page.screenshot({path:path.join(process.env.ARTIFACT_DIR,`cursor-copy-${width}.png`)});
          }
        }
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
