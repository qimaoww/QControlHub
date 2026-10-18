# Sidebar selection alignment

## Scope correction

The requested change is the selection-box alignment bug in the expanded sidebar. PR #296 also introduced navigation behavior and layout changes. Those additions are withdrawn in the corrective change based on `22dd96acc33fda3219b7440ccd26a504fa28bc26`.

Sidebar expansion, hover/focus behavior, menu order, phone primary destinations, row sizes, scrolling rules, viewport sizing and the More sheet use their original implementation. `700-shell-dock-rail.css`, `shell-icons.js` and the shell contract expectation are restored byte for byte to the pre-fix baseline `dae30aaa3d1bfa6c2073fe082593ec42012b2993`. No new expansion button or pinned state remains. Certificates is again a primary phone destination wherever the original permissions allow it.

The remaining production change is decorative: each link owns its indicator so it shares the link's scrolling and clipping ancestors. Only the route-selected indicator paints, using the original gradient, radius and shadow. Route changes retain the existing motion; interrupted motion and layout-dependent transient offsets are released. Scrolling does not restart animation or require a scroll listener. Route mapping and click handlers retain the original behavior.

## Confirmed cause and reproduction

On `#tasks`, the page, `.active` and `aria-current="page"` agree. The old indicator was positioned relative to `.app-dock`, outside the independently scrolling `.dock-nav`, and synchronized only during shell rendering. Scrolling moved the selected link without moving the indicator, which also bypassed the navigation clip and could overlap the fixed tools.

Start the baseline fixture with `QCH_BROWSER_SMOKE_SERVE_ONLY=1 node frontend/agents_browser_smoke.mjs`, open the printed local address with `?mode=tasks-preview&preview=1#tasks`, expand the rail, and scroll without clicking. Measure the selected link and indicator rectangles together with `location.hash`.

Fresh captures compare the pre-fix baseline `dae30aaa` with the corrected implementation. Both versions have the same sidebar width (212px), row height (38px), selected-link rectangle and scroll offset. Desktop 1280x600 at scrollTop=171: drift changes from 171px to 0px. Tablet 1120x680 at scrollTop=91: drift changes from 91px to 0px. Route and selection remain `#tasks` throughout.

## Current validation

- `make dock-navigation-browser-check`: desktop light/admin, tablet dark/admin, tablet light/user with reduced motion, phone dark/admin, phone light/user, and actual 125% browser zoom. Assertions cover route invariance and measured indicator geometry during wheel/touch scrolling, clipping/offscreen re-entry, taps after scrolling, rapid routes, layout changes, and task refresh during continuous scrolling. The test retains the original phone destination count and asserts the added expansion control/state is absent.
- `shell-layout,shell-layout-mobile,motion,motion-reduced,motion-mobile,motion-mobile-reduced,motion-popup-mobile,motion-popup-mobile-reduced`: all eight existing browser modes pass.
- `make frontend-module-check styles-check`, frontend Go contracts excluding the full browser suite, and `git diff --check` pass.

Current [measurements](evidence/regression.json), [regression log](evidence/regression.txt), [existing browser log](evidence/existing-browser.txt), and [capture geometry](evidence/capture-expanded-geometry.json) replace the superseded results from PR #296.

## Fresh before/after captures

Identical deterministic admin fixture/data, light theme, viewport and scroll offset within each pair. The clock is fixed at `2026-10-18 00:00 +08:00`. Both tablet versions use Chromium's forced `:hover` only to hold the original expanded state during measurement; product styles and expansion logic are unmodified. Desktop regression also exercises normal mouse hover. Videos contain real browser frames during wheel or emulated touch input, including direction changes and post-release scrolling. Side-by-side recordings show baseline left, corrected implementation right. Raw WebMs are retained in local `output/playwright/dock-navigation/`.

| Scenario | Before | After | Continuous scrolling |
| --- | --- | --- | --- |
| Desktop 1280x600, scrollTop=171 | [PNG](evidence/desktop-expanded-before.png) | [PNG](evidence/desktop-expanded-after.png) | [MP4](evidence/desktop-expanded-comparison.mp4) |
| Tablet 1120x680, scrollTop=91 | [PNG](evidence/tablet-expanded-before.png) | [PNG](evidence/tablet-expanded-after.png) | [MP4](evidence/tablet-expanded-comparison.mp4) |

The old phone screenshots/video and `capture-geometry.json` remain as historical PR #296 artifacts. Their after-images include withdrawn phone layout changes and do not represent this correction. Phone validation now checks the original destination membership and primary touch navigation; the More-sheet layout has been restored without attempting to fix it in this task.

## Limits and cleanup

Browser validation uses Chromium on Linux with viewport/touch/CDP emulation. No physical Android/Edge, iPadOS/Safari, actual address-bar collapse or OS font scaling was verified. Native browser 200% zoom remains unverified. The supplied screenshot's exact device dimensions/DPR are unknown. Fixtures use an in-memory API without production accounts, databases or user data.

The persistent cleanup timer is `qch-dock-navigation-cleanup-20261018.timer`. Its exact execution time, timezone, verified registration, stopped resources and cleanup scope are recorded in [cleanup.json](evidence/cleanup.json). The schedule is reset to at least 72 hours after the corrective tests finish. Only `/tmp/qch-dock-navigation-20261018` and the identified task profile `/tmp/qch-dock-browser-FQHYCL` are deleted. Screenshots, final recordings, reports and sanitized logs are retained separately in this directory and `output/playwright/dock-navigation/`. Timer configuration and its completion result remain under `/var/lib/qch-dock-navigation-cleanup-20261018/`.
