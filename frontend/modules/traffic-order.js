import { trafficCardOrderKey } from "./traffic-model.js";
export function createTrafficOrder(storage) {
  const savedTrafficCardOrder = () => {
    try {
      const parsed = JSON.parse(storage?.getItem(trafficCardOrderKey));
      if (!Array.isArray(parsed)) return [];
      return [...new Set(parsed.filter((key) => typeof key === "string" && key))];
    } catch {
      return [];
    }
  };
  const saveTrafficCardOrder = (keys) => {
    try {
      storage?.setItem(trafficCardOrderKey, JSON.stringify(keys));
    } catch {}
  };

  return { savedTrafficCardOrder, saveTrafficCardOrder };
}
