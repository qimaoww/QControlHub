// Bind only explicitly inventoried query controls. Editing forms never use
// this helper. Restore all fields together before invoking the query callback.
export function bindPreferenceFields(ctx, page, roots, { restore = () => {}, explicit = [], normalize = value => value, valid = () => true } = {}) {
  const preferences = ctx.preferences;
  if (!preferences) return { ready: Promise.resolve(), save() {} };
  const fields = roots.flatMap(root => root.matches?.('input,select') ? [root] : [...root.querySelectorAll('input,select')]).filter(input => input.name);
  const touched = new Set(explicit), initial = new Map(fields.map(input => [input.name, read(input)]));
  let restoring = false;
  function read(input) { return input.type === 'checkbox' ? input.checked : input.value; }
  function save(values) { Object.keys(values).forEach(key => touched.add(key)); preferences.patch(page, values); }
  for (const input of fields) {
    if (input.type === 'search' || input.type === 'text') input.maxLength = input.maxLength > 0 ? input.maxLength : 200;
    for (const type of ['input', 'change']) input.addEventListener(type, () => {
      if (restoring || !ctx.current()) return;
      touched.add(input.name);
      if (!input.checkValidity() || !valid(input.name, read(input))) return;
      save({ [input.name]: read(input) });
    }, { capture: true, signal: ctx.signal });
  }
  const ready = preferences.ready().then(() => {
    if (!ctx.current()) return;
    const saved = normalize(preferences.get(page));
    let changed = false;
    restoring = true;
    try {
      for (const input of fields) {
        const key = input.name;
        if (touched.has(key) || !Object.hasOwn(saved, key)) continue;
        const value = saved[key];
        if (input.tagName === 'SELECT' && ![...input.options].some(option => option.value === value)) {
          if (value) input.append(new Option('所选目标', value));
        }
        if (input.type === 'checkbox') input.checked = value; else input.value = value;
        if (read(input) !== initial.get(key)) changed = true;
      }
      if (changed) restore(saved);
    } finally { restoring = false; }
  });
  return { ready, save };
}
