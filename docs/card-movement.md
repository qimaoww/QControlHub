# Card movement review

Node overview cards and traffic cards use the same movement controller. Other
card lists follow the saved node order or a fixed business order and do not gain
new sorting controls. Node batch selection keeps its existing selection behavior.

Both lists use the original traffic appearance from main `2bbd07b`: the
24x28 grip and 14px icon, 96% pointer preview opacity, lifted shadow, 12px
corners, 40% accent outer border and 60% accent 2px inset. The destination keeps
its normal content and opacity. Node headers and footers now paint an opaque
surface above the inset, matching the original traffic card's actual paint
order. Merely copying the shadow declaration previously left a visibly heavier
ring on nodes because their headers and footers were transparent.

Movement guidance is the destination border. There are no new visual instruction
rows, floating labels or success/cancellation notices. A hidden live region
announces positions, saves and cancellation; existing error notices report failed
saves. Discovered listeners retain their dashed business border.

## Coordinates, preview and lifecycle

The old hit selection used a preview centre and cached rectangles. Different
grab points, scrolling and reflow could therefore select a different slot than
the pointer indicated. The controller now records the actual pointer-down offset
from the painted card bounds, moves the preview immediately in viewport CSS
pixels and converts those coordinates into its layer's local scale. Pointer
following has no transition or added pickup scale. Traffic previews retain their
workspace layout, spacing and typography in an inert viewport layer.

Hit selection uses the pointer and current untransformed layout rectangles.
A small boundary dead band avoids repeated switching in gaps. Every animation
frame rechecks nested/document scrolling and layout, even with a stationary
pointer. Browser/CSS zoom, sidebar changes and responsive columns cannot leave
cached hit rectangles behind. Auto-scroll advances the innermost available
scroll container and accounts for the fixed navigation and top bar. Its per-frame
scroll writes explicitly use instant behavior so the page's default smooth
scrolling cannot delay or repeatedly restart them.

The dragged original card occupies the selected slot using temporary CSS order;
neighbours yield using the existing 420ms reorder animation. The DOM order stays
committed until a save succeeds. Release uses the final pointer position and the
same preview order. Landing/return animation starts at the ghost's last painted
bounds, with scale-correct translation. Starting another drag interrupts landing
without releasing the refresh gate between gestures. Hover/entrance transforms
are suspended during dragging; route exit and cancellation clean up captures,
frames, preview layers and temporary styles. Reduced motion keeps all positional
feedback with immediate placement.

## Reproducible browser preview

1. Run `make generate-styles`.
2. Run `QCH_BROWSER_SMOKE_SERVE_ONLY=1 node frontend/agents_browser_smoke.mjs`.
3. Use the printed localhost origin with
   `/agents-browser-smoke.html?mode=motion-cards&preview=1#node-settings`.
   This uses local fixtures and needs no server credentials.
4. Drag from the six-dot grip, or a non-control card surface with a mouse.
   Try the centre and four corners, reverse direction, then drag again before
   landing completes. The original grab offset stays fixed; the bordered card
   shows the final insertion slot while neighbours yield. Include the first and
   last slots and row gaps. Press Escape to return to the saved position.
5. Open **Traffic** from the navigation and repeat. Filter by a core, reorder the
   visible cards, then clear the filter to check that hidden positions are kept.
   Both monitored ports and the two discovered listener cards in this fixture
   support the same movement. Check that pickup preserves their header layout.
6. Use the theme control to inspect light and dark themes. With a touch device or
   browser device emulation, drag from the grip; an invisible 44x44 touch area
   keeps its original visible size. Swipe elsewhere on the card to scroll normally.
   Hold the grip near the viewport edge to scroll during a drag.
7. Focus a grip and use arrow keys for immediate moves. Space/Enter picks up a
   card; arrows choose a slot; Space/Enter drops; Escape cancels. Reload the page
   to verify the saved order. Enable `prefers-reduced-motion: reduce` in browser
   rendering settings and repeat: the border and pointer tracking remain complete.

To inspect failure recovery, temporarily run this in the preview's developer
console, then move a card:

```js
const originalSetItem = Storage.prototype.setItem;
Storage.prototype.setItem = function (key, value) {
  if (/qcontrolhub:(node|traffic)-card-order/.test(key)) {
    throw new DOMException("Preview storage failure", "QuotaExceededError");
  }
  return originalSetItem.call(this, key, value);
};
```

The card returns to the last saved position and the existing error notice reports
that recovery. Restore with `Storage.prototype.setItem = originalSetItem`, or
reload the preview. A subsequent move must save normally.

## Native input regression and recordings

