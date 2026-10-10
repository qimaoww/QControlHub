// Each route owns its observer and scheduled layout. Reads precede writes;
// transforms never affect the measured height of a masonry row.
export function createCardMasonry(gridSelector, cardSelector) {
  let masonryObserver = null;
  let frame = null;
  const disconnect = () => {
    masonryObserver?.disconnect();
    masonryObserver = null;
    if (frame != null) cancelAnimationFrame(frame);
    frame = null;
  };
  function bind() {
    disconnect();
    const grid = document.querySelector(gridSelector);
    if (!grid) return;
    const cards = [...grid.querySelectorAll(cardSelector)];
    if (!cards.length) return;
    const layout = () => {
      frame = null;
      if (!grid.isConnected) return disconnect();
      const styles = getComputedStyle(grid);
      const rowHeight = Number.parseFloat(styles.gridAutoRows) || 1;
      const rowGap = Number.parseFloat(styles.rowGap) || 0;
      const spans = cards.map(card => Math.ceil((card.offsetHeight + rowGap) / (rowHeight + rowGap)));
      cards.forEach((card, index) => {
        const value = `span ${spans[index]}`;
        if (card.style.gridRowEnd !== value) card.style.gridRowEnd = value;
      });
    };
    layout();
    if (typeof ResizeObserver === "function") {
      masonryObserver = new ResizeObserver(() => {
        if (frame == null) frame = requestAnimationFrame(layout);
      });
      cards.forEach(card => masonryObserver.observe(card));
    }
  }
  return { bind, disconnect };
}
