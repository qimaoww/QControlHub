`ip_quality_native.ansi` is a complete 47-row IPQuality regression report,
reconstructed from the native SVG example supplied during PR #226 review.
Identity, location and report counters use documentation/example values.
It exercises all six sections, score bar boundaries, ANSI emphasis and mail
status backgrounds. It is not a fresh network probe or a captured node run.

`frontend/testdata/ip-quality-native-reference.svg` preserves the supplied
native layout with the same example substitutions. Browser tests compare it
against the production renderer's generated images as actual `<img>` pixels.
The reference is independent of the renderer and must not be regenerated from
it. The original script is https://github.com/xykt/IPQuality.

Regenerate only the two renderer output fixtures after an intentional change:

    QCH_UPDATE_IP_QUALITY_FIXTURE=1 go test ./internal/api -run TestIPQualityBrowserFixtureMatchesRenderer
