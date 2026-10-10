# Motion integration and playback coverage

Revision after merged PR #255 (`bdfa007b`). This inventory describes the current
implementation; the old "retained tiny fade / instant exit" decisions have been
replaced by coordinated visible motion. Aliases and redirects share their target
surface. Quiet polling and native field values keep immediate readable feedback. The
concrete trigger audit below includes conditional fields, title/validity prompts,
copy results and destructively closed dialogs, as requested in the follow-up.

## Page inventory

Every displayed page uses the shared canvas entrance and non-nested visible
surface sequence. All 18 route entries are visited in both themes in each of the
four popup modes (desktop/mobile, normal/reduced): **144 visits**. Normal-mode
visits sample live rendering frames to verify initial fade, ≥10 px displacement,
multiple playback frames and a fully restored endpoint. Alias visits preserve a
mounted target when no actual page change occurs.

| Route | Main surfaces / specific interactions | Exit and continuity | Verification |
| --- | --- | --- | --- |
| Login / boot | Login shell, loading orbit, busy submit, inline failure | Credentials/focus retained on rejection; static reduced-motion loading | M, C |
| `dashboard` | Stats, panels, calendar menu, traffic history | Native calendar exit/reversal; completed month result; quiet live metrics | P, S, dashboard modes |
| `ip-quality` | Node/report panels, date/calendar selectors, report dialogs | Menu/dialog exits; completed report/date result; readable status | P, S, ip-quality modes |
| `agents` | Redirect to live configuration | Target entrance only; no duplicate redirect sequence | P |
| `node-settings` | Node cards, detail service panels, monitor/Agent tabs, version/runtime drawers, enrollment/command/directory dialogs | Painted-position drag/reorder; card insert/delete; overlapping inert tab exit; native drawer and custom modal tails | M, P, enrollment, batch, shared-node modes |
| `client-access` | Profile cards, engine/node/search/format filters, copy/parameter dialogs | Card membership FLIP/retirement, completed result feedback, native dialog exit | P, S, client modes |
| `substore-sync` | Group/node cards, scope/search, target dialogs, sync feedback | Native dialogs, changed result, insertion/deletion/reflow; persistent selections | P, S, substore modes |
| `agent-config` | Redirect to live configuration | One target entrance; no redirect replay | P |
| `live-config` | Command bar, editor/workspace surfaces, node/engine/source/file selectors, tools, in/outbound dialogs, advanced/history/diff drawers | Native menu/dialog/disclosure exits; editor draft/caret/scroll preserved; completed results only | P, S, config modes |
| `archive-config` | Template cards, source/editor, revision selection, drawers | Card/selection feedback; native dialog/disclosure exits; dirty-state guards | P, S, presets |
| `tasks` | Timeline panels, filters, results, status badges, retry/cancel/load-more | Native disclosure exit; state change badge; output/clock polling stays quiet; anchors preserved | M, P, S |
| `core-logs` | Filter controls, paginated stream, node/engine/level/page selections | One completed result transition; incremental input remains readable; no row replay on polling | M, P, S, logs |
| `client-connections` | Query controls, sources disclosure, table panel, scope/date/pagination | Slow **first** result entrance fixed; native disclosure exit; cache/location enrichment stays quiet | P, S, connections |
| `traffic` | Policy cards, scope/status filters, quota/create/sync dialogs | Card addition/deletion/reflow; mouse/touch/keyboard reorder; native modal exits and reversal | M, P, S, traffic modes |
| `access-control` | Rule cards, node selection, editor dialog | Changed selection, card mutation, native dialog entrance/exit | P, S, access module tests |
| `system-bbr` | TCP cards, node/profile selection, edit panel/dialog, status | Changed result, reversible dialog, immediate focus/draft ownership | P, S, bbr modes |
| `settings` | Form/panel surfaces, tabs, check/switch shells, save state | Busy→saved/error feedback; same-page refresh retains fields/caret/scroll | M, P, S |
| `users` | User selector, account/allocation cards, invite/share dialogs | Completed user result, card mutation, native dialog exit, unsaved inputs retained | P, S, users/sharing modes |
| `my-quota` | Quota/share cards, node/quota dialogs, allocation status | Completed selection, native dialog exit/reversal; static permission/unsupported state | P, S, shared-node modes |

## Shared component inventory

