# Card movement review

Node overview cards and traffic cards use the same movement controller. Other
card lists follow the saved node order or a fixed business order and do not gain
new sorting controls. Node batch selection keeps its existing selection behavior.

Both lists share the original traffic drag declarations from main `2bbd07b`,
with their original values: the 24x28 grip uses a 14px icon, muted hover colours
and a 6px radius; the source fades to 40%; the pointer preview uses 96% opacity,
the existing lifted shadow and stacking level 60. The destination uses the same
40% accent border colour and 60% accent 2px inset. Other cards stay in place
during dragging. There is no added pickup scale or shadow animation. Releasing
commits the selected position and uses the existing `--motion-reorder` timing
(420ms) to animate the real card from the preview while its neighbours settle.
Cancellation or failure animates the real card back to its saved position.

Movement guidance is limited to the target-card border. No instruction rows,
floating labels, empty placeholder tiles or success/cancellation notifications
are added. Existing error notices report failed saves; a hidden live region
announces positions, saved order and cancellation to screen readers.

Traffic previews retain the workspace's header layout, type sizes, spacing and
footer layout in a non-interactive viewport layer. Discovered listeners retain
their original dashed business border during movement and receive the same
destination inset. The viewport layer is removed on landing, cancellation or
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
The matrix checks stationary card slots, visible source contents, previews
without added scaling, the original 96% opacity and target borders through
reversals and last-slot keyboard moves. Touch checks include the invisible
expanded hit area without enlarging the visible grip.
It also compares live and preview traffic header/footer styles and exercises
discovered listener pickup, reversals, drop, cancellation and keyboard movement
in both themes, including cleanup of the viewport layer.

## Actual validation

Validated after syncing latest main `df1ad92` with Chromium on Linux. The original
traffic styles referenced from `2bbd07b` are unchanged by that main update.

- `make generate-styles` and `make styles-check`: passed.
- The four card movement browser modes above: passed, with both card types and
  both themes in each mode.
- Manual Playwright comparison against unmodified main traffic cards: all 22
  inspected source, grip, icon, preview and destination paint properties match
  for both node and traffic cards in both themes. Keyboard focus remains visible;
  pointer dragging does not introduce an extra grip outline.
- On the initial commit `0ed0159`, all 58 existing browser modes outside the
  motion suites passed across separate batches, as did the four popup motion
  modes. The card matrix and non-browser checks were rerun after the style update.
- `node frontend/module_smoke.mjs` and the module/smoke policy tests: passed.
- `make non-browser-checks`: passed.
- `go test -p 1 ./... -skip '^TestAgentBrowserRuntimeSmoke$'`: passed after syncing
  latest main. The browser wrapper was excluded; the native card modes ran
  through `make check` as described below.
- `make check`: all four card movement modes passed, then it failed (exit 2) on
  the existing `motion-selection` assertion `quota refresh retains focus`
  (focus becomes the document body). The same assertion fails on an unmodified
  checkout of latest main `df1ad92`.
- Earlier supplemental existing `motion` runs on main `2bbd07b` also failed at
  navigation reversal timing assertions, before any card gesture. An unmodified
  checkout of that main reproduces `reverse navigation must not flash to a fully
  opaque frame`.

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
