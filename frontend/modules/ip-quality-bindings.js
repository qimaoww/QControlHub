import { bindEvent } from "./refresh.js";
import { closePopup } from "./popup.js";
import { ipQualityLatest } from "./ip-quality-model.js";

export function createIPQualityBindings({ state, load, runCheck, setSchedule, select, showMonth }) {
  const imageErrors = new WeakSet();
  return () => {
    document.querySelectorAll(".ip-quality-archive").forEach(figure => {
      const image = figure.querySelector("img"), error = figure.querySelector(".ip-quality-archive-error");
      const failed = () => { imageErrors.add(image); error.hidden = false; };
      bindEvent(image, "error", failed);
      bindEvent(image, "load", () => { imageErrors.delete(image); error.hidden = true; });
      // Reconciliation may restore the template's hidden attribute, while the
      // unchanged image has already failed and will emit no second event.
      if (imageErrors.has(image) || image.complete && !image.naturalWidth) error.hidden = false;
      bindEvent(figure.querySelector("[data-ip-quality-image-retry]"), "click", () => {
        imageErrors.delete(image);
        error.hidden = true;
        image.removeAttribute("src");
        image.src = figure.querySelector("a[download]").getAttribute("href").split("?")[0];
      });
    });
    document.querySelectorAll("[data-ip-quality-agent]").forEach((link) => {
      bindEvent(link, "click", (event) => {
        event.preventDefault();
        select(link.dataset.ipQualityAgent || "");
      });
    });
    const calendar = document.querySelector("[data-ip-quality-calendar]");
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
