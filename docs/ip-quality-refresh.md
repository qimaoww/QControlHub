# IP quality refresh visibility regression

Validated on 2026-10-12 against main `3b8b1bb2`. The browser is real Chromium;
API data and task transitions use the existing IP quality browser fixture, and
SVG images load over HTTP under the production CSP. No production account or
detector was used. PostgreSQL integration tests separately exercise real stored
reports, ownership and the same-date fallback query.

## Before: reproduction and trigger

1. Serve main's browser fixture:
   `QCH_BROWSER_SMOKE_SERVE_ONLY=1 node frontend/agents_browser_smoke.mjs`.
2. Open its printed origin with
   `/agents-browser-smoke.html?mode=ip-quality&preview=1#ip-quality` in Chromium.
   Let the fixture finish, leaving the completed IPv4/IPv6 report displayed.
3. Toggle the node's daily detection schedule and accept the confirmation.
   This produces the normal success notice and schedules the next poll.
4. Leave the page untouched for at least 16 seconds. Observe the notice's
   five-second expiry, and the five-, ten- and fifteen-second report reads.

Unchanged polls alone and a simulated 503/recovery kept the page visible on
main. A success notice followed by polling reproduced the disappearance. The
**entire `.ip-quality-workspace`** disappeared, including its controls, selected
node, report text and both images. The dock, node sidebar and nonzero-sized
`.workspace-main` remained. The next poll recreated the workspace, explaining
why checking DOM existence only immediately after a read missed the issue.

Measured timeline relative to the operation (milliseconds):

| Time | Request / DOM event |
| --- | --- |
| 24.6 | Initial `/ip-quality?timezone=Asia%2FShanghai` returned 200 |
| 5022.1 | Notice dismissal removed `data-spa-notice` and `data-refresh-key`; its exit still owned the connected div |
| 5030.2 | Automatic history read returned 200 |
| 5036.7 | Reconciliation reused the now unkeyed notice div as `.ip-quality-workspace` and removed the previous workspace |
| 5258.1 | The notice's exit completion removed that reused workspace |
| 5300.1 | First 50 ms sample with no node panel / empty main |
| 10037.0 | Next 200 history read began; workspace returned at 10048.9 |
| 15049.3 | Third automatic history read returned 200 |

There was no business JavaScript exception or failed report request at the
trigger. The original run had an unrelated fixture favicon 404. This is an
exit-callback/DOM-identity conflict, rather than an empty API result or failed
SVG download. [Before recording](ip-quality-refresh/before.mp4),
[disappeared page](ip-quality-refresh/disappeared.png), and
[request/DOM/sample data](ip-quality-refresh/measurements.json) preserve the
observation. The recording is an 18-second extract from the original longer
observation, including disappearance and recovery.

## Changes

- Notice dismissal removes the live notice immediately and preserves only
  isolated outgoing paint through the existing presence helper. Reconciliation
  cannot turn this div into unrelated live content. A shared motion regression
  exercises unkeyed incoming content during manual notice dismissal.
- Reconciliation removes departed siblings before placing survivors. Moving a
  surviving subtree ahead of an outgoing notice/error unnecessarily reconnected
  native details and replayed their `@starting-style` opacity/height entrance.
  The stricter read-only regression exposed this additional brief text hiding:
  `open` remained true, but details-content opacity and height returned to zero.
  Surviving nodes use native state-preserving moves when available. The IP
  read-error banner also keeps a hidden slot, so error/recovery does not need
  to reorder the report region on browsers using the insertion fallback.
- The IP workspace, result region and read-error banner have separate stable
  keys. Read failures and recovery preserve the loaded panel, image objects,
  expanded details, calendar month/open state and scroll position.
- Each read owns an AbortController plus account/navigation/sequence guards.
  Date switches cancel the previous read; even an uncancellable old response
  cannot commit. Route disposal releases the timer, request and visibility
  listener. Hidden tabs pause polling and restore with one immediate read.
- Node metadata refreshes with history, including ownership and permission
  removal. An authoritative metadata removal invalidates inaccessible cached
  nodes even if the parallel report read fails. 403 clears protected report
  snapshots; 401 uses the existing session reset/login flow.
- Valid cached reports survive temporary failures and malformed responses with
  error/retry feedback. Successful reads track deleted results per node and
  selected date, including when other nodes still have records, rather than
  keeping removed reports or labeling them as first-time results. The
  selected date/timezone and fallback node ownership are checked explicitly.
- Date-scoped SQL now retains the previous success **within that same local
  submission date** while another attempt is pending, running or failed.
  Latest views retain their existing cross-date fallback. Task IDs and image
  URLs always belong to the successful report. No schema migration is needed.
- Failed images show a separate retry control while text remains available.
  Unchanged images stay mounted; changed archive checksums replace the proper
  figure. Images remain lazy-loaded, same-origin, non-executable SVG previews.

## After: browser validation

The same real schedule-toggle/notice-expiry sequence produced three complete
automatic cycles and 330 samples at 50 ms intervals. Every workspace, node,
expanded text report and decoded image remained visible with positive width
and height. Both `/agents` and history reads returned 200; no runtime errors
were recorded. [After recording](ip-quality-refresh/after.mp4) covers the same
notice expiry and all three cycles.

The regression runner covers desktop (1280 px), mobile touch (390 x 844),
read-only access, desktop reduced motion and mobile reduced motion. Each mode
observes three unaccelerated five-second polls and at least 300 visibility
samples. It checks ancestor visibility/inertness, native details-content paint,
decoded image dimensions, page/panel dimensions and viewport reading after
scrolling. Expanded details, open calendar/month, scroll offsets and actual
panel/image object identities are retained through unchanged polling.

Additional scenarios cover pending/running/failed/canceled/succeeded tasks;
503/malformed data/recovery; slow reads; rapid node/date/latest switching;
navigation away with a pending response and reentry; hidden/visible events;
403 and node-permission changes; authoritative record removal; actual image
503/retry; and 401/login as another account while an obsolete response exists.

The CI hidden-tab case drives visibility events deterministically. A separate
native headed Chromium run also changed real active tabs without Playwright
focus emulation: the hidden page made **zero** history reads over 5.5 seconds
(64 -> 64), restoration made exactly one (64 -> 65), and the panel remained
692 x 2579.8 px. Its actual visibility events are included in the measurements.

Reproduce the focused matrix with:

```sh
QCH_BROWSER_SMOKE_MODES=ip-quality,ip-quality-mobile,ip-quality-readonly,ip-quality-reduced,ip-quality-mobile-reduced \
QCH_BROWSER_SMOKE_REPORT=output/playwright/measurements \
node frontend/agents_browser_smoke.mjs
```

The five modes are also part of the default `make check` browser group. Styles
are generated with `make generate-styles`. The PR Validation section reports
the completed full-project and required CI checks.

## Impact and rollback

The shared changes affect notice dismissal and DOM reconciliation on console
pages; the full browser suite exercises these common paths. The remaining
frontend changes are confined to IP quality. Each IP quality poll now makes an
additional metadata read to enforce current node visibility. Hidden tabs pause
both reads. The date fallback query adds a scoped prior-success lookup; the
existing latest lookup and authorization filters are retained. Reverting this
PR restores the previous frontend and query; no data or schema rollback is
required.