| Component / trigger | Current motion | Continuity / static feedback | Evidence |
| --- | --- | --- | --- |
| Route departure / completion | 220 ms outgoing isolated paint tail; 480 ms canvas + sibling sequence; context reveal | Latest response owns the page; mounted dock and launchers survive | M, P playback samples, paired clip |
| First loading→ready result | Ready key commits at completion; 280 ms visible result transition | Loading skeleton does not consume entrance; subsequent same-data polling is silent | S delayed-first regression, 900 ms browser reproduction, paired clip |
| Sidebar width / labels | 480 ms width, 280/320 ms labels with short coordinated delay | Native transitions reverse from current width; content grid stays fixed | M, shell modes, paired clip |
| Sidebar selected route | Moving gradient/elevation highlight, 320 ms | Native animation retargets current paint; CSSOM respects CSP | M, dashboard/ip-quality CSP modes, paired clip |
| Mobile More menu | Launcher-relative scale/translate/fade | Native details tail; Escape restores focus; pointer/touch/keyboard outside close | P, mobile dark clip |
| Context links / tabs / selected filters | Shared color, border, inset glow and underline transitions | Persistent `aria-current`, selected/pressed/expanded semantics | S, shell modes |
| Native dialog and scrim | 24 px / `.94` entry, fade scrim, coordinated content; 220 ms exit | Native top-layer retention; clearing allocation forms use an isolated complete tail; close/focus commit immediately; footer container stays fixed | P native exit/reopen assertions |
| Custom dialog and scrim | Same entry tokens and parallel scrim; isolated `.97` exit | Locks/timers/listeners release immediately; retired content cannot match queries | P, paired clip |
| Inline disclosures / version drawer | Opacity/displacement and native height interpolation | Reversible; complete close; final overflow visible; no initially-open replay | P dynamic multi-child and version tests |
| Node content tabs | 280 ms incoming; 220 ms outgoing paint outside layout flow | Hidden panel immediately inert/aria-hidden; draft and focus ownership survive refresh/reversal | P tab regression, mobile dark clip |
| Card insertion / deletion / reflow | 14 px / `.985` insertion; isolated 220 ms exit; 420 ms survivor FLIP | Geometry read only for changed membership/order; component queries ignore retired clones | S filters/empty results, M, module reconciliation |
| Mouse/touch drag / keyboard ordering | Direct pointer ghost; visible target elevation; 420 ms landing | Reverse keys capture painted rectangles before releasing prior landing; order/focus preserved | M four drag modes, paired clip |
| Buttons / controls | Hover elevation + 2 px lift; press 1 px / `.96`; color/shadow transitions | Stable layout/hit area; disabled semantics and busy sheen; keyboard focus ring | M, P, S |
| Native check / switch / select / input | Immediate native state plus themed shell/color/glow transitions | Value and focus never delayed; native check semantics preserved | M, S, component modes |
| Loading / progress | Actual-pending orbit/progress/sheens; smooth progress width | Labels stay visible; no idle loops; reduced mode keeps values/status static | M, component modes |
| Success / failure / task state | Changed result/status entry, themed state transition, notice exit | Identical messages/output/ticks never replay; fields remain editable with current drafts | M task/save/login, S, P inline feedback |
| Polling / enrichment / timers | Quiet DOM reconciliation | Scroll, editor caret/selection, forms, open results and focus preserved | M, S, logs pressure suite |
| Rapid reversal / cancellation / departure | Current-value retargeting; native CSS reversal; pending route resumes original paint | List reversal releases old owners before target reads; bounded cleanup on readiness + duration + delay; hidden document, removal and preference cancel ownership | M frame continuity/slow-route reversal, P, module ownership suite |
| Reduced motion | No CSS/WAAPI motion, stagger, press geometry or retained tail | Full selected colors, labels, status, focus, busy/disabled semantics and progress values | M/P/S reduced modes; route matrix |

## Concrete interaction audit

Every trigger below uses the shared control feedback and its page/result motion;
conditional content also uses complete presence. Initial nested fields are owned
by their entering page/dialog. They do not start a competing second animation.

