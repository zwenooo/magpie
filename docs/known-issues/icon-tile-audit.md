# Resolved: icon tile audit includes installation assets and misses Penguin

Observed 2026-10-28 while checking the CodeZ logo on base `b6ea5b1e`.

`node --test internal/gui/tests/icon-tile.test.cjs` failed because
`apple-touch-icon.png` is opaque but absent from the `.ic` tile-size rule.
That file and `icon-512.png` are home-screen installation assets listed in
`assets/manifest.json`, not provider logos. A temporary test-only exclusion
of those manifest assets then reveals `penguin-color` missing from the same
tile rule.

Resolved on 2026-10-28 before the second fork release. The audit now excludes
only installation icons declared by `manifest.json`; every other logo is still
measured. Penguin meets the existing solid-tile thresholds and now uses the same
82% size rule as the other filled logos. The rendered-size test includes Penguin.

Both the asset audit and rendered-size checks pass in Chromium and WebKit
(4 tests). Before the CSS fix, the same checks failed in both engines because
Penguin was absent from the tile rule and rendered at 22/22 pixels instead of 82%.
The CodeZ add-sheet image-loading checks pass in Chromium and WebKit across
all five languages; its transparent image does not meet the opaque-tile test.
