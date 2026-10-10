# Motion integration coverage

This is the follow-up audit of PR #255, based on `a9ef2a44`. The six columns
record the page, component, trigger, observed baseline, decision and evidence.
Every row is closed: **fixed** describes this follow-up; **retained** gives the
reason for keeping an existing response. The inventory includes the 18 route
entries in `modules/routes.js`; `agents` and `agent-config` redirect to live
configuration. Alias routes keep the same component behavior.

## Verification key

- `M`: `motion`, `motion-reduced`, `motion-mobile`, `motion-mobile-reduced`:
  real mouse/touch dragging, keyboard ordering, navigation, focus, drafts,
  polling, saving, confirmation and login failures.
- `P`: the four `motion-popup*` modes, including `browser/surface-motion.mjs`:
  native/custom close and reversal, dynamic disclosures, inline feedback,
  accessibility retirement, missing finish events, removal and cancellation.
  The route sweep visits all 18 entries in both themes in each mode (144 visits).
- `S`: the four `motion-selection*` modes: completed-result feedback, latest
  response ownership, filtering/search/empty results, selector focus and caret.
- Named modes below are existing component/business/browser regressions.
- `C`: stylesheet generation/contract, module smoke and architecture checks.

The full `make check` runs all 70 Chromium modes and the Go suite. A named
component mode validates that component's actual operations; M/P/S validate
the shared motion behavior it uses. The route sweep checks mounting, initial
readability, mounted dock identity, overflow and reduced-motion final state.
It does not substitute for richer component fixtures or remote-agent tests.

## Coverage checklist

