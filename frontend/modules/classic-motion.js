import { mountTheme } from "./theme-lifecycle.js";
import { cancelMotion } from "./motion.js";
import { disposeFeedbackObservation } from "./presence-motion.js";
import { disposeInteractionFeedback } from "./interaction-feedback.js";
import { disposeMotionIsolation } from "./motion-isolation.js";

export function mountClassicMotion() {
  return mountTheme("classic", scope => {
    scope.onSuspend(() => { cancelMotion(); disposeMotionIsolation(); });
    scope.own(disposeFeedbackObservation);
    scope.own(disposeInteractionFeedback);
    scope.own(disposeMotionIsolation);
    return { cancel: cancelMotion };
  });
}
