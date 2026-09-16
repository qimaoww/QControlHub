import { createIPQualityController } from "./ip-quality-controller.js";
import { createIPQualityView } from "./ip-quality-view.js";

export function installIPQuality(ctx) {
  return createIPQualityController(ctx, createIPQualityView(ctx)).load;
}
