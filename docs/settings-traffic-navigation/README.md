# Settings navigation, traffic reuse and dialog gestures

Four user-facing defects share one cause family: a rendered page was treated as
disposable. A settings section link, a traffic sidebar scope and a card dialog all
sat inside markup the shell rebuilds, so navigation reloaded the route, the poll
re-read the same three collections and a modal press was claimed by the card
movement controller. This change keeps the rendered page and lets each of those
interactions move inside it.

## Confirmed causes

1. **Settings directory.** The sidebar printed `class="active"` on its first
   entry (`frontend/modules/shell-context.js` in the base), so the highlight was
   constant and unrelated to `state.anchor`. `routeForHash` had no
   `settings-api-keys` alias, so `#settings-api-keys` fell through to the
   dashboard default. Any section link produced a `hashchange`, which ran a full
   `renderOnce`: `/settings`, `/settings/deployment` and `/settings/api-keys`
   were read again, the form was re-rendered and the anchor scroll ran afterwards.
2. **Traffic reads.** `traffic()` read `/agents`, `/traffic-policies` and
   `/traffic-endpoints` on every call, and it is called on route entry, on a
   sidebar node scope change (a pure hash change) and by the 2 s poll. A
   `/agents` read also triggers the node-order read, so one poll cost four
   requests. Nothing described which account the data belonged to or how old it
   was, so nothing could be reused.
3. **Modal gestures.** `bindCardMove` listens for `pointerdown` on the card
   grid. A quota dialog is a DOM child of its card, so a press on the dialog still
   resolved a card and started a drag with `preventDefault` and pointer capture;
   releasing over another slot committed and saved an order.
4. **Topbar.** The breadcrumb was plain text, so the panel name and page title
   could not navigate.

## Changes

**Settings directory (`frontend/modules/settings-nav.js`, routes, app shell).**
The directory markup moved into a focused module that receives `can` and renders
the same entries, including the administrator-only API Key entry. Selection is
resolved from the sections the page actually rendered, so an unrendered section
can never be selected. `routeForHash` learns `settings-api-keys` and keeps the
`settings-` prefix with the settings page after the `settings-node-`/`node-`
branches, so `#settings-node-<id>` still belongs to node settings. A section
link now moves inside the rendered page: `app.js` intercepts route-neutral
section hashes and calls the page's `focusSection`, which updates
`state.anchor`, repaints the selection, scrolls with the existing
`scroll-margin-top` and returns without reading or re-rendering anything.
Reading position is sampled with a merged `requestAnimationFrame`, ignores
programmatic scroll settling, and writes the URL with `replaceState` so history
keeps no scroll entries; the end of the scroller is attributed to the last
section because it can never cross the reading line. A sample never overrides a
URL that already names that section, which is what made a section link pre-empt
its own `hashchange` and reload the route. Anchor scrolling in `app.js` now
runs only when the anchor actually changed, so a background refresh cannot pull
the reader back.

**Traffic snapshot (`frontend/modules/traffic-model.js`, `traffic.js`).**
One complete read per account scope is kept with a 30 s window, deliberately
below the 45 s rate-staleness window of `trafficRateForDisplay`, so reused rates
stay truthful. The snapshot is rejected when `state.data`, `state.session` or
the `agents.read` decision changed identity, and when the navigation epoch did
not advance, so a poll, a save, a batch action or a sync inside one navigation
still reads the server. Sidebar node scope changes and returning to the page
render from the snapshot without a request; quota writes clear it first. The
refresh channel additionally pins the account, so a response read for another
account cannot render. Background renders wait while a quota dialog with unsaved
draft is open and apply once, when the last one closes; status dialogs stay live
through `data-refresh-live`. The poller keeps its 2 s cadence: live metrics are
unchanged, only navigation-driven reads disappeared.

**Dialog pointer ownership (`frontend/modules/card-move.js`).** Presses inside a
`dialog` or an `[inert]` subtree are left to the browser before any pointer
capture or `preventDefault`. Text selection, control focus, dialog scrolling,
backdrop dismissal and Escape keep their native behaviour; grips, card surfaces,
the keyboard path (`aria-pressed`, direction keys, Enter/Space) and the live
region are untouched.

**Topbar (`frontend/modules/shell-view.js`).** The panel name links to the
dashboard or, without `overview.read`, to the existing `my-quota` fallback, so
the link never bounces through a redirect. The page title links to the route's
module entry (`agent-config` keeps its `live-config` entry). Anchors stay inside
the existing `<span>`/`<b>`, so the shell styles, the separator, the role badge
and the phone layout that hides the panel name are unchanged; no CSS was added.

## Validation

Fast checks on the final tree:

```
node frontend/module_smoke.mjs                                  exit 0
go test ./frontend -count=1 -skip '^TestAgentBrowserRuntimeSmoke$'  ok
node --test frontend/module_policy_test.mjs frontend/smoke_policy_test.mjs  3 pass
node scripts/generate-styles.mjs --check                        pass (no style change)
node --test .github/scripts/check-pr.test.mjs                   pass
gofmt -l <changed go files>                                     empty
git diff --check                                                clean
```

Real browser (Chromium on Linux, production modules served with the in-memory
fixture API, no browser-specific branch):

```
node agents_browser_smoke.mjs   -> agents browser runtime smoke passed (full mode matrix, see evidence/matrix.log)
```

