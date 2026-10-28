import { routeForHash } from "./routes.js";
import { bindEvent } from "./refresh.js";

const directorySelector = '[aria-label="设置目录"]';
const sectionSelector = ".settings-workspace .settings-section[id]";
// A section owns the reading position once its heading crosses this line below
// the scrolling area's top; it matches the section scroll margin.
const readingOffset = 96;
// Programmatic section scrolling crosses other sections; sampling waits for it.
let readingSuspendedUntil = 0;

// The API Key section renders only for administrators, so its entry follows the
// same permission and the sidebar can never link to a hidden section.
export function settingsDirectoryMarkup({ can }) {
  return `<nav class="context-menu" aria-label="设置目录"><a href="#settings-engines"><span>01</span>默认内核能力</a><a href="#settings-basic"><span>02</span>基础设置</a><a href="#settings-runtime"><span>03</span>任务与同步</a><a href="#settings-data"><span>04</span>数据与日志</a><a href="#settings-notify"><span>05</span>事件通知</a><a href="#settings-komari"><span>06</span>Komari 联动</a><a href="#settings-cnip"><span>07</span>CN IP 数据源</a><a href="#settings-certificates"><span>08</span>证书申请</a><a href="#settings-deployment"><span>09</span>部署状态</a>${can("admin") ? '<a href="#settings-api-keys"><span>10</span>API Key</a>' : ""}</nav>`;
}

function sectionHash(value) {
  return String(value ?? "").replace(/^#/, "").split("?")[0];
}

// The rendered sections own the directory order: an entry can only be selected
// when the page actually shows that section.
function sectionElements() {
  return [...document.querySelectorAll(sectionSelector)];
}

export function syncSettingsSelection(value, root = document) {
  const hash = sectionHash(value);
  const sections = sectionElements();
  const selected = sections.find((section) => section.id === hash) || sections[0] || null;
  root.querySelectorAll(`${directorySelector} a`).forEach((link) => {
    const active = selected != null && link.getAttribute("href") === `#${selected.id}`;
    link.classList.toggle("active", active);
    if (active) link.setAttribute("aria-current", "location");
    else link.removeAttribute("aria-current");
  });
  return selected?.id || "";
}

export function currentSettingsSection() {
  const sections = sectionElements();
  if (!sections.length) return null;
  const main = sections[0].closest(".workspace-main");
  // Desktop scrolls .workspace-main; touch layouts scroll the page itself.
  const scroller = main && main.scrollHeight > main.clientHeight + 1 ? main : null;
  // The last section can never cross the reading line, so the end of the page
  // belongs to it; otherwise a deep link to it selects its predecessor.
  const atEnd = scroller
    ? scroller.scrollTop + scroller.clientHeight >= scroller.scrollHeight - 2
    : globalThis.scrollY + globalThis.innerHeight >=
        (document.documentElement?.scrollHeight ?? 0) - 2;
  if (atEnd) return sections[sections.length - 1];
  const line = (scroller ? scroller.getBoundingClientRect().top : 0) + readingOffset;
  return sections.reduce(
    (current, section) => section.getBoundingClientRect().top <= line ? section : current,
    sections[0],
  );
}

// Same-route section navigation: scroll the rendered page instead of reloading
// it, so drafts, focus and the loaded settings survive a directory click, a
// deep link and browser back/forward.
export function focusSettingsSection({ state, hash }) {
  if (state.route !== "settings") return false;
  const requested = sectionHash(hash);
  // The bare route hash means "top of the page", never a missing section.
  const section = requested === state.route ? "" : requested;
  const sections = sectionElements();
  const target = section
    ? sections.find((element) => element.id === section)
    : sections[0];
  if (!target) return false;
  state.anchor = section || state.route;
  syncSettingsSelection(target.id);
  // Let the requested section own the selection until its scroll settles.
  readingSuspendedUntil = Date.now() + 700;
  target.scrollIntoView({ block: "start" });
  return true;
}

export function bindSettingsNavigation({ state }) {
  const main = document.querySelector(".settings-workspace")?.closest(".workspace-main");
  if (!main) return;
  // A navigation started after this page rendered must win over a pending
  // scroll sample, otherwise the section would pull the URL back to itself.
  const epoch = state.navigationEpoch;
  let frame = null;
  const settle = () => {
    frame = null;
    if (Date.now() < readingSuspendedUntil) return;
    if (state.route !== "settings" || state.navigationEpoch !== epoch) return;
    // The reader may already have left the page; never pull another route's
    // URL back to a settings section.
    if (routeForHash(globalThis.location?.hash || "") !== "settings") return;
    const section = currentSettingsSection();
    if (!section) return;
    const hash = `#${section.id}`;
    // A section link scrolls to its target before the hashchange task runs, so
    // the URL can already name this section. That navigation owns the
    // selection; sampling must not pre-empt it or the route would reload.
    if (globalThis.location?.hash === hash) return;
    // Reading a section selects it; replaceState keeps history free of scrolls.
    state.anchor = section.id;
    syncSettingsSelection(section.id);
    globalThis.history?.replaceState?.(null, "", hash);
  };
  const schedule = () => {
    if (frame !== null) return;
    frame = globalThis.requestAnimationFrame?.(settle) ?? setTimeout(settle, 16);
  };
  bindEvent(main, "scroll", schedule, { passive: true });
  bindEvent(globalThis, "scroll", schedule, { passive: true });
}
