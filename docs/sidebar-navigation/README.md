# Sidebar navigation scrolling

Baseline: `dae30aaa3d1bfa6c2073fe082593ec42012b2993` (latest `origin/main` checked again before delivery).

## Confirmed cause

On the task history page (`#tasks`), `.active` and `aria-current="page"` agree with the route. The old decoration is an absolutely positioned child of `.app-dock`, while `.dock-nav` scrolls independently. `syncDockMotion` only samples viewport rectangles during shell rendering. Scrolling changes the link's viewport rectangle without moving the decoration; a later shell render can animate the stale decoration toward the new rectangle. The decoration also bypasses the navigation scroller's clip and can paint over the fixed tools. Resizing retains stale inline height/translate until another shell render.

Reproduce on the baseline: start the deterministic fixture with `QCH_BROWSER_SMOKE_SERVE_ONLY=1 node frontend/agents_browser_smoke.mjs`, open the printed URL with `?mode=tasks-preview&preview=1#tasks`, use a short landscape viewport, expand the sidebar, and scroll its navigation. Do not click a destination. Record `location.hash`, the selected link rectangle and the decoration rectangle. At desktop 1280x600 with scrollTop=171, both route and selection remain `#tasks` while decoration y=543 and link y=372: 171px drift. At tablet 1120x680 with scrollTop=91, the drift is 91px. A separate 48px wheel measurement produced 48px drift. No unintended route switch was observed during real wheel or emulated touch drags, including reversals and post-release momentum.

## Changes

Each link owns its decorative indicator. Only the route-selected indicator paints, with the existing gradient, radius and shadow. `inset:0` shares link geometry and the real scrolling/clipping ancestors, so scroll events require no listeners, frame scheduling or layout reads. Settings owns its decoration in the separate tools region.

Capture the old painted rectangle before reconciliation and retain the existing route transition only between visible links in the same region. Capture interrupted motion, cancel previous ownership, and release temporary ResizeObservers on settlement/cancellation/disconnection. Changes in link or container size cancel transient offsets; resting geometry never stores sizes or coordinates. Reduced motion uses the existing controller.

Touch tablets have an explicit 44px expand/collapse button, 44px navigation/tool targets, and no dependency on sticky hover. Mouse hover and keyboard expansion remain available, including hybrid input. Dynamic viewport height, native pan/pinch, scroll containment and clear focus outlines use existing styles and variables. The phone retains four primary destinations plus More (certificates remains accessible inside More). Fix the animated More sheet's containing block so it spans the viewport, and permit scaled/long labels to wrap.

## Executed validation

- `make dock-navigation-browser-check`: six passing scenarios in actual headless Chromium. Desktop light/admin, tablet dark/admin, tablet light/user with reduced motion, phone dark/admin, phone light/user, and actual 125% desktop browser zoom.
- Expanded-sidebar scroll frames measure route and visible decoration geometry (max drift <=1 CSS px), real CDP wheel/touch gestures and reversals, tap hit-testing after scrolling, partial/offscreen/re-entering selection, bottom routes and settings with nonzero scroll, rapid routes, layout changes during animation, touch expansion, font scale 150%, long labels, orientation, reduced motion, pinch zoom, manual reconciliation and task timer polling during continuous scrolling. Scroll position is asserted after reconciliation. Phone sheet width and viewport bounds are measured after actual touch opening; navigation is performed using real touch input.
- Existing browser scenarios: `shell-layout,shell-layout-mobile,motion,motion-reduced,motion-mobile,motion-mobile-reduced,motion-popup-mobile,motion-popup-mobile-reduced` all pass.
- `make frontend-module-check styles-check`, `go test -count=1 ./frontend -skip '^TestAgentBrowserRuntimeSmoke$'`, and module/smoke policy checks pass. `git diff --check` passes.
- Full backend/infrastructure `make check` was not executed for this frontend change. Browser regression is included in `frontend-browser-check` for future runs.
- Optional existing zoom-runner attempts failed before meaningful product checks: one remained on Chromium settings, one used a wide viewport, and a 200% run failed its programmatic keyboard-focus expectation. These are not counted as passing checks. The new regression independently passes actual 125% browser zoom and 150% page-scale/pinch geometry. Native browser 200% zoom remains unverified.