| Page | Component | Trigger | Current behavior at audit | Resolution | Verification |
| --- | --- | --- | --- | --- | --- |
| Login / startup | Boot indicator and login shell | Initial load / authentication pending | One route-level reveal; actual pending spinner | Retained: one 280 ms shell reveal, readable pending label; no decorative loop | M, C |
| Login | Credentials, theme, submit, error | Focus / submit / rejection / retry | Busy submit; inline failure keeps inputs | Retained: instant busy/disabled semantics, 160 ms changed error; retain password-manager DOM | M |
| All pages | Workspace navigation | Route change / rapid route departure | One opacity owner; mounted dock | Retained: immediate route commit and 280 ms incoming fade; no outgoing old workspace containing editable controls | M, P, shell-layout* |
| All pages | Pending route line | Request starts / succeeds / fails | Busy line and inert main release | Retained: only pending work loops; release state independently of animation events | M, P |
| Shell | Desktop rail, labels and divider | Hover / keyboard focus / leave / reverse | Token width, label and divider transitions | Retained: 200 ms overlay width, 120 ms labels, bounded 80 ms delay; workspace grid stays fixed | M, shell-layout* |
| Shell | Selected navigation and context links | Select node / engine / user / route | Persistent color, border and aria state | Retained: 120 ms colors; links and focus survive same-route reconciliation | M, S, P |
| Shell | Mobile More menu | Enter / close / Escape / navigation / reopen | Enter-only content fade; close was immediate | Fixed: native 160 ms reveal / 120 ms exit; launcher stays opaque; focus returns immediately | P, M |
| Shared | Buttons and icon actions | Hover / press / focus / disabled / busy | Stable hit area, press translate and focus rings; opacity rhythm incomplete | Fixed: token opacity for disabling; retained 1 px individual press translate and visible focus | M, P, C |
| Shared | Native select, checkbox and radio | Choose / toggle / disable | Browser checked indicator is immediate | Retained: immediate checked/value state is clearer for forms; animate surrounding color/opacity only | P, capability-settings*, users* |
| Shared | Custom capability switch | Toggle / failure / disable / rapid reversal | Thumb and background use tokens; opacity omitted | Fixed: disabled opacity joins 120 ms background transition; thumb has its own transform | capability-settings*, C |
| Shared | Labels around checks | Batch / release channel / shared engines | Some shells lacked matching color transitions | Fixed: common 120 ms background, border, shadow and opacity; native checks stay immediate | batch-layout*, sharing*, users-layout*, C |
| Shared | Fields and validation | Focus / edit / invalid / read-only / disabled | Stable bounds; native validity and changed error text | Fixed: common disabled-opacity transition; retained instant focus ring and validation state | M, config-layout*, bbr*, users* |
| Shared | Cards and panels | Enter / hover / selected / drag / removal | Inserted branch fades once; hover does not transform | Retained: 120 ms inserted card, 200 ms elevation; immediate removal avoids stale hit targets or list spacers | M, S, traffic-layout*, client-layout* |
| Shared | Native dialog and backdrop | Open / close / Escape / outside / reverse / removal | Unified enter; close had no visual tail | Fixed: 200 ms enter, 120 ms exit, independent scrim; close/focus immediate, closed rendered dialog inert and hidden from accessibility | P, bbr*, users*, config-layout*, traffic-layout* |
| Shared | Restored native dialog | Keyed move / local refresh / modal restoration | Fresh enter canceled during reconciliation | Fixed: cancel native opacity/translate entrance too; retain closed-dialog inert/aria state across refresh | P, bbr*, M |
| Shared | Native dialog padding and scrim gesture | Drag from content / actual outside click | Pointer-start ownership already tracked | Retained: only genuine outside gestures dismiss; closing scrim cannot intercept background clicks | P, bbr* |
| Shared | Inline disclosure content | Open / close / rapid reverse / dynamic insert | First content child faded on open; no exit or multi-child ownership | Fixed: one native details-content reveal/exit; 200 ms height for in-flow content, final overflow visible; no timer or end-event dependency | P |
| Shared | Initially open disclosure | Initial route / restored open state / refresh | Explicitly initiated reveals avoided nested route entrances | Retained: opt in after summary activation; preserve opt-in marker and open state through refresh | P, presets, config-layout* |
| Shared | Popup menus and calendars | Open / dismiss / Escape / focus leaves / reopen | Enter-only; launcher focus retained | Fixed: opacity-only details-content enter/exit; no height/translate on positioned popups; immediate expanded state | P, S |
| Shared | Operation notice | Success / failure / identical message / dismiss / expiry | Changed notice enters once; dismissal was immediate | Fixed: inert 120 ms visual exit, new notice supersedes old tail; background refresh keeps visible notice | M, P |
| Shared | Inline operation error | Failure / repeated error / hide / retry | Several modal errors appeared without shared feedback | Fixed: `updateFeedback` gives changed/revealed error 160 ms; text/visibility update immediately; identical visible message remains quiet | P, sharing*, users*, config-layout*, traffic-layout* |
| Shared | Reduced motion | Preference before load / during active animation | CSS and native animation skip/cancel | Retained and extended: no enter, exit, delay, size or thumb transition; readable busy/result text stays | M/P/S reduced modes, C |
| Dashboard | Host metrics, progress and charts | Poll / same data / changed metric | In-place patch, no number tween | Retained: keep readable numbers and charts stable; bounded progress width uses 200 ms | dashboard*, S, C |
| Dashboard | Recent nodes and tasks | Automatic updates / selected navigation | Status colors and links update | Retained: no recurring row entrance; content/selected state is direct feedback | dashboard*, P |
| Dashboard | Month picker and year controls | Open / month/year select / close / reverse | Selection feedback and response guards already integrated | Fixed picker exit via shared native disclosure; retained stable picker DOM and 160 ms completed chart feedback | S, P, dashboard* |
| Dashboard | Month request | Rapid months / failed request / retry / route leave | Latest request wins; displayed month restored on failure | Retained: abort old work, clear busy, recover label; animate only completed selected result | S |
| Dashboard | Daily traffic details | Open / close / outside / reopen | Native reveal; immediate close | Fixed shared native exit and closed accessibility state; preserve existing table layout | dashboard*, P |
| Nodes | Overview and empty/permission states | Enter / refresh / add / offline | Stable cards, status labels and empty prompt | Retained: one workspace entry; no online pulse or polling replay; disabled explanations stay visible | admin, empty, readonly, shared-node*, M, P |
| Nodes | Batch mode bar and checks | Enter/exit batch / select / submit / failure | Retained toolbar and selection state | Fixed check-shell color rhythm; retained direct bar/checked changes and immediate task feedback; no disappearing selection animation | batch-layout*, admin, M |
| Nodes | Card sorting | Pointer/touch drag / keyboard / cancel / route leave | Native FLIP, inert ghost, immediate order commit | Retained: transform exclusively owned by drag/FLIP; cancel superseded landing, clean capture and styles | M, ports*, client-order* |
| Node settings | Services / metrics tabs | Mouse / arrows / rapid alternating tabs | Persistent selected underline and one revealed panel | Retained: immediate outgoing hidden panel, 160 ms incoming fade; avoids two interactive panels and old metrics beneath new tab | M, admin, ports* |
| Node settings | Name, address and command copy | Edit / save / copy / failure | Text, busy state and result notice | Retained: same form/caret on refresh; direct copied label and shared notice | admin, ports*, M |
| Node settings | Core install/version/runtime drawers | Expand / collapse / select release / execute | Shared reveal only; button launches skipped opt-in and closed display cut off exit | Fixed button opt-in, discrete parent display exit and focus return to launcher, plus multi-child disclosure exit; retained instant conditional fields, selected release and task state | P, admin, uninstall-writeonly |
| Node settings | Agent capabilities | Toggle / pending / failure / permissions | Thumb/background move; task state authoritative | Fixed disabled opacity; retained pending/error label and server ownership; no checkbox animation delays | capability-settings*, C |
| Node settings | Region chooser | Search / choose / save / close | Native dialog, persistent selected grid | Fixed shared exit; retained live search without pulsing grid and immediate aria-pressed selection | regions, P |
| Node settings | Add node, command and directory | Open / cancel / replace / route leave / late response | Custom focus/lock/request lifecycle complete; instant close | Fixed inert visual tail with independent scrim; remove binding data; cancel on replacement/route; release locks/listeners/timers and focus immediately | P, enrollment*, admin |
| Node settings | Enrollment submit/delete/copy | Pending / duplicate / success / failure / stale completion | Busy guards and owned copy/delete timers | Retained and extended: retiring surface cannot be treated as active by async callbacks; credentials do not resurrect | P, enrollment*, M |
| Live configuration | Node and engine selectors | Select / confirm dirty / rapid select / failed load | Semantic scopes, confirmed request ownership | Retained: workspace fade for genuine node/engine; mounted selectors and focus; superseded confirmation cannot navigate | S, config-scope, config-layout* |
| Live configuration | Source read / source selector | Switch / loading / error / retry / same source | Busy read and completed source-region feedback | Retained: source choice commits directly; only completed semantic change fades; reading does not replace editor or replay same data | S, config-migration, config-layout* |
| Live configuration | Code editor, source file and toolbar | Type / select file / same file / format / refresh | Draft and caret protected; file feedback only on change | Retained: typing/formatting/reads are immediate; 160 ms true file switch; same file preserves caret and selection | config-layout*, M, S |
| Live configuration | Visual studio and workbench tabs | Tab / protocol / inbound / common field | One content owner and semantic selected states | Retained: immediate outgoing hidden section, one 160 ms changed workspace; native conditional controls stay readable | presets, config-inbounds*, S |
| Live configuration | Inbound and tools menus | Open / action / outside / keyboard / reopen | Shared menu semantics, enter-only content | Fixed shared positioned-popup exit; retain dirty guards and launcher DOM | P, config-inbounds*, config-layout* |
| Live configuration | Inbound/outbound editing dialogs | Dynamic create / save / deploy / error / discard / close | Native dynamic dialogs and immediate inline failures | Fixed native entry/exit plus changed-error feedback; critical save/dirty/deploy decisions never wait for animation | config-inbounds*, config-layout*, P |
| Live configuration | Preflight, deployment and result | Loading / task running / success / failed / retry | Pending line, task badge and result link | Retained: only real pending work loops; phase updates and errors are immediate; no editor entrance during task polling | presets, config-layout*, M |
| Configuration archive | Templates, revisions, history/diff | Choose / expand / close / edit / validate / deploy | Native form and selected revision state | Fixed history/diff disclosure exit and shared dialog lifecycle; retain draft/source reads and immediate revision visibility | presets, config-layout*, P |
| Clients | Node/export cards and masonry | Initial / inserted card / refresh / reorder | Stable keyed cards and coalesced measurement | Retained: one inserted branch, no nested entrances, no per-row poll motion; observer/frame cleanup on route exit | client-layout*, client-order*, M |
| Clients | Engine, node, format and search | Change / empty / same selection / typing | Semantic result region and stable inputs | Retained: 160 ms changed selected result; search count updates without dimming composition or moving caret | S, client-layout* |
| Clients | Secret visibility and copy/export | Reveal/mask / copy / disabled / failure | Native password masking and immediate copied/result text | Retained: immediate sensitive text visibility; shared control color/focus and notice, no opacity linger on secrets | client-layout*, P |
| Clients | Parameter and display dialogs | Open / edit / save / close / rapid reopen | Native form, enter-only dialog | Fixed shared reversible exit and closed accessibility retirement; values and business guards unchanged | client-layout*, P |
| Connections | Node, engine, dates and pagination | Apply / load / error / rapid scopes | Latest completed selection region | Retained: 160 ms completed result; immediate busy/query controls; ignore aborted stale enrichment | connections*, S |
| Connections | Table, addresses and empty state | Poll / enrichment / cached response / no matches | Stable rows and scroll | Retained: no row/counter entrance; readable address updates and empty message are sufficient feedback | connections*, S, P |
| Sub-Store | Target, node and search selectors | Choose / pending / failure / typing | Selected region, ordered writes, retained caret | Retained: completed grid 160 ms; recover failed target; live search does not pulse | S, substore-scope, substore-layout |
| Sub-Store | Target/settings/delete dialogs | Open / conditional form / save / delete / close | Native dialogs, form guards | Fixed common native exit; conditional import/rename controls remain direct to protect input and tab order | substore-scope, substore-layout, P |
| Sub-Store | Node cards and sync results | Dynamic cards / copy / sync / refresh | Shared card contract, result text | Retained: inserted card once and stable polls; immediate pending/result labels plus notice | substore-layout, S |
| Tasks | Filter disclosure and controls | Open / collapse / scope/status/action/count | Filter region and persistent selected controls | Fixed disclosure exit; retained one completed timeline feedback for deliberate filter application | S, P, M |
| Tasks | Running/result/failed task states | Pending / progress / success / failure / retry/cancel | Running dot, status, buttons and links | Retained: genuine running opacity pulse; task text/actions update immediately without animation event dependencies | M, admin, bbr* |
| Tasks | Timeline, clocks and paging | Poll / insertion / removal / load more | Silent background rows, retained expanded results | Retained: no list-height choreography or time animation; deliberate mobile load-more gets short feedback and timeline focus | M, S |
| Tasks | Result disclosure | Expand / collapse / reopen after refresh | Enter-only result content and rotating arrow | Fixed native multi-child enter/exit; expanded result state remains mounted through polling | P, M |
| Logs | Node, level, engine, page controls | Select / repeat / refresh | Selected colors and semantic result key | Retained: 160 ms deliberate completed selection; same choice remains quiet; launcher focus preserved | logs, logs-restore, S |
| Logs | Search, append, wrap and export | Type / compose / append / resize / export | Silent rows and retained scroll/input | Retained: direct text/count/checked feedback avoids interrupting reading; no append or same-data entrance | logs, logs-restore, M, S |
| Traffic | Node, engine, endpoint/status filters | Choose / rapid reverse / empty / reset | One selected grid owner | Retained: 160 ms completed result; stable toolbar/sidebar and persistent selected colors; no filter replay on polling | S, traffic-layout* |
| Traffic | Quota/create/status dialogs | Open / submit / error / close / outside / reopen | Shared native enter; immediate close | Fixed common reversible exit and instant accessible close; keep quota controls, disabled state and validation | P, traffic-layout* |
| Traffic | Sync candidate list and row checks | Load / retry / select / submit / failure | Direct checked and busy/result state; errors abrupt | Fixed changed inline-error feedback; retained immediate list/count/selection with no parent/row nested fade | traffic-layout*, P |
| Traffic | Usage/progress/accounting states | Poll / unavailable / limit reached | Persistent meter and diagnostic badge | Retained: bounded 200 ms width; no number tween or continuing online effect; static reason remains readable | traffic-layout*, C |
| Traffic | Policy ordering | Pointer/touch / keyboard / cancel / route leave | Shared FLIP and owned ghost lifecycle | Retained: instant order update; no hover/entrance transform conflict; removed cards and navigation release drag | M, traffic-layout* |
| IPQuality | Node/date/calendar navigation | Select / month/year / close / empty | Semantic report and open-calendar region | Fixed calendar exit; retained 160 ms completed report/date feedback and static disabled dates | S, ip-quality*, P |
| IPQuality | Run/schedule/state controls | Trigger / running / saved / failure / permission | Busy task/result labels and aria-pressed state | Retained: direct selected/scheduled state and shared result feedback; no extra report movement during polling | ip-quality*, S |
| IPQuality | Report, archive/raw JSON and errors | Read / expand / collapse / missing report | Readable report; raw disclosure enter-only | Fixed raw disclosure exit; retain static report figures/output for inspection and copying | ip-quality*, P |
| Access control | Node scopes and rule cards | Select / inserted rule / refresh | Semantic region and common cards | Retained: one 160 ms scope feedback; inserted branch only; rules/metrics do not replay | config-restrictions, P |
| Access control | Rule editor and save feedback | Edit / dirty / save / error / discard / close | Native dialog and busy/dirty/result semantics | Fixed shared native exit; retain immediate field visibility, validity and permission/unsupported explanations | config-restrictions, config-layout*, P |
| TCP | Node and detail/parameter dialogs | Select / open / close / quick reopen | Native dialogs; rapid reopen retained old deep scroll | Fixed genuine launch resets body to form actions; local restoration retains draft/caret/scroll and cancels fresh entrance | bbr*, P |
| TCP | Preset, checks and validation | Fill / toggle / invalid / pending / clear | Direct draft and parameter feedback | Retained: immediate preset/input values, native checks, disabled controls and error text; shared control rhythm | bbr*, C |
| TCP | Confirmation, task and runtime state | Confirm / supersede / fail / poll / task removed | Independent confirmation resolver and runtime controls | Retained: immediate resolver/business state, token dialogs, no polling replay; state cleanup releases disabled snapshot | bbr*, M, P |
| Users | Sidebar/mobile selected user | Choose / refresh / active request / empty | Semantic allocation region and stable selector | Retained: 160 ms completed allocation feedback, stable focus and mobile selected value | S, users*, users-layout* |
| Users | Account and permissions editor | Open / change role / save / error / close | Native dialog, direct permission fields | Fixed native exit and common inline changed-error feedback; retain conditional field visibility and typed drafts | users*, P |
| Users | Allocation/invitation editor | Open / node/engine/quota choice / pending / error | Native dialog and guarded availability snapshots | Fixed error feedback and native exit; retain immediate limit/port fields and fully allocated disabled controls | users-layout*, sharing*, P |
| Sharing | Share rows and node share dialog | Add / edit / revoke / stale/error / close | Draft/revision/consent guards; error had no shared fade | Fixed common error feedback and dialog exit; retain immediate accepted/pending permission text | sharing*, shared-node*, P |
| My quota | Invitations and shared allocation | Open / accept / reject / leave / failure / refresh | Stable quota page, guarded response and inline error | Fixed changed error feedback and shared dialog exit; retain direct consent, quota, busy and result labels | users*, sharing*, S, P |
| Settings | Toggle, select and conditional inputs | Toggle / disabled / font scale/theme / focus | Existing native checked values and visible labels | Fixed check-shell/opacity consistency; retained instant form values and conditional sections to protect editing | capability-settings*, M, C |
| Settings | Save, deployment/update and result | Save / edit while saving / success / failure / retry | Busy save, retained newer draft, changed state badge | Retained: duplicate guard, 160 ms state feedback and common notices; saved response cannot clear newer edits | M, capability-settings* |
| Settings | Version/runtime and copy panels | Hover / copy / pending / unavailable | Common card/controls, static version info | Retained: stable bounds and readable static version; token progress only for real pending work | capability-settings*, P |
| Cross-page branches | Permissions / offline / unsupported / empty | Different role/capability / no data / failure | Disabled reason, empty prompt and error remain readable | Retained: absence is not animated; shared controls/dialogs apply on insertion; no decorative motion on unavailable work | readonly, dashboard-readonly/limited/unavailable, bbr-readonly/writeonly, ip-quality-readonly, empty, shared-node*, P |

