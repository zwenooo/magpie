window.setUpdateMirror = function (mirror) {
  return writingPrefs(api("settings/update-mirror", { mirror }))
    .then((ns) => { prefs = ns; mirrorEditing = false; mirrorDraft = mirrorErr = ""; renderSettings(); status(t("Saved"), "ok", 1500); return ns; });
};
// Use a mirror… and Change open the field from mirroredit.js
const editMirror = (r, s, v) => import("./mirroredit.js").then(() => { mirrorEditing = true; mirrorDraft = v; renderUpdateMirror(r, s); }, statusErr);
window.renderUpdateMirror = function (r, s) {
  r.classList.add("update-mirror-row");
  const who = r.querySelector(".who"), val = r.querySelector(".val");
  who.querySelector(".sub")?.remove();
  val.replaceChildren();
  const sub = el("div", "sub");
  who.append(sub);
  const m = s.updateMirror || "";
  sub.textContent = mirrorErr || (m ? t("Updates are downloaded through this mirror, each checked against the release checksum")
    : t("Updates are downloaded from GitHub; a mirror can be used where GitHub is slow"));
  sub.classList.toggle("err", !!mirrorErr);
  sub.title = t("The release checksum is checked even when using a mirror; a file that doesn't match it is not installed.");
  if (mirrorEditing) return mirrorEdit(r, s, sub, val);
  if (m) {
    const official = el("button", "text", t("Use GitHub"));
    official.onclick = () => setUpdateMirror("").catch(statusTErr);
    const change = el("button", "text", t("Change"));
    change.onclick = () => editMirror(r, s, m);
    val.append(el("code", "", m), change, official);
  } else {
    const add = el("button", "text", t("Use a mirror…"));
    add.onclick = () => editMirror(r, s, "");
    val.append(el("code", "", "GitHub"), add);
  }
};
