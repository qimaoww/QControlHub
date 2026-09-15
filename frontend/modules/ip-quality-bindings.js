export function createIPQualityBindings({ state, render, load }) {
  return () => {
    const localDate = (date) => [date.getFullYear(), String(date.getMonth() + 1).padStart(2, "0"), String(date.getDate()).padStart(2, "0")].join("-");
    const today = localDate(new Date());
    const input = document.querySelector("[data-ip-quality-date]");
    input?.addEventListener("change", () => {
      const value = input.value;
      if (!value) return;
      state.data.ipQualityDate = value;
      void load(value);
    });
    document.querySelectorAll("[data-ip-quality-day]").forEach((button) => {
      button.addEventListener("click", () => {
        const current = state.data.ipQualityDate || input?.value;
        if (!current) return;
        const next = new Date(`${current}T00:00:00`);
        next.setDate(next.getDate() + Number(button.dataset.ipQualityDay || 0));
        const value = localDate(next);
        if (value > today) return;
        state.data.ipQualityDate = value;
        void load(value);
      });
    });
    document.querySelector("[data-ip-quality-refresh]")?.addEventListener("click", () => {
      void load(state.data.ipQualityDate, { force: true });
    });
  };
}
