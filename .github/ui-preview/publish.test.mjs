import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";

const repo = "zwenooo/magpie";
const script = new URL("./publish.mjs", import.meta.url).href;

// Run the real detect command, replacing HTTP at its boundary. No test can
// reach GitHub or use the developer's token.
function detect(t, override = {}) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "magpie-preview-test-"));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const out = path.join(dir, "outputs");
  const pr = {
    state: "open", labels: [], body: "Description", head: { sha: "abc1234", repo: { full_name: repo } },
    base: { repo: { full_name: repo } }, ...override,
  };
  const code = `
    const pr = ${JSON.stringify(pr)};
    const requests = [];
    globalThis.fetch = async (url, init = {}) => {
      const method = init.method || "GET";
      requests.push(method + " " + url);
      const data = url.includes("/files?") ? [{ filename: "internal/gui/assets/app.js" }] : pr;
      return { ok: true, status: 200, json: async () => data };
    };
    process.on("exit", () => console.log("REQUESTS=" + JSON.stringify(requests)));
    process.argv[2] = "detect";
    await import(${JSON.stringify(script)});
  `;
  const result = spawnSync(process.execPath, ["--input-type=module", "-e", code], {
    env: { ...process.env, GITHUB_TOKEN: "fixture-token", GITHUB_REPOSITORY: repo, PR: "123", GITHUB_OUTPUT: out },
    encoding: "utf8",
  });
  assert.equal(result.status, 0, result.stderr);
  const requests = JSON.parse(result.stdout.split("\n").find((line) => line.startsWith("REQUESTS=")).slice(9));
  return { outputs: fs.readFileSync(out, "utf8"), requests };
}

test("an open same-repository UI PR starts recording at its checked SHA", (t) => {
  const result = detect(t);
  assert.match(result.outputs, /^ui=true\nsha=abc1234\nnumber=123\n$/);
  assert.equal(result.requests.filter((r) => r.startsWith("PATCH ")).length, 1);
});

for (const [name, override] of [
  ["external fork", { head: { sha: "abc1234", repo: { full_name: "contributor/magpie" } } }],
  ["deleted fork", { head: { sha: "abc1234", repo: null } }],
  ["different target", { base: { repo: { full_name: "yetone/magpie" } } }],
  ["closed PR", { state: "closed" }],
]) {
  test(`${name} never records or writes a PR description`, (t) => {
    const result = detect(t, override);
    assert.equal(result.outputs, "ui=false\n");
    assert.deepEqual(result.requests, [`GET https://api.github.com/repos/${repo}/pulls/123`]);
  });
}
