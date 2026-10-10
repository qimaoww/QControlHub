import { reducedMotion } from "./motion.js";
import { restorePaintScroll } from "./paint-snapshot.js";

let exitStyles;
// Retired paint stays outside component queries, focus and the accessibility
// tree. All exits share one parsed production sheet and bounded ownership.
export function isolateMotionSurface(surface) {
  if (reducedMotion() || !globalThis.CSSStyleSheet?.prototype.replaceSync || !surface.attachShadow) return null;
  try {
    if (!exitStyles) {
      const source = [...document.styleSheets].find(sheet =>
        (sheet.href || "").split(/[?#]/, 1)[0].endsWith("/app.css"));
      if (!source) return null;
      exitStyles = new CSSStyleSheet();
      exitStyles.replaceSync([...source.cssRules].map(rule => rule.cssText).join("\n"));
    }
    const host = document.createElement("div");
    host.className = "motion-exit-host";
    host.inert = true;
    host.setAttribute("aria-hidden", "true");
    const shadow = host.attachShadow({ mode: "closed" });
    shadow.adoptedStyleSheets = [exitStyles];
    surface.inert = true;
    shadow.append(surface);
    document.body.append(host);
    restorePaintScroll(surface);
    return host;
  } catch { return null; }
}
