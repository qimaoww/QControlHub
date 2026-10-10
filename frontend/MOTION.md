# Console motion audit and preview

## Motion contract

The console keeps its existing theme, layout, native modules, and workflows.
Style sources live in `frontend/styles`; run `make generate-styles` after edits.
Do not edit the generated `frontend/app.css` directly. The pre-deployment-dialog
stylesheet baseline is frozen by `TestInitialConsoleStylesRemainExactOutsideApprovedExtensions`;
its original declarations remain intact. The approved 420/440 motion layer
overrides their legacy timings and focus treatments.

| Interaction | Token | Behavior |
| --- | --- | --- |
| Color, border, hover and inserted card | `--motion-fast`, 120 ms | Stable bounds; inserted branch fades once |
| Result and disclosure | `--motion-feedback`, 160 ms | Brief opacity feedback on an actual change |
| Dialog, rail and card landing | `--motion-base`, 200 ms | One reveal or transform owner |
| Login, first workspace and route/view change | `--motion-slow`, 280 ms | One workspace opacity fade; no nested stagger |
| Pending request / running task | `--motion-loading`, 1000 ms | Only genuine pending work can loop |

Spatial easing is `--motion-ease-out`. Dialog displacement is 6 px, using
individual `translate` so existing positioning transforms remain intact.
Color transitions use `ease`. Closing, deletion, and operation submission are
immediate: business work never waits for an animation.

`modules/motion.js` reads the CSS tokens for native browser animations. Each
animation releases its timer, callbacks, media listener, and removal observer on
finish, cancellation, removal, preference change, or a duration + 80 ms fallback.
The observer and preference listener are shared and exist only while motion is
active. Browsers without `Element.animate` show the final DOM immediately.
`prefers-reduced-motion` disables CSS animations/transitions, including delays,
and skips/cancels JavaScript animations. Text, status colors, busy semantics,
progress bars, and result links remain visible.

## Coverage and findings

The audit scanned every style source and all modules for animation, transition,
transform, timers, frame callbacks, navigation, reconciliation, pointer events,
and feedback. The principal problems were competing parent/child entrances,
filled animations overriding hover transforms, selected controls replaying,
unnecessary metric/online pulses, input focus movement, event-dependent landing
cleanup, and a traffic drag lifecycle that survived route departure.

| Area / source | Changes | Preserved behavior and reason |
| --- | --- | --- |
| Shell / `shell-view`, `app`, 420, 700 | One route fade; token rail timing; compositor divider; cancel old motion; guarded anchor callback | Mounted dock and links survive navigation, retaining hover and keyboard focus. Width still overlays the context column without moving workspace grid tracks. |
| Mobile More navigation | Return keyboard focus to summary when its navigation closes the menu | Existing native details menu and bottom dock keep their layout and touch behavior. |
| Login / `login-page` | One initial fade; busy button; inline failure retains credentials/input DOM | Authentication, password manager support, and theme toggle are unchanged. |
| Dashboard / `dashboard-*`, `panel-metrics`, 640 | Shared route/control contract; metric width uses common duration | Metric patching, calendar selection, aggregation, and charts stay stable. No chart/number tween or automatic refresh entrance. |
| Node overview and settings / `agent-*`, 280, 330 | No card lift or metric entrance replay; explicit tab reveal; pointer/keyboard sorting; inert ghost; capture and copy-timer cleanup | Interaction gate still coalesces metric/roster updates. Native tab semantics, node operations, permissions, and saved order remain unchanged. |
| Configuration, presets, archive / `live-config-*`, `preset-*`, `config-*`, 420, 520, 530 | No phase/selected-option replay; explicit workbench tab reveal; changed deployment status fades once | Editor drafts, source baselines, cursor/selection, validation, deployment preflight, and recovery rules remain unchanged. Source reads and task polling never animate the editor. |
| Clients / `client-access-*`, `client-connections-*`, 515 | Shared control feedback; batched masonry measurements and canceled frames/observers on departure | Export/copy dialogs, access order, filters, cached connection queries, pagination and table layout remain unchanged. No per-row refresh animation. |
| Sub-Store / `substore-*`, 670 | Same masonry lifecycle and card/control contract | Selection persistence, sync targets, and business requests are unchanged. |
| Tasks / `task-*`, 090 | Silent timeline insertion/patching; opacity-only running feedback; explicit result disclosure | Scroll anchoring, clocks, open results, polling, retry/cancel and pagination remain unchanged. No animated row height or delayed removal. |
| Logs / `core-log-*`, 270, 430 | Removed log-row entrance from reconciliation; static automatic-update indicator | Staged loads, local search/pagination, append reconciliation, preferences and refresh scroll positions stay intact. |
| Traffic / `traffic-*`, 320 | Stable cards; native FLIP; keyboard order alternative; route/logout drag cleanup | Filtered order merge, quota forms, counters and sync preview stay intact. Pointer ghost follows directly even with reduced motion; landing snaps. |
| IPQuality / `ip-quality-*`, 650 | Shared route/dialog/control behavior with full reduced-motion coverage | Reports, SVG/archive previews, calendars, task controller and one-node selection remain unchanged. Animating reports would obscure readable results. |
| Users, sharing and quota / `user-*`, `agent-sharing`, 580-630 | Shared dialog/reveal/control behavior; stable form hit areas | Drafts, allocation revisions, invitations, permissions, and all submission guards remain unchanged. |
| TCP and access control / `system-bbr-*`, `access-control-*`, 400, 500 | Shared dialogs, stable controls and card feedback | Native parameter editor, task feedback, rules, confirmation and server validation remain unchanged. |
| System settings / `settings-*` | Busy save semantics, duplicate-submit guard, retained edits made during save, result feedback | Existing settings schema and update workflow remain unchanged. A later edit stays dirty after the earlier snapshot saves. |
| Common controls / 440, 570, 690 | Shared tokens; no input focus displacement; visible keyboard rings; no selected-state scale or decorative online pulse | Color, border, focus and native checked/expanded/selected states continue to communicate interaction without motion. |
| Native and custom dialogs / 230, 440, `shell-feedback` | One token reveal/backdrop; no filled transform; repeated/superseded/external confirmation close settles its caller | Open/close remain immediate native operations. Existing custom command scrim and dialog focus behavior remain unchanged. |

