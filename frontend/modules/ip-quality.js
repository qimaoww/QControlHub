import { createIPQualityController } from "./ip-quality-controller.js";
import { createIPQualityView } from "./ip-quality-view.js";

export function installIPQuality(ctx) {
  const controller = createIPQualityController(ctx, createIPQualityView(ctx));
  return Object.assign(controller.load, { dispose: controller.dispose });
}
