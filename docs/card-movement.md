# Card movement review

Node overview cards and traffic cards use the same movement controller. Other
card lists follow the saved node order or a fixed business order and do not gain
new sorting controls. Node batch selection keeps its existing selection behavior.

Only the drop border provides visible movement guidance: a solid accent edge,
soft halo and inset line surround the insertion slot. The preview follows the
pointer directly, and surrounding cards move into their new positions. No new
instruction rows, floating labels or success/cancellation notifications are added.
Existing error notices report failed saves; a hidden live region announces
positions, saved order and cancellation to screen readers.

## Reproducible browser preview

1. Run `make generate-styles`.
2. Run `QCH_BROWSER_SMOKE_SERVE_ONLY=1 node frontend/agents_browser_smoke.mjs`.
3. Use the printed localhost origin with
   `/agents-browser-smoke.html?mode=motion-cards&preview=1#node-settings`.
   This uses local fixtures and needs no server credentials.
4. Drag a card's six-dot grip to another slot. Reverse direction before releasing,
   and repeat immediately after dropping. Inspect the moving border, pickup,
   neighbour movement and landing. Press Escape to return to the saved position.
5. Open **Traffic** from the navigation and repeat. Filter by a core, reorder the
   visible cards, then clear the filter to check that hidden positions are kept.
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

## Actual validation

Validated against main `2bbd07b` with Chromium on Linux:

- `make generate-styles` and `make styles-check`: passed.
- The four card movement browser modes above: passed, with both card types and
  both themes in each mode.
- All 58 existing browser modes outside the motion suites: passed across
  separate batches. The four existing popup motion modes also passed.
- `node frontend/module_smoke.mjs` and the module/smoke policy tests: passed.
- `make non-browser-checks`: passed.
- `go test -p 1 ./... -skip '^TestAgentBrowserRuntimeSmoke$'`: passed. The browser
  runner was exercised separately; the skipped wrapper runs the same suite.
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
