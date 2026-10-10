# Console motion, after PR #255

This revision starts from the merged `bdfa007b` snapshot of PR #255. The original
PR is merged and its branch was deleted; the new implementation ships in a
linked follow-up PR. The visual direction is an expressive, layered console:
clear spatial entrances, a continuous navigation highlight, elevated interaction
feedback and coordinated surface exits.

## Why the original animation was hard to see

Reproduced in real Chromium using the production stylesheet and deterministic
API fixtures, with the default `prefers-reduced-motion: no-preference` setting:

1. **Small changes, front-loaded easing.** A route animated only opacity `.55 →
   1` over 280 ms. Its first painted frame had no translate or scale. Selection
   feedback was `.8 → 1` over 160 ms; insertion used an even shorter fade. There
   was no layered sequence or outgoing route choreography. These effects ran,
   but offered very little visible spatial information.
2. **The first loading paint consumed the result key.** In
   `syncSelectionMotion`, the initial or animation-suppressed branch recorded
   `data-motion-key` before checking `data-motion-ready`. A first response after
   the route fade saw the same key and skipped its result entrance. A 900 ms
   delayed connection request reproduced an opacity of `1` and **zero** result
   animations at completion. Loading keys now remain uncommitted until ready.
3. **Animation lifecycle used creation time.** The cleanup fallback began at
   `Element.animate()` creation, before playback readiness. Binding/render work
   could spend the budget before a rendering frame. Entrances now wait for the
   next rendering frame, and the fallback starts from `animation.ready`. This
   is a lifecycle hardening; the ordinary route reproduction did start at time
   zero, so it is not claimed as the cause of every missing animation.
4. **Coverage counted static responses as finished.** The old checklist retained
   immediate route/tab/list exits and tiny fades. Those states were functionally
   correct but did not meet the requested visible motion standard.

The source manifest generated the expected `app.css`, the browser loaded its
versioned URL, and reduced motion was **false** in the reproduction. No stale
stylesheet or accidental default reduced-motion override was found. The frozen
initial stylesheet remains unchanged. The final motion layer follows component
skins in `styles/manifest.json`; JavaScript style updates use CSSOM to respect
the production CSP, which prohibits inline style attributes in templates.

## Shared rhythm

| Interaction | Token / duration | Painted change |
| --- | --- | --- |
| Press / immediate control feedback | fast / 120 ms | 2 px hover lift, 1 px press, `.96` press scale; persistent focus ring |
| Result / status feedback | feedback / 280 ms | `.25 → 1` opacity and 12 px result displacement; 14 px small surface entrance |
| Menu / dialog / disclosure | base / 320 ms | Launcher-relative menus; 24 px / `.94 → 1` dialog entrance; native reversible height |
| Page / sidebar expansion | slow / 480 ms | Canvas fade plus 28 px / `.975 → 1` sibling surfaces; 45 ms stagger, capped at 225 ms |
| Card reordering | reorder / 420 ms | FLIP from current painted geometry to the committed slot |
| Exit | exit / 220 ms | 12 px / `.97` retired surface; 10 px outgoing route; scrim fades in parallel |
| Genuine pending work | loading / 1000 ms | Progress/orbit, with readable status text; no idle decorative loop |

The spatial easing is `cubic-bezier(.22,1,.36,1)`; exits use
`cubic-bezier(.4,0,1,1)`. Every completed effect releases its temporary animation.
Individual `translate` and `scale` leave positioning/FLIP `transform` ownership
intact. At most 12 visible, non-nested route surfaces participate in a sequence.
Offscreen rows, telemetry, clocks, logs and identical polling results stay quiet.

## Implementation ownership

- `motion.js`: shared native animation ownership, retargeting from current paint,
  first-result readiness and bounded inert exits. One removal observer
  and preference/visibility listener exist only while animations are active.
- `presence-motion.js`: conditional fields and prompts share complete entry/exit,
  painted-state reversal and sibling reflow. Unchanged text is quiet. A sparse
  text observer covers changed button labels and semantic server feedback,
  without observing native values, checked state, caret, clocks or geometry.
- `interaction-feedback.js`: delegated pointer/focus title hints and native
  constraint failures use authored animated feedback. Tips reverse on refocus,
  stay in the top layer without taking focus, and restore native metadata on
  dismissal. Validity still blocks invalid submissions; corrections retire the
  error without changing the draft/caret. Same-page polling retains these messages.
- `paint-snapshot.js`: interrupted descendant animations freeze their currently
  painted styles inside retiring pages, cards, fields and destroyed dialogs.
  A closed modal cannot replay its headings/footer or flash an inner message.
