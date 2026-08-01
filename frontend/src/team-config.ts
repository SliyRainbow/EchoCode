import type { TeamSettings } from "./types";

export function defaultTeamSettings(): TeamSettings {
  return {
    enabled: false,
    maxReviewRounds: 1,
    roles: [
      { role: "planner", enabled: true, providerId: "", modelId: "" },
      { role: "implementer", enabled: true, providerId: "", modelId: "" },
      { role: "tester", enabled: true, providerId: "", modelId: "" },
      { role: "reviewer", enabled: true, providerId: "", modelId: "" },
      { role: "synthesizer", enabled: true, providerId: "", modelId: "" },
    ],
  };
}
