import { gibibyte } from "./traffic-model.js";
export function resetTrafficCreateForm(form, agents = [], renderEngineOptions = () => "") {
  if (!form) return;
  form.reset();
  const agentSelect = form.querySelector("[name=agent_id]");
  const engineSelect = form.querySelector("[name=engine]");
  const agent = agents.find((candidate) => candidate.id === agentSelect?.value);
  if (engineSelect) engineSelect.innerHTML = renderEngineOptions(agent);
}

export const requestFromForm = (form) => {
    const values = new FormData(form);
    return {
      agent_id: values.get("agent_id"),
      engine: values.get("engine"),
      name: values.get("name"),
      port: Number(values.get("port")),
      protocol: values.get("protocol"),
      cycle: values.get("cycle"),
      cycle_anchor: `${values.get("cycle_anchor")}T00:00:00Z`,
      limit_bytes: Math.round(Number(values.get("limit_gb")) * gibibyte),
      auto_block: values.get("auto_block") === "1",
    };
  };
