// Why an update magpie puts in by itself can be trusted (ArcticLampyrid
// on X): two lines in the Automatic updates row's hover card
// (settingshints.js keeps a row's .sub lines out of sight and shows them
// there), and a mark by the row's name that says the card is there. A
// touch screen, with no hover, keeps the lines in place and no mark. What
// the lines say is the fork release adapter and download verifier.
// settingshints.js imports this, so the core doesn't carry it; Settings'
// About is drawn again whole, so each drawing of the row is watched for.
const about = document.getElementById("about");
const mark = () => {
  const row = about?.querySelector(":scope > .update-auto-row");
  const who = row?.querySelector(":scope > .who");
  if (!who || who.querySelector(".update-trust")) return;
  who.append(el("div", "sub update-trust", t("Fork updates are checked against SHA-256 checksums from the zwenooo/magpie release on GitHub")),
    el("div", "sub update-trust", t("A mirror cannot supply the checksum. A file that does not match is not installed")));
  const tip = el("span", "as-tip");
  tip.setAttribute("aria-hidden", "true"); // a screen reader reads the lines
  tip.append(svg(INFO_I, 12, 1.6));
  who.querySelector(".name")?.append(tip);
};
if (about) new MutationObserver(mark).observe(about, { childList: true });
mark();