| Scope / source modules | Concrete triggers and messages connected |
| --- | --- |
| Shell, login, appearance, feedback | All dock/context navigation and aliases; sidebar hover/focus/reversal and selected marker; mobile More; theme toggle; busy login, rejected credentials; confirmation replacement/accept/cancel/Escape; notice success/error/close/timeout; every dynamically mounted native title hint (pointer/focus/blur/Escape) and constraint-validation message (invalid/corrected input) |
| Dashboard and IP quality | Month/year/date/calendar menus and close; completed historical result, empty/failure/unavailable states; node/report selection, refresh/detection pending/result and report dialogs |
| Nodes: workspace, addresses, Komari, core actions | Card/detail selection, Agent/core/monitor tabs; selected/deployed/permission states; IPv4/IPv6/address visibility, connection notes and address-copy results; monitoring URL/secret fields; stable/development/custom version sources; version drawers, installation/upgrade/start/stop/restart/uninstall/deploy confirmations and task results |
| Nodes: enrollment, batch, regions, sharing | Enrollment/open/close/copy; directory/command panels and deleted entries; batch eligibility/count, engine/version/custom fields, submitting/retry/error/results and dismissed rows; region hint, modal, search/count/empty/ready, selection, save/error and destroyed close; recipient add/remove/reinvite, sharing save/reload/error and destroyed close |
| Configuration: preset bindings/server-plan form | Builder tabs; protocol/transport/TLS/Reality/authentication/Mieru conditional fields; generated credentials/keys/certificates, show/hide/copy/regeneration pending/result/error; advanced options, dirty/reset/discard guards; saved, preflight, validate/deploy/install progress/failure/success and operation links/retry |
| Configuration: live navigation/submit, editor | Node/engine/source/file selection, switching placeholder and removal, first completed/empty result; tools menus; source/editor entry; dirty/valid/invalid/oversize/format feedback and reset; save/import/validate/deploy busy and all results; latest-response and unsaved-input/caret/scroll continuity |
| Configuration: inbounds/outbounds/restrictions/archive | Menu action availability and common actions; add/modify/delete/history/diff panels and complete destroyed exits; saved/live deployment differences and preflight failures; bound/unbound/manual/JSON/node modes, protocol-specific fields, peer loading/selection/stale/error; restriction expansion; archive source/file/revision selection and editor feedback |
| Clients and Sub-Store | Node/engine/search/profile/format/group/scope selection and completed/empty results; parameter and secret display/copy; publish/busy/result; target creation/edit/delete and native close; remote association/auth/group fields, validation/loading/failure; synchronization status, task result and retry |
| Traffic | Scope/engine/status filters; policy add/delete/FLIP and mouse/touch/keyboard order; quota/status/create/edit dialogs; shared allocation attribution; synchronization candidate list/loading/empty/error, selection count, submit/result/retry and complete destroyed close |
| TCP / access controls | TCP node/profile selection, editor opening/native reversal, preset/reset/confirmation, conditional drafts/count, validation error/correction and configuration/enable/disable task states; rule cards/selection/editor, checked/unchecked/dirty/clean/preflight/saving/failure/success and destroyed rule close |
| Users / shared quota | User/account selection and cards; account add/edit/save/error, role permissions/default hint reveal/hide; allocation add/edit/remove, node/engine/quota fields and consent messages; failure/reload/unsaved guards; invite/reinvite/accept/reject/leave, loading/result/error and full destroyed invitation/allocation close |
| Settings | Every switch/check/input/select/tab with themed focus/selected feedback; dirty/saving/saved/concurrent-dirty/failed states; version-check loading/current/new/uncomparable/failure and release link; font/theme changes and persistent form focus/caret |
| Tasks, logs, connections | Task filters/load-more/retry/cancel, added/removed/reordered cards, changed status, result disclosure; log node/engine/level/date/page/search, first/full result, refresh/error/recovery; connection node/engine/source/scope/date/page/search and source disclosures, loading/empty/failure/ready; identical output/counters/enrichment do not replay |

### Continuity checks added for this audit

Four desktop/mobile and normal/reduced popup modes exercise live rendering of:
keyboard tooltip entry/exit/refocus using the same element; updated hint metadata
through refresh; native validity rejection, correction and message persistence
through polling; immediate field reversal without an opacity jump; copy-message
clear with its full outgoing paint; and destruction of a native modal in mid-entry
with bounded complete panel/scrim retirement. Live frames compare the retired
heading/footer opacity to their interrupted source paint and require the modal
to keep fading/shrinking without an enlarged or bright frame. These join the route/list/drag/tab/
disclosure and delayed-first-result regressions. Reduced mode has no retained
paint or motion, while preserving the same messages, constraints and focus.

## Evidence key

- **M**: `motion`, `motion-mobile`, `motion-reduced`, `motion-mobile-reduced`.
  Native mouse/touch gestures, rapid keyboard reordering, mounted dock identity,
  editable inputs/caret/scroll, saves during edits, confirmations, polling and login.
