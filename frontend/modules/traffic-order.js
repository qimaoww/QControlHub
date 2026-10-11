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
      const value = JSON.stringify(keys);
      storage?.setItem(trafficCardOrderKey, value);
      return storage?.getItem(trafficCardOrderKey) === value;
    } catch { return false; }
  };

  return { savedTrafficCardOrder, saveTrafficCardOrder };
}
