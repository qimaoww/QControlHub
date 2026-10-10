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
| Dialog, disclosure and notice exit | `--motion-exit`, 120 ms | Non-interactive visual tail; state/focus commit immediately |
| Dialog, rail and card landing | `--motion-base`, 200 ms | One reveal or transform owner |
| Login, first workspace and route/view change | `--motion-slow`, 280 ms | One workspace opacity fade; no nested stagger |
| Pending request / running task | `--motion-loading`, 1000 ms | Only genuine pending work can loop |

Spatial easing is `--motion-ease-out`. Dialog displacement is 6 px, using
individual `translate` so existing positioning transforms remain intact.
Color transitions use `ease`; exits use `--motion-ease-in` and a 4 px dialog
offset. Closing, deletion, and operation submission commit immediately:
business work never waits for an animation. Native CSS discrete transitions
and inert retired surfaces provide visual exits without delaying focus or locks.

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
layout transition and does not change the workspace grid tracks. User-operated
in-flow disclosures now also interpolate their height for 200 ms, entirely in
CSS; they restore visible overflow at completion. Positioned menus fade only.

The follow-up [six-column coverage checklist](MOTION-COVERAGE.md) closes 82
page/component/scenario rows, including retained immediate/static responses
with their design reasons. It covers all 18 route entries and shared children.

## Popup inventory and lifecycle

| Popup family | Instances / sources | Motion and lifecycle |
| --- | --- | --- |
| Native detail and edit dialogs | Dashboard daily traffic; client parameter/display; traffic create/quota/status/sync; Sub-Store settings/targets/delete; user account/allocation/invitation/sharing; region picker; TCP editor/parameters; configuration inbound/outbound/common/history/diff/access | Shared 200 ms surface reveal and independent 160 ms native backdrop. Close and native focus restoration are immediate. Existing busy guards, unsaved-change confirmation, request invalidation and native focus trapping remain authoritative. Gesture-aware backdrop dismissal is shared by existing dismissible traffic/client/dashboard/config dialogs; padding and a gesture starting inside content cannot dismiss them. |
| Custom modal and command surfaces | Add node, enrollment records, deployment command, administrator node directory in `agent-enrollment` | Independent scrim pseudo-element keeps the original dim/blur without multiplying the content fade. One active surface per controller; focus enters immediately and remains trapped. Escape, actual outside gesture, route departure, logout, or external removal release background inertness, overflow, observers, listeners and owned timers. Stale create/command/directory responses cannot reopen a departed or closed popup. |
| Menus and calendars | Configuration inbound/outbound/tools menus; mobile More menu; dashboard month picker; IPQuality calendar | Only the revealed panel fades for 160 ms in / 120 ms out; the launcher stays still. Closing content is inert and aria-hidden immediately; reopening restores its previous attributes. Escape restores launcher focus; outside pointer/focus dismisses the popup. Configuration rebind retains its open state. Month selection returns focus to the summary before refresh. Menu positioning and calendar date rules remain unchanged. |
| Inline drawers and disclosures | Runtime/version management, config advanced/diff sections, task results, traffic accounting, IPQuality detail sections and enrollment history | One native details-content owner covers all children, with 160 ms enter, 120 ms fade out and bounded 200 ms height reversal. Initially open/restored sections stay still. Open state changes immediately; no JavaScript height measurement or end-event business dependency. |
| Confirmation and result feedback | Shared confirmation dialog, global success/error notice, login errors, deployment and settings status | Superseded/external confirmation closes settle their callers; identical notices do not replay. Results stay readable under reduced motion. Error messages retain their existing dismissal behavior. |
| Browser-owned popups | Native select options, date controls, password manager, and title hints | Preserve browser/platform presentation and accessibility. CSS styles the launcher and focus state; it does not attempt to animate system popup contents. |

`modules/popup.js` keeps shared document bindings bounded through `bindEvent`.
Native modal restoration after TCP reconciliation cancels the new CSS entrance,
so a background update cannot masquerade as another user opening. Native
dialogs remain native, including their top layer and focus behavior. Closing
surfaces commit immediately; any visual tail is inert and pointer-transparent.
Saves and navigation do not wait behind an animation event.

## Selection and filter inventory

