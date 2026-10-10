import { updateFeedback } from "./presence-motion.js";
import { utcMonth } from "./dashboard-model.js";
import { bindDialogBackdrop, closePopup } from "./popup.js";
export function createDashboardBindings({ state, notify }, { panelMetrics, dashboard }) {
  return ({ trafficYear, trafficMonth }) => {
  panelMetrics.mount();
  document.querySelectorAll("[data-dashboard-agent]").forEach((link) => {
    link.onclick = () => {
      state.data.selectedAgent = link.dataset.dashboardAgent;
    };
  });
  document.querySelectorAll("[data-dashboard-status]").forEach((link) => {
    link.onclick = () => {
      state.data.taskFilters = { status: link.dataset.dashboardStatus };
    };
  });
  document.querySelectorAll("[data-dashboard-task]").forEach((link) => {
    link.onclick = () => {
      state.data.focusTask = link.dataset.dashboardTask;
    };
  });
  const trafficMonthPicker = document.querySelector("[data-dashboard-traffic-month]");
  if (trafficMonthPicker) {
    let pickerYear = trafficYear;
    const currentMonth = utcMonth();
    const currentYear = Number(currentMonth.slice(0, 4));
    const yearLabel = trafficMonthPicker.querySelector("[data-dashboard-month-year]");
    const monthButtons = [...trafficMonthPicker.querySelectorAll("[data-dashboard-month-option]")];
    const yearButtons = [...trafficMonthPicker.querySelectorAll("[data-dashboard-month-year-shift]")];
    const renderMonthPicker = () => {
      yearLabel.textContent = pickerYear;
      monthButtons.forEach((button) => {
        const value = `${pickerYear}-${String(button.dataset.monthIndex).padStart(2, "0")}`;
        button.dataset.dashboardMonth = value;
        button.disabled = value > currentMonth;
        button.classList.toggle("active", value === trafficMonth);
      });
      yearButtons.forEach((button) => {
        button.disabled = Number(button.dataset.dashboardMonthYearShift) > 0 && pickerYear >= currentYear;
      });
    };
    const selectMonth = async (value) => {
      if (value === state.data.dashboardTrafficMonth) {
        closePopup(trafficMonthPicker);
        trafficMonthPicker.querySelector("summary").focus({ preventScroll: true });
        return;
      }
      const data = state.data, epoch = state.navigationEpoch;
      const previousMonth = trafficMonth;
      state.data.dashboardTrafficMonth = value;
      const chart = document.querySelector(".dashboard-traffic-chart");
      const hint = document.querySelector(".dashboard-traffic-title small");
      chart?.setAttribute("aria-busy", "true");
      if (hint) { updateFeedback(hint, "UTC 自然日 · 正在读取…"); hint.setAttribute("role", "status"); }
      closePopup(trafficMonthPicker);
      trafficMonthPicker.querySelector("summary").focus({ preventScroll: true });
      try {
        await dashboard({ overview: state.data.overview });
      } catch (error) {
        if (data !== state.data || epoch !== state.navigationEpoch ||
            state.route !== "dashboard" || data.dashboardTrafficMonth !== value) return;
        data.dashboardTrafficMonth = previousMonth;
        chart?.removeAttribute("aria-busy");
        if (hint?.isConnected) updateFeedback(hint, "UTC 自然日");
        notify?.(`读取流量月份失败：${error.message}`, "error");
      }
    };
    yearButtons.forEach((button) => {
      button.onclick = () => {
        pickerYear += Number(button.dataset.dashboardMonthYearShift);
        renderMonthPicker();
      };
    });
    monthButtons.forEach((button) => {
      button.onclick = () => selectMonth(button.dataset.dashboardMonth);
    });
    trafficMonthPicker.querySelector("[data-dashboard-current-month]").onclick = () => selectMonth(currentMonth);
    renderMonthPicker();
  }
  const trafficDetailsButton = document.querySelector("[data-dashboard-traffic-details]");
  const trafficDetailsDialog = document.querySelector("[data-dashboard-traffic-dialog]");
  if (trafficDetailsButton && trafficDetailsDialog) {
    trafficDetailsButton.onclick = () => trafficDetailsDialog.showModal();
    trafficDetailsDialog.querySelectorAll("[data-dashboard-traffic-close]").forEach((button) => {
      button.onclick = () => trafficDetailsDialog.close();
    });
    bindDialogBackdrop(trafficDetailsDialog);
  }
  };

}
