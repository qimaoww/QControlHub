`ip_quality_native.ansi` is a complete 47-row IPQuality regression report,
reconstructed from the native SVG example supplied during PR #226 review.
Identity, location and report counters use documentation/example values.
It exercises all six sections, score bar boundaries, ANSI emphasis and mail
status backgrounds. It is not a fresh network probe or a captured node run.

`frontend/testdata/ip-quality-native-reference.svg` preserves the supplied
native layout with the same example substitutions. It documents the original
appearance. The updated presentation deliberately
uses upright text and a fixed grid to correct slant and font-fallback drift.
Browser tests verify upright glyphs, columns and image aspect ratios.
The reference is independent of the renderer and must not be regenerated from
it. The original script is https://github.com/xykt/IPQuality.

The renderer fixtures include IPv4 and both compressed/full IPv6, low/medium/
high risk, mixed/all/limited/blocked unlocks, and missing provider results. IPv6
headers use upstream centering and omit IPv4-only DNS blacklist rows.

Regenerate the renderer output fixtures after an intentional change:

    QCH_UPDATE_IP_QUALITY_FIXTURE=1 go test ./internal/api -run TestIPQualityBrowserFixtureMatchesRenderer
