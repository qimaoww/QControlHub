import { bindEvent } from "./refresh.js";
import { closePopup } from "./popup.js";
import { ipQualityLatest } from "./ip-quality-model.js";

export function createIPQualityBindings({ state, load, runCheck, setSchedule, select, showMonth }) {
  return () => {
    document.querySelectorAll("[data-ip-quality-agent]").forEach((link) => {
      bindEvent(link, "click", (event) => {
        event.preventDefault();
        select(link.dataset.ipQualityAgent || "");
      });
    });
    const calendar = document.querySelector("[data-ip-quality-calendar]");
    if (calendar) bindEvent(document, "pointerdown", (event) => {
      if (state.route === "ip-quality" && calendar.open && !calendar.contains(event.target)) closePopup(calendar);
    });
    bindEvent(calendar, "keydown", (event) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      closePopup(calendar);
      calendar.querySelector("summary").focus();
    });
    bindEvent(calendar, "focusout", (event) => {
      if (event.relatedTarget && !calendar.contains(event.relatedTarget)) closePopup(calendar);
    });
    document.querySelectorAll("[data-ip-quality-date-option]").forEach((button) => {
      bindEvent(button, "click", () => {
        if (button.disabled) return;
        closePopup(calendar);
        calendar.querySelector("summary").focus();
        void load(button.dataset.ipQualityDateOption);
      });
    });
    document.querySelectorAll("[data-ip-quality-month]").forEach((button) => {
      bindEvent(button, "click", () => { if (!button.disabled) showMonth(button.dataset.ipQualityMonth); });
    });
    document.querySelectorAll("[data-ip-quality-day]").forEach((button) => {
      bindEvent(button, "click", () => {
        if (!button.disabled && button.dataset.ipQualityDateJump) void load(button.dataset.ipQualityDateJump);
      });
    });
    bindEvent(document.querySelector("[data-ip-quality-refresh]"), "click", () => { void load(); });
    bindEvent(document.querySelector("[data-ip-quality-latest]"), "click", () => { void load(ipQualityLatest); });
    document.querySelectorAll("[data-ip-quality-run]").forEach((button) => {
      bindEvent(button, "click", () => { if (!button.disabled) void runCheck(button.dataset.ipQualityRun); });
    });
    document.querySelectorAll("[data-ip-quality-schedule]").forEach((button) => {
      bindEvent(button, "click", () => {
        if (!button.disabled) void setSchedule(button.dataset.ipQualitySchedule, button.dataset.enabled !== "true");
      });
    });
  };
}
