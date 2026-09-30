import { ipQualityToday, ipQualityLatest, ipQualityHistoryNeighbor, nextIPQualityMonth } from "./ip-quality-model.js";

export function createIPQualityCalendarView({ esc }) {
  return ({ date, timezone, dates = [], month, loading, readFailed }) => {
    const latest = date === ipQualityLatest, today = ipQualityToday();
    const newestMonth = (dates[0] || today).slice(0, 7);
    const oldestMonth = (dates.at(-1) || today).slice(0, 7);
    const defaultMonth = latest ? newestMonth : date.slice(0, 7);
    const visibleMonth = month >= oldestMonth && month <= newestMonth ? month : defaultMonth;
    const previousMonth = nextIPQualityMonth(visibleMonth, -1), nextMonth = nextIPQualityMonth(visibleMonth, 1);
    const previousDate = ipQualityHistoryNeighbor(dates, date, -1), nextDate = ipQualityHistoryNeighbor(dates, date, 1);
    const first = new Date(`${visibleMonth}-01T12:00:00`);
    const offset = (first.getDay() + 6) % 7;
    const end = new Date(first);
    end.setMonth(end.getMonth() + 1);
    end.setDate(0);
    const available = new Set(dates);
    const blocked = loading || readFailed;
    const days = Array.from({ length: end.getDate() }, (_, index) => {
      const value = `${visibleMonth}-${String(index + 1).padStart(2, "0")}`;
      const enabled = !blocked && available.has(value), selected = value === date;
      return `<button type="button" data-ip-quality-date-option="${value}" data-refresh-key="calendar-${value}" aria-label="${value}${enabled ? "，有检测记录" : "，不可选"}" aria-pressed="${selected}"${value === today ? ' aria-current="date"' : ""}${enabled ? "" : " disabled"} title="${enabled ? "查看检测记录" : "无可查看的检测记录"}">${index + 1}</button>`;
    }).join("");
    return `<div class="ip-quality-date-picker">
      <button type="button" class="button small" data-ip-quality-latest aria-pressed="${latest}">最新结果</button>
      <button type="button" class="button small" data-ip-quality-day="-1" data-ip-quality-date-jump="${previousDate}" aria-label="较早的检测日期"${blocked || !previousDate ? " disabled" : ""}>‹</button>
      <div class="ip-quality-date"><span title="${esc(timezone)}">历史日期</span>
        <details class="ip-quality-calendar" data-ip-quality-calendar data-refresh-key="ip-quality-calendar">
          <summary data-ip-quality-date data-selected-date="${latest ? "" : esc(date)}" aria-label="选择历史检测日期"><span>${latest ? "选择检测日期" : esc(date)}</span><i aria-hidden="true">⌄</i></summary>
          <div class="ip-quality-calendar-popover" role="group" aria-label="历史检测日历">
            <header><button type="button" data-ip-quality-month="${previousMonth}" aria-label="上个月"${blocked || previousMonth < oldestMonth ? " disabled" : ""}>‹</button><strong>${esc(visibleMonth.slice(0, 4))} 年 ${Number(visibleMonth.slice(5))} 月</strong><button type="button" data-ip-quality-month="${nextMonth}" aria-label="下个月"${blocked || nextMonth > newestMonth ? " disabled" : ""}>›</button></header>
            <div class="ip-quality-calendar-week" aria-hidden="true">${["一", "二", "三", "四", "五", "六", "日"].map((day) => `<span>${day}</span>`).join("")}</div>
            <div class="ip-quality-calendar-grid">${'<span aria-hidden="true"></span>'.repeat(offset)}${days}</div>
            <p>${loading ? "正在读取检测日期…" : readFailed ? "日期读取失败，请刷新重试。" : dates.length ? "仅可选择有检测记录的日期" : "暂无检测记录"}</p>
          </div>
        </details>
      </div>
      <button type="button" class="button small" data-ip-quality-day="1" data-ip-quality-date-jump="${nextDate}" aria-label="较新的检测日期"${blocked || !nextDate ? " disabled" : ""}>›</button>
      <button type="button" class="button small" data-ip-quality-refresh${loading ? " disabled" : ""}>${loading ? "正在读取…" : "刷新记录"}</button>
    </div>`;
  };
}