See [machine measurements](evidence/regression.json), [browser log](evidence/existing-browser.txt), [expanded geometry](evidence/capture-expanded-geometry.json), and [baseline/collapsed geometry](evidence/capture-geometry.json).

## Actual before/after captures

Same deterministic admin fixture, task data, fixed clock (`2026-10-18 00:00 +08:00`), theme and viewport per pair. No production credentials, remote panel or user data were used. Videos contain actual browser frames captured during input. Side-by-side MP4s show before on the left and after on the right. They align recording starts, not individual frames; document loading and the new explicit expand control introduce small timing differences. Raw unedited per-version WebMs are retained separately in local `output/playwright/dock-navigation/`.

The supplied screenshot describes an expanded sidebar. These are the primary expanded comparisons:

| Scenario | Before | After | Continuous scrolling |
| --- | --- | --- | --- |
| Desktop, 1280x600, light | [PNG](evidence/desktop-expanded-before.png) | [PNG](evidence/desktop-expanded-after.png) | [MP4](evidence/desktop-expanded-comparison.mp4) |
| Touch tablet landscape, 1120x680, light | [PNG](evidence/tablet-expanded-before.png) | [PNG](evidence/tablet-expanded-after.png) | [MP4](evidence/tablet-expanded-comparison.mp4) |
| Phone, 390x844, dark, More open | [PNG](evidence/phone-before.png) | [PNG](evidence/phone-after.png) | [MP4](evidence/phone-comparison.mp4) |

Desktop comparison uses identical scrollTop=171. The tablet uses the bottom of the list on both versions (91 before, 231 after) so Tasks is visible in both; the difference follows the explicit control and 44px touch targets. Identical tablet scrollTop=110 captures are also retained locally: the fixed version correctly clips the offscreen current item. See the geometry JSON for exact positions. Baseline expansion uses Chromium's forced `:hover` pseudo state to reproduce the screenshot's persistent expanded state without modifying product styles; the fixed tablet expands by real touch on the new control.

## Limits and cleanup

All device coverage uses Chromium viewport/touch/CDP emulation on Linux, including the portrait/landscape switch and post-release native scroll animation. No physical tablet/phone, Android Edge, Safari/iPadOS, operating-system font scaling, or real browser address-bar collapse was tested. Dynamic viewport changes and pinch input are simulations; they do not establish device-specific browser chrome behavior. The supplied screenshot's exact physical screen size and device pixel ratio are unknown; 1120x680 is an approximation of its landscape content area. Fixture API responses exercise the production frontend, not a real backend/account database.

Cleanup registration and final stopped-process/port evidence are in [cleanup.json](evidence/cleanup.json). Only this task's temporary baseline, fixture scratch data, raw recording intermediates, and explicitly identified orphan profile are scheduled for deletion 72 hours after tests finish. Review artifacts, screenshots, final recordings, reports and sanitized logs are preserved separately in this directory and local `output/playwright/dock-navigation/`.

Cleanup is registered as `qch-dock-navigation-cleanup-20261018.timer`, active and enabled with `Persistent=true`. Scheduled execution: **2026-10-21 00:54:00 Asia/Hong_Kong (UTC+08:00)**, or 2026-10-20 16:54:00 UTC (72 hours after test completion, rounded up to the next minute). The two fixture servers and the Playwright session were stopped; ports 45617 and 33625 have no listeners. The timer deletes only `/tmp/qch-dock-navigation-20261018` and the explicitly identified failed-run browser profile `/tmp/qch-dock-browser-FQHYCL`. Timer configuration and its final execution result remain under `/var/lib/qch-dock-navigation-cleanup-20261018/`.
