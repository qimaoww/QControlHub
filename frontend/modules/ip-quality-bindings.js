import { bindEvent } from "./refresh.js";
import { ipQualityToday, nextIPQualityDay, validIPQualityDate } from "./ip-quality-model.js";

export function createIPQualityBindings({ state, load, runCheck, setSchedule }) {
  return () => {
    const input = document.querySelector("[data-ip-quality-date]");
    bindEvent(input, "change", () => {
      if (!validIPQualityDate(input.value) || input.value > ipQualityToday()) {
        input.value = state.data.ipQualityDate || ipQualityToday();
        return;
      }
      void load(input.value);
    });
    document.querySelectorAll("[data-ip-quality-day]").forEach((button) => {
      bindEvent(button, "click", () => {
        const date = nextIPQualityDay(state.data.ipQualityDate, Number(button.dataset.ipQualityDay));
        if (date && date <= ipQualityToday()) void load(date);
      });
    });
    bindEvent(document.querySelector("[data-ip-quality-refresh]"), "click", () => { void load(); });
    bindEvent(document.querySelector("[data-ip-quality-today]"), "click", () => { void load(ipQualityToday()); });
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
