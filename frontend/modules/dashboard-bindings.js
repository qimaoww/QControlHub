import { utcMonth } from "./dashboard-model.js";
export function createDashboardBindings({ state }, { panelMetrics, dashboard }) {
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
      const previousMonth = state.data.dashboardTrafficMonth;
      state.data.dashboardTrafficMonth = value;
      trafficMonthPicker.open = false;
      try {
        await dashboard({ overview: state.data.overview });
      } catch {
        state.data.dashboardTrafficMonth = previousMonth;
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
    trafficDetailsDialog.onclick = (event) => {
      if (event.target === trafficDetailsDialog) trafficDetailsDialog.close();
    };
  }
  };

}
