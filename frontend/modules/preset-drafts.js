// Drafts contain credentials: keep them in session memory, never browser
// storage. Keys include the server revision so stale drafts cannot overwrite
// a newer configuration after another editor saves.
const valueType = control => ["checkbox", "radio"].includes(control.type) ? control.type : "value";
const snapshot = form => [...form.elements].filter(control => control.name && !["submit", "button", "reset"].includes(control.type))
  .map(control => ({name:control.name, type:valueType(control), value:control.value, checked:Boolean(control.checked)}));
const signature = values => JSON.stringify(values);

export function createPresetDrafts() {
  const drafts = new Map(), bindings = new Map();
  const capture = () => {
    for (const [form, binding] of bindings) {
      if (form.isConnected === false) { bindings.delete(form); continue; }
      const values = snapshot(form);
      if (signature(values) === binding.baseline) drafts.delete(binding.key);
      else drafts.set(binding.key, values);
    }
  };
  return {
    capture,
    bind(form, key) {
      if (!form || bindings.get(form)?.key === key) return;
      const baseline = signature(snapshot(form));
      const saved = drafts.get(key);
      if (saved) {
        const controls = [...form.elements];
        for (const value of saved) {
          const control = controls.find(item => item.name === value.name && valueType(item) === value.type &&
            (value.type !== "radio" || item.value === value.value));
          if (!control) continue;
          if (["checkbox", "radio"].includes(value.type)) control.checked = value.checked;
          else control.value = value.value;
        }
      }
      bindings.set(form, {key, baseline});
    },
    otherDirty(key, scope) { capture(); return [...drafts.keys()].some(item => item.startsWith(scope) && item !== key); },
    dirty(scope = "") { capture(); return [...drafts.keys()].some(key => key.startsWith(scope)); },
    clear(scope = "") {
      for (const key of drafts.keys()) if (key.startsWith(scope)) drafts.delete(key);
      for (const [form, binding] of bindings) if (binding.key.startsWith(scope)) bindings.delete(form);
    },
  };
}