Intentional stability: list removals commit immediately; log/task/background
refreshes are not choreographed. Animate an actual inserted card branch, not its
children. Disclosure and tab reveals are requested by interaction handlers, so
an initially open/restored panel does not animate inside a route entrance.
Cards use color/elevation on hover; pointer ghosts and FLIP exclusively own
card transforms. Metric/progress width remains a small, bounded layout
transition; it is disabled for reduced motion. Rail width is the other retained
layout transition and does not change the workspace grid tracks.

## Regression checks

Run `make check`. It includes all module smoke suites, all existing Chromium
browser modes, stylesheet generation checks, architecture/PR/schema/deployment
policy checks, installer/quick-start checks, vet, and Go tests.

The added `smoke/motion.mjs` covers superseding/canceling animations, removed
nodes, live preference changes, lost finish events, token timing, and bounded
listener/observer/timer ownership. Drag smoke verifies settlement and interaction
coalescing without temporary styles or forced per-card layout.

New browser modes:

```sh
QCH_BROWSER_SMOKE_MODES=motion,motion-reduced,motion-mobile,motion-mobile-reduced \
  node frontend/agents_browser_smoke.mjs
```

These drive real Chromium mouse/touch input and pointer capture for drop,
Escape cancellation, and route departure. They also check rapid keyboard
sorting, dock focus/DOM preservation, native dialog reopen/external close,
settings drafts/cursor/scroll on same-page refresh, changes during save,
duplicate submission, editor selection/open details during reconciliation,
quiet log rendering, removed animations, both themes, reduced motion, and
login failure input retention. Existing browser modes cover the production
page workflows listed above using deterministic local API fixtures.

### Validation record (2026-10-10)

Started from clean worktrees on latest `main` (`a035b2b9`) and created
`codex/console-motion`. The final `make check` passed with an isolated
PostgreSQL 17 test database: module smoke, all 62 Chromium modes, source/style
and repository policy checks, installer/quick-start checks, vet, and all Go
packages. The Go frontend package also reran the browser suite successfully
(289.708 s). The frozen initial stylesheet hash contract and `git diff --check`
passed. No required check was blocked by the environment.

Manual Chromium inspection covered desktop drag/navigation and both themes,
plus dark reduced-motion phone portrait (375 x 812) and landscape (844 x 390).
Both phone layouts had no horizontal overflow and no active animations.
An 18-second drag/navigation recording is retained locally as
`output/playwright/console-motion-review.mp4`; the preview below reproduces
the interactions without that local artifact.

## Reproducible visual preview

No production API, account, or credentials are needed:

```sh
QCH_BROWSER_SMOKE_SERVE_ONLY=1 node frontend/agents_browser_smoke.mjs
```

Use the printed loopback origin and open
`/agents-browser-smoke.html?mode=motion&preview=1#node-settings`.
The preview stays open on the node overview. For the richer existing page
fixtures, substitute `mode=dashboard`, `mode=config-layout`, `mode=users`,
`mode=presets`, `mode=ip-quality`, or `mode=connections` with `preview=1`.
The config-layout fixture runs its layout checks before retaining the final
screen for inspection.

1. Hover the desktop rail; move away; Tab into it. Navigate to traffic and
   settings while it is expanded. Check that the workspace does not move and
   keyboard focus stays on the clicked dock link.
2. Drag node/traffic grips across rows, then start another drag immediately.
   Press Escape during a drag. Leave the route while holding a card. Check for
   a remaining ghost, highlighted target, stuck cursor, or delayed order commit.
3. Focus a grip and use arrow keys quickly. Check final order, focus ring, and
   the screen-reader position announcement. On mobile, repeat with touch.
4. Open/close quota and confirmation dialogs rapidly; press Escape. Check the
   backdrop, focus restoration, and readable final content.
5. Edit settings, select part of the text, and dispatch a same-route refresh
   (`window.dispatchEvent(new HashChangeEvent("hashchange"))` in DevTools).
   Check input, selection, focus and scroll. Watch background node/log refresh
   without touching controls; there should be no entrance replay.
6. Toggle light/dark theme. Inspect at 1280 x 900, narrow mouse width 820,
   phone widths 375/390, and landscape. Emulate reduced motion in DevTools
   Rendering before reload and while a dialog/landing is active. The final
   layout and busy/result feedback should be available immediately.

Local recordings/screenshots are written to ignored `output/playwright/`.
Browser validation is Chromium on Linux; Firefox/WebKit and physical mobile
hardware are not part of this environment. The fixtures validate UI behavior,
not actual deployment to remote agents. Roll back by reverting the motion PR
and regenerating styles; there are no database/API/schema migrations.
