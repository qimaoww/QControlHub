# Card movement review

Node overview cards and traffic cards use the same movement controller. Other
card lists follow the saved node order or a fixed business order and do not gain
new sorting controls. Node batch selection keeps its existing selection behavior.

Both lists use the original traffic-card drag style: the source card fades to
40% opacity, a full-size lifted preview follows the pointer directly, and the
destination card receives an accent border with a 2px inset highlight. Other
cards stay in place during dragging. Releasing commits the selected position
and animates the real card from the preview's bounds while its neighbours settle.
Cancellation or failure animates the real card back to its saved position.

Movement guidance is limited to the target-card border. No instruction rows,
floating labels, empty placeholder tiles or success/cancellation notifications
are added. Existing error notices report failed saves; a hidden live region
announces positions, saved order and cancellation to screen readers.

Traffic previews retain the workspace's header layout, type sizes, spacing and
footer layout in a non-interactive viewport layer. Discovered listeners keep
their dashed business border at rest and use the same solid border as monitored
ports while dragging. The viewport layer is removed on landing, cancellation or
route exit.

## Reproducible browser preview

1. Run `make generate-styles`.
2. Run `QCH_BROWSER_SMOKE_SERVE_ONLY=1 node frontend/agents_browser_smoke.mjs`.
3. Use the printed localhost origin with
   `/agents-browser-smoke.html?mode=motion-cards&preview=1#node-settings`.
   This uses local fixtures and needs no server credentials.
4. Drag a card's six-dot grip to another card. Reverse direction before releasing,
   and repeat immediately after dropping. Inspect the faded source, full-size
   preview and target-card border; the grid stays still until release. Include
   the last card as a destination. Press Escape to return to the saved position.
5. Open **Traffic** from the navigation and repeat. Filter by a core, reorder the
   visible cards, then clear the filter to check that hidden positions are kept.
   Both monitored ports and the two discovered listener cards in this fixture
   support the same movement. Check that pickup preserves their header layout.
6. Use the theme control to inspect light and dark themes. With a touch device or
   browser device emulation, drag from the 44px grip; swipe elsewhere on the card
   to scroll normally. Hold the grip near the viewport edge to scroll during a drag.
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

## Automated regression

```sh
QCH_BROWSER_SMOKE_MODES=motion-cards,motion-cards-reduced,motion-cards-mobile,motion-cards-mobile-reduced node frontend/agents_browser_smoke.mjs
make check
```

The existing Chromium runner dispatches native mouse/touch input and inspects
both themes. Coverage includes the movement threshold, fast direction reversals,
background polling, stable card identity/focus/form drafts, keyboard pickup/drop,
Escape, lost capture/touch cancellation, outside drops, failed storage writes,
a fresh document reading saved order, filtered traffic slots, edge scrolling,
normal touch scrolling and route exit. Reduced motion runs the same operations.
The matrix checks stationary card slots, visible source contents, full-size
previews and target borders through reversals and last-slot keyboard moves.
It also compares live and preview traffic header/footer styles and exercises
discovered listener pickup, reversals, drop, cancellation and keyboard movement
in both themes, including cleanup of the viewport layer.

## Actual validation

Validated against main `2bbd07b` with Chromium on Linux:

- `make generate-styles` and `make styles-check`: passed.
- The four card movement browser modes above: passed, with both card types and
  both themes in each mode.
- On the initial commit `0ed0159`, all 58 existing browser modes outside the
  motion suites passed across separate batches, as did the four popup motion
  modes. The card matrix and non-browser checks were rerun after the style update.
- `node frontend/module_smoke.mjs` and the module/smoke policy tests: passed.
- `make non-browser-checks`: passed.
- On the initial commit `0ed0159`, `go test -p 1 ./... -skip '^TestAgentBrowserRuntimeSmoke$'`:
  passed. The browser runner was exercised separately; the skipped wrapper runs
  the same suite.
- After the style update, `go test -p 1 ./frontend -skip '^TestAgentBrowserRuntimeSmoke$'`:
  passed.
- `make check`: ran and failed on the existing `motion-selection` assertion
  `quota refresh retains focus` (focus becomes the document body). The same
  assertion fails on an unmodified checkout of main `2bbd07b`.
- Supplemental existing `motion` runs also failed at the navigation reversal
  timing assertions, before any card gesture. An unmodified main checkout
  reproduces `reverse navigation must not flash to a fully opaque frame`.

The unrelated quota and navigation failures are recorded here rather than
reported as successful checks. Their production behavior is outside this change.

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