Targeted modes for the touched surfaces also pass individually: `shell-layout`,
`shell-layout-mobile`, `capabilities-settings`, `capabilities-settings-mobile`,
`capabilities-settings-readonly`, `motion`, `motion-mobile`, `motion-cards`,
`motion-cards-mobile`, `traffic-layout`, `traffic-layout-mobile`,
`traffic-layout-dense`, `traffic-layout-dense-mobile`, `dashboard`,
`dashboard-mobile`. The `motion-cards` modes are the browser regression for the
dialog press: the quota and status dialogs of a card are pressed and dragged, and
the test asserts no preview, no drop slot, no order change, no saved order and an
unprevented `pointerdown`.

Behaviour records (request log captured by wrapping the page's `fetch`, same
fixture, same viewport):

| Scenario | Before | After |
| --- | --- | --- |
| Direct `#settings-basic` | highlight on `#settings-engines`, no `aria-current` | highlight `#settings-basic`, `aria-current="location"` |
| Direct `#settings-api-keys` | route `dashboard`, section absent | route `settings`, hash kept, API Key section highlighted |
| Section link | `preferences/node-order`, `settings/deployment`, `settings`, `settings/api-keys` | no request; draft and save state kept; same form node |
| History back / forward | highlight `#settings-engines` for both | `#settings-data` and `#settings-notify` |
| Traffic sidebar node scope | 5 requests | 1 request (`preferences/node-order` from `/agents` only) |
| Return to traffic | 5 requests | 1 request |
| Press and drag inside the open quota dialog | body gains `card-moving traffic-card-dragging`, preview created, card order changed | no class, no preview, order unchanged, no saved order, Escape closes, text selectable |
| 12 filter changes in 475 ms | — | 4 requests: one background poll; filter changes issue none |
| Idle 6 s on traffic | 12 requests (3 polls) | 12 requests (unchanged live cadence) |

Machine-readable copies: [measurements](evidence/measurements.json),
[capture before](evidence/capture-before.json), [capture after](evidence/capture-after.json).

## Screenshots and recordings

Identical fixture, account, theme, viewport and scroll state within each pair.
Desktop 1440x900, tablet 1024x768 touch, phone 390x844 dpr3 touch; light and dark
through the browser colour scheme, not a product switch.

| Scenario | Before | After | Recording |
| --- | --- | --- | --- |
| Direct `#settings-basic` | [PNG](evidence/settings-direct-basic-before.png) | [PNG](evidence/settings-direct-basic-after.png) | — |
| Direct `#settings-api-keys` | [PNG](evidence/settings-api-keys-before.png) | [PNG](evidence/settings-api-keys-after.png) | [MP4](evidence/settings-api-keys-entry-after.mp4) |
| Section switch with an unsaved draft | [PNG](evidence/settings-inpage-switch-before.png) | [PNG](evidence/settings-inpage-switch-after.png) | [MP4](evidence/settings-section-nav-after.mp4) |
| Traffic filters | [PNG](evidence/traffic-filters-before.png) | [PNG](evidence/traffic-filters-after.png) | [MP4](evidence/traffic-filter-cache-after.mp4) |
| Press inside the quota dialog | [PNG](evidence/traffic-dialog-drag-before.png) | [PNG](evidence/traffic-dialog-drag-after.png) | [MP4](evidence/traffic-dialog-interaction-after.mp4) |
| Topbar crumbs | [PNG](evidence/topbar-before.png) | [PNG](evidence/topbar-after.png) | [MP4](evidence/topbar-route-after.mp4) |
| Phone settings directory | [PNG](evidence/settings-mobile-light-before.png) | [PNG](evidence/settings-mobile-light-after.png) | [MP4](evidence/mobile-settings-nav-after.mp4) |

Every recording also exists as a `*-before.mp4` capture of the base commit with
the same steps. Theme and layout matrix without behaviour change:
[desktop light](evidence/traffic-desktop-light-after.png),
[desktop dark](evidence/traffic-desktop-dark-after.png),
[tablet dark](evidence/traffic-tablet-dark-after.png),
[phone](evidence/traffic-mobile-light-after.png),
[settings dark](evidence/settings-desktop-dark-after.png). No page reported
horizontal overflow and no page error was logged in either version.

## Multi-user and permission coverage

The directory only lists an entry whose section the page rendered, so the API Key
entry follows `can("admin")` and is absent for users. The traffic snapshot is
bound to the account and session object identity, which logout, account switch and
the access-change handler all replace, and the refresh channel refuses a response
read for another account; quota writes drop the snapshot before refreshing. The
crumb home target uses the same `overview.read` decision as the existing dashboard
redirect. Fixture runs cover the admin, read-only, write-only, shared-node and
empty scenarios through the mode matrix above.

## Limits

- The fixture API is in-memory; no database, production account or real node was
  involved. Request logs come from the page's own `fetch` wrapper.
- The fixture has no `GET /settings/api-keys` handler, so that section body shows
  its own read-error state in both versions; the navigation and selection
  assertions do not depend on it.
- Browser coverage is Chromium on Linux with viewport, touch and device-scale
  emulation. Physical Android/iOS devices, Safari/Firefox, screen readers, native
  browser zoom and OS font scaling were not exercised.
- The reading position rewrites the current history entry with `replaceState`
  (the pattern GitHub uses). Scrolling from one section to another therefore
  replaces the entry instead of adding one.
- Filter changes were verified as request-free by attribution over a 475 ms
  window and by comparing against the 6 s idle rate; the poll cadence itself is
  unchanged, so live metrics keep their 2 s latency.
