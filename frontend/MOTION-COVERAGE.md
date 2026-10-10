# Motion integration and playback coverage

Revision after merged PR #255 (`bdfa007b`). This inventory describes the current
implementation; the old "retained tiny fade / instant exit" decisions have been
replaced by coordinated visible motion. Aliases and redirects share their target
surface. Quiet polling and native field values keep immediate readable feedback.

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

Validation results are recorded here after the required command completes.
