import { setVisible } from "./presence-motion.js";
import { reconcileView } from "./refresh.js";
import { cancelMotion, enterSurface } from "./motion.js";
import { captureListMotion, finishListMotion } from "./list-motion.js";
export function openResultTaskIds() {
    return new Set(
      [
        ...document.querySelectorAll(
          "[data-task-id] details[data-task-result][open]",
        ),
      ]
        .map((details) => details.closest("[data-task-id]")?.dataset.taskId)
        .filter(Boolean),
    );
  }

export function reconcileTaskTimeline(timeline, taskCards) {
    const template = document.createElement("template");
    template.innerHTML = taskCards;
    const freshCards = [...template.content.children];
    const destination = timeline.cloneNode(false);
    destination.append(...freshCards);
    const snapshot = captureListMotion(timeline, destination, card => card.dataset.taskId);
    if (freshCards.some((card) => !card.dataset.taskId)) {
      timeline.replaceChildren(...freshCards);
      finishListMotion(timeline, snapshot);
      return;
    }

    const existingCards = new Map(
      [...timeline.querySelectorAll(":scope > [data-task-id]")].map((card) => [
        card.dataset.taskId,
        card,
      ]),
    );
    const nextCards = freshCards.map((freshCard) => {
      const existingCard = existingCards.get(freshCard.dataset.taskId);
      if (existingCard) {
        const before = existingCard.querySelector(".status-label")?.textContent;
        const reconciled = reconcileView(existingCard, freshCard);
        const status = reconciled.querySelector(".status-label");
        if (status && before !== status.textContent) enterSurface(status, { token: "--motion-feedback", id: "qch-task-state" });
        return reconciled;
      }
      return freshCard;
    });

    let cursor = timeline.firstElementChild;
    nextCards.forEach((card) => {
      if (card === cursor) {
        cursor = cursor.nextElementSibling;
      } else {
        timeline.insertBefore(card, cursor);
      }
    });
    const retainedCards = new Set(nextCards);
    [...timeline.children].forEach((card) => {
      if (!retainedCards.has(card)) card.remove();
    });
    finishListMotion(timeline, snapshot);
    requestAnimationFrame(() => {
      if (!timeline.isConnected || timeline.closest(".workspace-main")?.getAnimations().some(a => a.id === "qch-route")) return;
      nextCards.filter(card => !existingCards.has(card.dataset.taskId) && !card.hidden).slice(0, 8).forEach(card => {
        const bounds = card.getBoundingClientRect();
        if (bounds.bottom > 0 && bounds.top < innerHeight) enterSurface(card, { id: "qch-task-added" });
      });
    });
  }

export function captureTaskAnchor(timeline) {
    const main = document.querySelector(".workspace-main");
    if (!main) return null;
    const mobile = window.matchMedia("(max-width: 820px)").matches;
    const scroller = mobile ? document.scrollingElement : main;
    if (!scroller || scroller.scrollTop <= 1) return null;
    const viewportTop = mobile ? 0 : main.getBoundingClientRect().top;
    const viewportBottom = mobile
      ? window.innerHeight
      : main.getBoundingClientRect().bottom;
    const anchors = [...timeline.querySelectorAll(":scope > [data-task-id]")]
      .filter((card) => {
        if (card.hidden) return false;
        const bounds = card.getBoundingClientRect();
        return bounds.bottom > viewportTop && bounds.top < viewportBottom;
      })
      .map((card) => ({
        id: card.dataset.taskId,
        top: card.getBoundingClientRect().top,
      }));
    return anchors.length ? { scroller, anchors } : null;
  }

export function restoreTaskAnchor(anchor, timeline) {
    if (!anchor?.scroller?.isConnected) return;
    const cards = new Map(
      [...timeline.querySelectorAll(":scope > [data-task-id]")].map((card) => [
        card.dataset.taskId,
        card,
      ]),
    );
    const previous = anchor.anchors.find(
      (entry) => cards.has(entry.id) && !cards.get(entry.id).hidden,
    );
    if (!previous) return;
    const delta =
      cards.get(previous.id).getBoundingClientRect().top - previous.top;
    if (Math.abs(delta) < 0.5) return;
    anchor.scroller.scrollTop += delta;
  }

export function setupTaskPagination(timeline) {
    let loadMore = timeline.nextElementSibling;
    if (!loadMore?.matches(".task-load-more")) {
      loadMore = document.createElement("button");
      loadMore.className = "button task-load-more";
      loadMore.type = "button";
      loadMore.textContent = "加载更多任务";
      timeline.after(loadMore);
    }
    const rows = [...timeline.querySelectorAll(":scope > [data-task-id]")];
    rows.forEach((row) => (row.hidden = false));
    const shouldCollapse =
      rows.length > 20 &&
      window.matchMedia("(max-width: 820px)").matches &&
      timeline.dataset.mobileExpanded !== "true";
    rows.slice(shouldCollapse ? 20 : rows.length).forEach(
      (row) => (row.hidden = true),
    );
    setVisible(loadMore, shouldCollapse);
    loadMore.onclick = () => {
      rows.forEach((row) => (row.hidden = false));
      timeline.dataset.mobileExpanded = "true";
      setVisible(loadMore, false);
      cancelMotion(timeline);
      enterSurface(timeline, { token: "--motion-feedback", id: "qch-selection" });
      timeline.tabIndex = -1;
      timeline.focus({ preventScroll: true });
    };
  }
