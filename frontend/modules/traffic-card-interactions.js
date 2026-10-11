import { bindCardMove } from "./card-move.js";
import { mergeVisibleTrafficCardOrder } from "./traffic-model.js";

export function createTrafficCardInteractions({ cardInteractions, saveTrafficCardOrder, notify }) {
  let cancelTrafficCardDrag = () => {};
  return {
    enableTrafficCardDrag(grid, allKeys) {
      cancelTrafficCardDrag();
      cancelTrafficCardDrag = bindCardMove({
        grid, selector: "[data-traffic-card-key]", gripSelector: ".traffic-card-grip", cardInteractions, notify,
        save: cards => saveTrafficCardOrder(mergeVisibleTrafficCardOrder(allKeys, cards.map(card => card.dataset.trafficCardKey))),
      });
    },
    cancel() { cancelTrafficCardDrag(); cancelTrafficCardDrag = () => {}; },
  };
}