## Rules and lifecycle decisions

1. Color, border, background, shadow and disabled opacity use 120 ms. Entering
   result/disclosure content uses 160 ms; dialogs use 200 ms; route opacity uses
   280 ms. Exit opacity/translation uses 120 ms and ease-in; entering spatial
   effects use ease-out. Dialog translation is 6 px in / 4 px out. There is no
   scaling, child stagger or continuing decorative effect.
2. Native dialog `close()` and details `open` state remain authoritative and
   immediate. Discrete display/overlay/content-visibility transitions retain
   only paint. Closed dialogs become inert and aria-hidden before painting the
   tail; reopens restore their previous accessibility attributes. CSS reverses
   from the current appearance. Closing disclosure children also become inert
   and aria-hidden immediately; reopen restores their original attributes.
   Reconciliation preserves these temporary states until the owning reopen.
   Unsupported CSS capabilities close immediately.
3. Custom command/enrollment/directory surfaces release focus traps, body locks,
   request ownership, timers and listeners synchronously. Their short retired
   surface is inert, aria-hidden, pointer-transparent and stripped of binding
   data and IDs. Its closed shadow tree shares one parsed copy of the existing
   CSS, keeping retired markup out of document-level component queries. A new
   modal cancels it. Route departure, removal, preference
   change, cancellation and the bounded native-animation fallback remove it.
   Retired opacity holds at zero until cleanup, so a lost finish event cannot
   flash the old surface during the fallback interval. Live surfaces and drag
   animations keep their existing no-fill behavior.
