// Each route owns its observer. Construction does not touch the DOM.
export function createCardMasonry(gridSelector, cardSelector) {
  let masonryObserver = null;
  function bind() {
    const grid = document.querySelector(gridSelector);
    if (!grid) return;
    // Normal card grids let CSS align rows, as on the node overview.
    // Only a grid with an explicit pixel row size needs masonry spans.
    if (getComputedStyle(grid).gridAutoRows === "auto") return;
    const cards = [...grid.querySelectorAll(cardSelector)];
    if (!cards.length) return;

    const layout = () => {
      const styles = getComputedStyle(grid);
      const rowHeight = Number.parseFloat(styles.gridAutoRows) || 1;
      const rowGap = Number.parseFloat(styles.rowGap) || 0;
      cards.forEach((card) => {
        const height = card.getBoundingClientRect().height;
        const span = Math.ceil((height + rowGap) / (rowHeight + rowGap));
        card.style.gridRowEnd = `span ${span}`;
      });
    };

    layout();
    requestAnimationFrame(layout);
    if (typeof ResizeObserver === "function") {
      masonryObserver = new ResizeObserver(layout);
      cards.forEach((card) => masonryObserver.observe(card));
    }
  }

  return {
    bind,
    disconnect() { masonryObserver?.disconnect(); masonryObserver = null; },
  };
}