Selections now use semantic `data-motion-region` / `data-motion-key` scopes.
The shell compares them after reconciliation and fades only the changed result
region from 0.8 to 1 opacity for 160 ms. Pending reads, cached previews, and
background enrichment do not consume/replay the completed selection. The
initial route owns its entrance; its descendants do not also fade. Unchanged
scope keys exclude metrics, response versions, and time-dependent row content.
The mounted main and context sidebar survive local filters; keyed launchers
retain focus. A replaced node/engine editor returns focus to the corresponding
launcher without transferring another editor's values or caret.

| Selection / filter | Feedback and lifecycle | Intentionally immediate / stable |
| --- | --- | --- |
| Node overview, node settings and preset node | One workspace entrance for a genuinely new node; mounted sidebar and matching launcher focus; existing node tabs fade only their revealed panel | Batch checkboxes and saved node order retain native selection semantics; metric updates remain quiet |
| Live configuration node, engine and source | Node/engine workspace entrance; source result fades after loading; superseded or route-stale confirmations cannot navigate; read failures are reported | Dirty/source draft confirmation, save locks and request sequencing remain authoritative |
| Preset protocol, inbound, common field and workbench | Embedded selected content fades once; workbench panels retain explicit reveal feedback | Captured per-selection drafts remain intact; same choice does not replay |
| Source file and merged preview | Brief editor-frame feedback only when the file changes; same-file click is ignored | Dirty file contents are saved in memory before switching; typing/validation does not animate |
| Client node, engine, submitted search and display format | One result-grid feedback; toolbar, search selection and empty-result region stay mounted | Native address/format choices, secret visibility, copy and export keep existing state/result semantics |
| Traffic node, engine, endpoint, status and reset | One stable result region for populated and empty filters; native select and sidebar focus stay mounted | Counters, progress, quota forms and polling stay quiet; filtered sorting still merges global order |
| Task node, status, action, limit and reset | Completed timeline feedback; applying-filter status and busy semantics; mobile Load more transfers focus to the revealed timeline | Polling, clocks, open outputs and automatic timeline updates stay quiet |
| Log node, engine, level, limit, reset and pagination | One completed stream feedback; loading/preview/cache phases do not replay; pagination/scroll anchors stay in place | Incremental search, log appends and automatic refresh remain immediate/readable |
| Connection node, engine, source IP, public/private, date, day/month, reset and pagination | One completed detail-region feedback; cached query/enrichment do not replay | Native query fields and background location enrichment remain stable |
| IPQuality node, latest/history date, day navigation and calendar month | Node/report feedback only when ready; open calendar grid fades for a month change | Polling, scheduling/running tasks and closed-calendar refresh remain quiet |
| TCP node / access-control node | One result-grid feedback for changed scope | Open parameter/rule dialogs and their drafts keep existing busy/dirty guards |
| Sub-Store node, synchronization group and search | One grid feedback for node/group; busy pending group and recoverable failure; keyed cards/targets | Incremental search retains mid-text caret/selection and does not pulse; queued selection writes stay ordered |
| User selection (sidebar/mobile), sharing and settings | Selected allocation feedback; mounted selector value/focus; release old busy snapshots before computing new button availability | Allocation drafts, invitation status, consent, permissions, native form toggles and conditional protocol fields stay immediate |
| Dashboard traffic month and year | Changed chart fades; month picker stays mounted; pending status; abort/ignore older month responses and restore the displayed month on failure | Year browsing updates native choices; metric/number/chart refresh does not replay |

Incremental search, native select/checkbox/radio indicators, form field
visibility, password visibility, and revision/output reads deliberately avoid
extra content fades: moving or dimming them would interfere with reading,
composition or cursor placement. Existing count, selected, busy and result
labels remain the primary feedback. No control changes a business result by
waiting for an animation event.

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

The four additional `motion-popup`, `motion-popup-reduced`,
`motion-popup-mobile`, and `motion-popup-mobile-reduced` modes cover custom
scrim/content ownership, repeated native/custom open and close, real focus
events, launcher restoration, menu rebind, calendars, content-to-backdrop
gestures, duplicate enrollment submission, pending responses across same-route
refresh, late/superseded enrollment/directory responses, route departure and
external removal. Headless CDP focus emulation is enabled for motion modes so focus
events are exercised as they are in an active browser window.