4. In-flow disclosure height is the additional bounded layout transition.
   There is no per-frame JavaScript measurement. The browser interpolates the
   height and handles reversal. The version drawer uses only its outer height
   (including the hiding summary), avoiding a final summary-height jump; other
   drawers use their content height. Overflow becomes visible at completion so
   focus rings and nested menus are not permanently clipped. Positioned menus
   use opacity only. Initially open disclosures remain static inside routes.
5. Route replacement, outgoing tabs, removals, typing/composition, editor/source
   reads, numeric counters, conditional form fields and native checked/value
   states remain immediate. Old editable content must not overlap the active
   workspace; reading/caret placement and business correctness take priority
   over a visual exit in those cases. Persistent labels, selected color,
   counts, busy state and results provide their complete feedback.
6. Polling, log append, task clocks and identical messages never request another
   entrance. Deliberate selection only animates the completed result region.
   Feedback text/visibility and server effects update before visual work.

## Review and limitations

Use the preview steps in [MOTION.md](MOTION.md). The focused recording for this
follow-up is `output/playwright/surface-motion-review.mp4`. It shows native
close/reopen, custom close/reopen and disclosure reversal. Recordings are local
review artifacts, not repository assets.

Browser evidence is Chromium on Linux with desktop mouse and emulated phone
touch/keyboard, both themes, portrait/landscape and reduced motion. Firefox,
WebKit, physical phones and real remote-agent deployments are not validated by
this environment. Feature detection keeps final content and direct close in
browsers missing modern CSS transitions. There are no schema/API migrations.

Validation on 2026-10-10: `make check` passed with an isolated PostgreSQL 17
database and all 70 browser modes; the Go frontend browser rerun took 409.387 s.
All named component suites above passed. The final lost-finish opacity
hardening also passed the eight motion/popup modes and repeated module/style
checks on the final snapshot. Generated styles, frozen baseline,
module policies and `git diff --check` passed. Real-browser live preference
changes removed custom retired surfaces and closed version drawers immediately,
retaining launcher focus. Phone portrait/landscape inspection found zero active
animations and no horizontal overflow with reduced motion. No required check
was blocked by the environment.

Merge review correction: Debian and Alpine CI exposed a fixed-delay assertion
in the native-dialog exit regression. It now observes the hidden paint state
and zero active animations with a bounded timeout, while still asserting
immediate business/focus/accessibility state. The four popup modes passed
locally after the correction; production motion and timings are unchanged.
