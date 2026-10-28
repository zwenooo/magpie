# Open: icon tile audit includes installation assets and misses Penguin

Observed 2026-10-28 while checking the CodeZ logo on base `b6ea5b1e`.

`node --test internal/gui/tests/icon-tile.test.cjs` fails because
`apple-touch-icon.png` is opaque but absent from the `.ic` tile-size rule.
That file and `icon-512.png` are home-screen installation assets listed in
`assets/manifest.json`, not provider logos. A temporary test-only exclusion
of those manifest assets then reveals `penguin-color` missing from the same
tile rule. The temporary exclusion was reverted; the existing audit remains
unchanged and this issue remains open.

Resolve the audit's asset scope and verify Penguin's intended displayed size
in a separate change. Do not describe the full icon audit as passing meanwhile.
The CodeZ add-sheet image-loading checks pass in Chromium and WebKit across
all five languages; its transparent image does not meet the opaque-tile test.