- **P**: four `motion-popup*` modes. Full route playback, native/custom modal
  enter/exit/reversal, disclosures/tabs, accessibility/click-through, missing finish
  events, external removal, cancellation and both themes.
- **S**: four `motion-selection*` modes. Initial slow result, latest response
  ownership, filters, empty results, source/file/scope/date/month/user/page changes,
  stale confirmations, persistent launcher focus, incremental search and caret.
- **C**: generation, frozen stylesheet hash, style contracts, module smoke and
  architecture/policy checks. Component fixture modes validate real workflows.

## Measured browser findings

- Original default mode: stylesheet loaded; reduced motion false; first route
  frame opacity `.55`, translate/scale `none`, duration 280 ms.
- Original first connection result with a 900 ms response: opacity `1`, no result
  animation. This is the reproduced readiness/key bug, not a stylesheet guess.
- New route live frame sample: starts at opacity `0`, translate 28 px, scale `.975`;
  approximately 69 ms into playback, the first card is at 13.4 px while later
  siblings remain at 22.5/28 px; the last layer settles around 650 ms in that view.
- New first connection result with the same 900 ms delay: opacity `.25`,
  active `qch-selection` animation at completion; the result then reaches opacity 1.
- Paired actual browser recordings: identical sequence, 1440 × 960, 60 fps,
  original playback speed. [Before](../docs/motion/255-before.mp4),
  [after](../docs/motion/255-after.mp4),
  [mobile/dark/touch](../docs/motion/255-mobile-dark.mp4).
- The capture uses deterministic API fixtures and actual browser input. It does
  not test remote agents or production latency beyond the injected slow response.
  Physical mobile devices and Firefox/WebKit were not exercised.

## Commands and final results

- `make generate-styles`: passed; generated stylesheet matches the ordered manifest and frozen initial source remains unchanged.
- `QCH_TEST_DATABASE_URL=postgresql://postgres@127.0.0.1:56481/qch_motion?sslmode=disable make check`: **passed, exit 0**, using a task-owned PostgreSQL 17 instance. All 70 Chromium modes, module/policy/style contracts, installer/redeployment/quick-start checks, vet and all Go packages passed. The Go frontend run repeated the browser suite and completed in **420.468 s**.
- Final supplemental four-mode feedback/exit suite, module smoke and 158-module boundary check: **passed**. It includes complete painted-state sampling of a destroyed modal, field/prompt reversal, tooltip metadata refresh and native validation persistence. A real pointer-hover probe retained its `0px -2px` geometry across 18 rendering frames and completion.
- Direct log pressure suite: 8,000 loaded records, 200 DOM rows, 69 ms switch acknowledgement and 81 ms cached switch; the 500 ms budget is unchanged.
- Actual recording metadata: before 22.97 s, after 22.95 s, 1440 x 960 / 60 fps; mobile dark/touch 10.77 s, 390 x 844 / 60 fps. All are H.264 at original playback speed.
- Full required-command output and final supplemental evidence: [255-validation.txt](../docs/motion/255-validation.txt).

During this audit, a conditional-field FLIP inside a scaling parent produced horizontal overflow. Parent entrance now owns the nested reveal. The first remote CI run also exposed an early test completion: document animation enumeration cannot see a closed-shadow retirement. The exit assertion now waits for the actual retired surface to disconnect, keeping the cleanup deadline and all interaction budgets unchanged.

The pre-merge review reproduced another exit discontinuity: native clones reset
select choices and nested scroll offsets. Shared paint snapshots now retain
single/multiple choices and restore content/editor scroll before their first
paint. Four popup modes verify the copied draft and both scroll axes. The task
polling check also waits for paused deferred entrances, so an initial disclosure
cannot be mistaken for a polling replay. Five actual result refreshes retained
one initial disclosure owner and created no new entrance. CI's aggregate Go
package deadline is 20 minutes because it repeats all 70 browser modes beside
the standalone suite; per-mode deadlines and interaction budgets remain unchanged.

The review also corrected task membership coverage to use the outer event row
for manual refresh and background polling. Four motion modes now verify a new
task entrance, order changes and a complete inert removal tail. A layout reflow
releases only its own element's landing, so hiding a neighboring auxiliary button
cannot cancel a nested task insertion or feedback animation. Both four-mode
motion/popup suites and module/ownership checks passed with these corrections.