The four `motion-selection`, `motion-selection-reduced`,
`motion-selection-mobile`, and `motion-selection-mobile-reduced` modes exercise
real production bindings and shell reconciliation across traffic, clients,
logs, tasks, connections, IPQuality, Sub-Store, users and dashboard. They cover
exactly one completed selection fade, same-choice/refresh stability, repeated
filters, empty results, retained sidebar/selector focus, mid-text selection,
latest-month wins even if an aborted request returns, recoverable month failures,
quiet quota refresh, and stale configuration
confirmations after superseding/departure. Existing preset/configuration, TCP,
IPQuality, user and connection suites validate their richer business flows.

### Validation record (2026-10-10)

Started from clean worktrees on latest `main` (`a035b2b9`) and created
`codex/console-motion`. The final `make check` passed with an isolated
PostgreSQL 17 test database: module smoke, all 70 Chromium modes, source/style
and repository policy checks, installer/quick-start checks, vet, and all Go
packages. The Go frontend package also reran the browser suite successfully
(409.387 s in this follow-up). The frozen initial stylesheet hash contract and `git diff --check`
passed. No required check was blocked by the environment.

The final exit hardening was additionally verified with the eight motion and
popup modes: an inert retired surface holds zero opacity if its finish event
is lost, then the bounded fallback removes it. Module smoke and generated-style
checks were repeated on that final snapshot. The route sweep in the popup modes
visits all 18 entries in both themes (144 visits across four modes).

The merge review found a native-dialog exit assertion failing in both Debian
and Alpine CI: a fixed 220 ms timer could expire before the browser completed
its rendering frames. The regression now waits for the actual hidden,
animation-free paint state with a bounded four-second timeout. Immediate close,
focus, inertness and click-through assertions are retained. All four popup
modes passed locally after this test-only correction.

Manual Chromium inspection covered desktop drag/navigation and both themes,
plus dark reduced-motion phone portrait (375 x 812) and landscape (844 x 390).
Both phone layouts had no horizontal overflow and no active animations.
An 18-second drag/navigation recording is retained locally as
`output/playwright/console-motion-review.mp4`; the preview below reproduces
the interactions without that local artifact.
The popup follow-up also passed focused native/custom popup, enrollment,
configuration, and TCP regressions. Its real browser inspection includes dark
reduced-motion custom dialogs at 390 x 844 and 844 x 390, with no horizontal
overflow or active animations. The 10-second popup recording is retained as
`output/playwright/popup-motion-review.mp4`.
The selection/filter follow-up adds a 16-second browser recording at
`output/playwright/selection-motion-review.mp4` and verifies dark reduced-motion
390 x 844 portrait and 844 x 390 landscape without overflow or active animation.
Focused selection, quota, configuration, popup and existing business suites
passed; the final full check includes all 70 browser modes twice (directly and
from the Go frontend suite).

## Reproducible visual preview

No production API, account, or credentials are needed:

```sh
QCH_BROWSER_SMOKE_SERVE_ONLY=1 node frontend/agents_browser_smoke.mjs
```

Use the printed loopback origin and open
`/agents-browser-smoke.html?mode=motion&preview=1#node-settings`.
For the popup review, use
`/agents-browser-smoke.html?mode=motion-popup&preview=1#node-settings`:
it starts with Add node open. Press Escape, reopen it, generate a fixture
command, close it, and navigate to traffic to open quota/status dialogs.
The popup recording is `output/playwright/popup-motion-review.mp4` (10 s).
For selection/filter review, use
`/agents-browser-smoke.html?mode=motion-selection&preview=1#node-settings`.
Navigate to traffic, click several sidebar nodes, change engine/port/status,
select a status with no results, and clear filters. Open logs, select
engine/level/node, search with a caret in the middle, and paginate. Repeat
clients, connections, IPQuality, synchronization groups, users and dashboard
month selection. The 16-second recording is
`output/playwright/selection-motion-review.mp4`.
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
   Open Add node and close/reopen it during the short exit. Open a node detail,
   press Version, use its Collapse summary, then press Version again before the
   drawer finishes closing. Focus should return to Version and no closing
   content should accept clicks or keyboard focus. The follow-up 7.8-second
   recording is `output/playwright/surface-motion-review.mp4`.
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