```sh
QCH_BROWSER_SMOKE_MODES=motion-cards-coordinates,motion-cards-coordinates-reduced,motion-cards-coordinates-mobile,motion-cards-coordinates-mobile-reduced,motion-cards,motion-cards-reduced,motion-cards-mobile,motion-cards-mobile-reduced node frontend/agents_browser_smoke.mjs
QCH_BROWSER_SMOKE_MODES=motion-cards-coordinates QCH_BROWSER_SMOKE_ZOOM=1.25 QCH_BROWSER_SMOKE_DPR=1.5 node frontend/agents_browser_smoke.mjs
make check
```

The Chromium runner dispatches native mouse/touch input. Coordinate cases measure
`hypot(ghost.left + grab.x - pointer.x, ghost.top + grab.y - pointer.y)` against
the delivered pointer events and compare the last preview with the committed
order. They cover centre/corner/handle grabs, slow and fast motion, reversals,
interrupting landing, variable heights, multiple rows/columns, first/last slots,
gaps, stationary reflow, sidebar offset, CSS zoom and nested manual/edge scrolling.
Touch starts on the grip; normal card content remains available for scrolling.
For deterministic interruption coverage, the fixture waits for an in-flight
landing to pause before selecting a grip that is actually visible and hit-testable.
Its short-card fixture keeps the header from shrinking underneath the body, and
uses a longer reorder token so the normal animation safety deadline cannot fire
while a loaded CI driver aims. Production pointer-down must cancel the existing
landing and preserve its grab point. Other gestures and the recordings run with
normal animation playback.
Browser zoom uses Chromium's actual default zoom setting, separately from display
scale. At 125% zoom and 150% display scale the observed DPR is 1.875.

The lifecycle matrix covers both themes, nodes, monitored traffic and discovered
listeners; threshold, keyboard moves, identity/focus/drafts, polling, Escape,
touch cancellation/lost capture, outside drops, failed writes/recovery, a fresh
document loading the saved order, filtered slots, normal touch scrolling, edge
scrolling and route exit. It also checks the destination's original traffic
border, radius, opaque content and header paint rather than just CSS shadow values.

- [Desktop native mouse recording](card-movement/desktop.mp4)
- [Mobile native touch recording](card-movement/mobile.mp4)
- [Measured gestures and final orders](card-movement/measurements.json)
- Destination appearance: [node](card-movement/node-target.png),
  [traffic](card-movement/traffic-target.png) (same light theme and drag state).

The recording-only cursor follows delivered pointer events and highlights a
press. The clips show node cards in light theme, traffic in dark theme, reversals,
actual drops and Escape return. Recreate them against the local fixture origin:

```sh
node scripts/record-card-movement.mjs http://127.0.0.1:PORT desktop
node scripts/record-card-movement.mjs http://127.0.0.1:PORT mobile
```

## Actual validation

Validated on Linux Chromium after fetching main `df1ad92`. The original traffic
styles from `2bbd07b` are unchanged by that main update.

- `make generate-styles`, `make non-browser-checks`: passed.
- `go test -p 1 ./... -skip '^TestAgentBrowserRuntimeSmoke$'`: passed. The browser
  wrapper is excluded here; native card modes run separately and in `make check`.
- Desktop coordinate modes: 34 measured gestures each, normal and reduced motion.
  Maximum grab-point error was 0.095 CSS pixels, including live CSS zoom.
- Touch coordinate modes: 24 measured gestures each, normal and reduced motion.
  Maximum grab-point error rounded to 0.0000 CSS pixels.
- Browser 125% zoom with 150% display scale: 34 gestures passed; maximum error
  was 0.189 CSS pixels. Every successful drop matched its last preview.
- Both recordings verify their own pointer error and preview/final order.
The initial `make check` ran to exit 2 after all eight card modes passed, at
`motion-selection` / `quota refresh retains focus`. This also failed on unmodified
main `df1ad92`. During merge review, the quota launcher now restores focus after
its temporary disabled state, provided focus has not moved to another control.
All four selection modes pass with that repair.

Merge review also found that the new interruption fixture could aim at a grip
occluded by another landing card or by its artificially compressed body. The
fixture changes described above fix that input race. All four coordinate modes
and all four existing motion modes pass. Navigation reversal uses a controlled
request gate and compares the captured reversal opacity with the native resume
keyframe, avoiding stale wall-clock samples. Full-check and CI results are
recorded in the PR.

## Persistence and rollback

Orders remain synchronous account-scoped browser preferences under the existing
keys. Traffic sorting changes only visible slots in the complete order. Writes
are confirmed before DOM order is committed; failures retain the last saved order.
There are no asynchronous order requests that can complete out of sequence.
Data refreshes wait until movement settles and then read the latest preference.

Rollback is a normal revert followed by `make generate-styles`. Existing storage
keys and formats are unchanged, so the previous implementation can read saved
orders. The browser matrix emulates mobile Chromium; physical-device Safari is
not covered by the automated runner.
