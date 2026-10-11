# Client configuration workspace

Baseline: main `df1ad925aa270a146b2ad342143546d8ff74c1ea`. Screenshots use the existing `client-layout` browser fixture: eight nodes, four engines, thirteen inbounds, an offline node, IPv6, long names, and a missing connection address. All credentials are synthetic test values.

## Information and interactions

- Keep node selection in the existing context sidebar. Show the selected scope and filtered inbound count above search, export format, and engine filters.
- Compact responsive masonry cards have a node header, one header per engine, and consistently aligned inbound names, protocols, ports, and primary copy controls. Flags have a reserved column and at least 10 px clearance from text. Avoid repeating engine badges and totals within every inbound.
- URL/YAML previews start collapsed and masked. Copy is always available for the selected format without opening the preview. Unsupported exports have an explanation and disabled copy control; no alternative format is silently copied. Long YAML has a bounded scroll area. Closing a preview or parameter dialog masks its values again.
- Display settings use one form transaction. Restore automatic address edits the draft, preserves family preference, and requires Save. Typing a manual address disables family selection; clearing it unlocks selection. Failed saves keep the modal and input values, show an inline error, and unlock controls for retry.
- The scope description follows `putAgentClientAddress` -> `SetConfigClientPreferences`: preferences belong to the deployed config and the engine/listen-address/port identity. Other nodes reusing that config can change too; unrelated configs and ports remain independent. The backend's address-family fallback is explained explicitly.
- Refresh failures retain the last usable exports and show a retry action. Successful refresh preserves selected node, engine, format, search drafts, caret, and scroll position through existing keyed reconciliation. Stable profile identities include config, engine, tag, and port.
- Users with client-access permission but no agents permission can select nodes from the authorized export response. Read-only users have no edit controls. Server configuration links require the existing read permission.

## Validation evidence

Reused and extended `frontend/smoke/client-access.mjs`, `frontend/browser/client-layout.mjs`, `frontend/browser/node-runtime.mjs`, and the existing browser runner. `client-workspace.mjs` adds dense fleet, clipboard, refresh, and disclosure scenarios to the same runner.

Verified:

- Node selection/order, incompatible engine reset, custom name/port/IPv6 search, empty results/reset, format persistence, unsupported exports, first load/failure/retry, and missing-address guidance.
- Complete URL and multiline YAML copy with previews closed; native Chromium clipboard checked with read/write permission, deterministic clipboard rejection, and the legacy selection fallback. Late copy responses cannot enable an unsupported format.
- Sensitive parameter reveal/hide, remasking on close/reopen, bounded YAML, unique controls across 60 inbounds, long names, and both theme palettes.
- Save failure retains drafts and displays an inline error; repeated submit sends one request; independent port names/addresses/family choices; automatic reset is draft-only until Save.
- Read-only and client-only permission fixtures; 390 x 844 touch viewport and additional 320 px narrow-screen inspection without horizontal overflow.
- Existing selection regression reproduced an unrelated quota refresh focus failure on unmodified main. A small launcher-focus restoration in `user-quota.js` fixes it; all four normal/reduced-motion desktop/mobile selection scenarios pass.

The PR records the final `make check` result and required CI status. Styles are regenerated with `make generate-styles`.

## Visual comparisons

Each image shows the same data and viewport before (left) and after (right), with settled animations. Desktop: 1440 x 1000; mobile: 390 x 844 with touch emulation. Flag artwork uses the browser fixture's opt-in preview provider.

| View | Comparison |
| --- | --- |
| Desktop, light | [Before and after](desktop-light.png) |
| Desktop, dark | [Before and after](desktop-dark.png) |
| Mobile, light | [Before and after](mobile-light.png) |
| Mobile, dark | [Before and after](mobile-dark.png) |

To inspect the fixture, run `QCH_BROWSER_SMOKE_SERVE_ONLY=1 QCH_BROWSER_SMOKE_PREVIEW_FLAGS=1 node frontend/agents_browser_smoke.mjs` and open its URL with `?mode=client-layout&preview=1#client-access`. The preview contains no production API access.
