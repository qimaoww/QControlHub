const gripIcon = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M9 6h.01M9 12h.01M9 18h.01M15 6h.01M15 12h.01M15 18h.01"/></svg>';
const instructions = "拖动手柄调整顺序；空格拾起，方向键移动，空格放下，Esc 取消";

// Instructions and order announcements are available to assistive technology;
// the drop position is communicated visually by the card border itself.
export function cardMoveHelp(scope) {
  return `<div class="visually-hidden" data-refresh-key="card-move-help-${scope}"><span id="card-move-help-${scope}">${instructions}</span><span data-card-order-status role="status" aria-live="polite" aria-atomic="true"></span></div>`;
}

export function cardMoveGrip(scope, extraClass = "", label = "卡片") {
  return `<span class="node-card-grip ${extraClass}" title="拖动或用方向键调整顺序" role="button" tabindex="0" aria-label="调整 ${label} 的顺序" aria-describedby="card-move-help-${scope}" aria-pressed="false">${gripIcon}</span>`;
}