- `workspace-motion.js`: canvas/sibling choreography, outgoing route feedback,
  context sidebar reveal, moving mounted dock highlight and immediate accessibility
  retirement for tabs. Fast API responses retain outgoing workspace paint in an
  inert shadow tail, so they do not cut off the exit. Reversing a pending request
  restores the mounted page from current opacity/displacement immediately.
- `list-motion.js`: geometry is read only when card membership/order changes;
  removed cards retain painted opacity as inert isolated tails. Repeated reflows
  capture current paint and release old owners before reading destination slots.
- `motion-isolation.js`: one shared parsed production stylesheet for closed
  shadow-tree paint tails, outside document-level component queries.
- `refresh.js`: retains DOM, focus, caret, dirty fields and scroll; same-member
  polling performs no list motion measurements. Motion ownership survives refresh.
- `popup.js` and CSS: native dialogs/details own entrance, exit and rapid reversal;
  custom modal business locks release immediately, with a separate visual tail.
  Region, in/outbound, sharing, invitation, access-rule, sync and allocation
  editors that immediately clear their form retire a complete paint
  copy, rather than animating an emptied dialog. Footer containers remain fixed
  to avoid covering the scrollable body.
- Drag handlers: keyboard reversals capture positions before cancelling the
  previous landing. Pointer ghosts follow native mouse/touch input directly.
- `task-timeline.js`: changed task state animates its badge; output/clock polling
  preserves the mounted row, open result and scroll anchor. New/removed task cards
  use the same insertion, retirement and membership-only reflow as other cards.

Close, delete, submit and request completion commit immediately. Retired visuals
are inert, non-announcing and pointer-transparent. Navigation, removal, reduced
motion and hidden-document changes cancel native animation ownership. Missing
finish events are cleaned up at playback duration + delay + 80 ms.

## Accessibility and compatibility

`prefers-reduced-motion: reduce` disables CSS/WAAPI transitions, stagger, busy
sheen, hover/press geometry and retained paint. Final labels, selected colors,
focus rings, status icons, busy semantics, progress values and error messages
remain. Desktop mouse, keyboard navigation/reordering and mobile touch retain
normal hit areas. Light and dark themes share semantic colors and elevation.

`Element.animate`, constructable stylesheet isolation, native discrete
transitions, `::details-content` and auto-size interpolation are feature-detected
or scoped by CSS support. Older browsers keep readable immediate state for
unsupported surface exits. Chromium desktop/mobile emulation is verified;
physical devices and Firefox/WebKit are outside this local verification.

## Actual recordings

The recordings use identical scripted operations against two separate fixture
servers: merged `bdfa007b` and this implementation. They are real browser captures,
1440 × 960 at 60 fps, converted to H.264 without changing playback speed.

- [Before](../docs/motion/255-before.mp4)
- [After](../docs/motion/255-after.mp4)
- [Mobile / dark / touch](../docs/motion/255-mobile-dark.mp4)

The paired clip covers page transitions, sidebar open/close/reversal, custom
modal enter/exit/rapid reopening, mouse dragging, rapid keyboard reordering and
an initial connection response delayed by 900 ms. The mobile clip adds bottom
navigation, popup reversal, tab exits and the version disclosure. The desktop
clip also reverses a route during a slow request and shows authored title tips,
country search empty/ready feedback, complete destroyed-dialog close and copy
notice dismissal. No screenshots
are used as animation acceptance evidence.

## Reproduce

Start the existing real-browser fixture:

```sh
QCH_BROWSER_SMOKE_SERVE_ONLY=1 node frontend/agents_browser_smoke.mjs
```

Open its printed loopback origin with
`/agents-browser-smoke.html?mode=motion-selection&preview=1#node-settings`.
For a phone, use the `motion-selection-mobile` mode with an actual touch/mobile
browser context, not only a narrow desktop viewport. The recorder uses
Playwright CLI 0.1.22; it is an optional review tool, not a production dependency:

```sh
node scripts/record-motion.mjs http://127.0.0.1:PORT after
```

Use a second fixture server from `bdfa007b` for the matching `before` recording.
Run `make generate-styles` after editing source styles. `make check` runs module
smoke, all 70 Chromium modes, policies/style generation, deployment checks, vet
and the sequential Go suite (including another browser run). Set
`QCH_TEST_DATABASE_URL` to an isolated PostgreSQL database for integration tests.

## Verification

See [MOTION-COVERAGE.md](MOTION-COVERAGE.md) for page/component coverage, exact
commands, playback evidence and the final validation result. Route playback
checks now sample the complete native animation sequence, requiring visible
initial opacity/displacement and a fully restored endpoint. The four popup
modes cover all 18 route entries, both themes, desktop/mobile and normal/reduced
motion: 144 route visits. The longer popup deadline accommodates complete
playback sampling; log-switch interaction budgets remain unchanged.
