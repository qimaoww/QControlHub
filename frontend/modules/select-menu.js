// Keep the native control as the form value, validation and focus owner. The
// revealed options reuse the configuration menu and its theme states.
let installed = false, current, suggestion, committing = false, serial = 0;
const dropdown = node => node?.matches?.('select:not([multiple])') && node.size <= 1;
const enabled = option => !option.disabled && !option.closest('optgroup[disabled]');
const visible = option => !option.hidden && !option.closest('optgroup[hidden]');
const suggested = node => suggestion?.input === node;
const restore = (control, attributes) => {
  for (const [name, value] of attributes) {
    if (value == null) control.removeAttribute(name);
    else control.setAttribute(name, value);
  }
};
function releaseSuggestion() {
  if (!suggestion) return;
  const { input, attributes, watch } = suggestion;
  suggestion = null;
  watch.disconnect();
  restore(input, attributes);
}
function prepareSuggestion(input) {
  if (suggested(input)) return;
  releaseSuggestion();
  if (!input?.matches?.('input[list]:not(:disabled):not([readonly])') || !input.list) return;
  const list = input.list;
  const attributes = ['list', 'role', 'aria-autocomplete', 'aria-haspopup', 'aria-expanded'].map(name => [name, input.getAttribute(name)]);
  input.removeAttribute('list');
  input.setAttribute('role', 'combobox');
  input.setAttribute('aria-autocomplete', 'list');
  input.setAttribute('aria-haspopup', 'listbox');
  input.setAttribute('aria-expanded', 'false');
  const watch = new MutationObserver(() => {
    if (!input.isConnected || input.matches(':disabled,[readonly]')) { close(); releaseSuggestion(); }
  });
  suggestion = { input, list, attributes, watch };
  watch.observe(document.body, { childList: true, subtree: true, attributes: true, attributeFilter: ['disabled', 'readonly'] });
}

export function preserveSelectMenuAttribute(control, name) {
  return current?.select === control && ['aria-expanded', 'aria-controls', 'aria-activedescendant'].includes(name) ||
    suggested(control) && ['list', 'role', 'aria-autocomplete', 'aria-haspopup', 'aria-expanded'].includes(name);
}

function close() {
  if (!current) return;
  const { select, popup, attributes, watch } = current;
  current = null;
  watch.disconnect();
  popup.remove();
  restore(select, attributes);
}
function activate(index) {
  if (!current) return;
  const { select, popup, options } = current;
  const button = popup.querySelector(`[data-select-index="${index}"]`);
  if (!button || !enabled(options[index])) return;
  current.active = index;
  popup.querySelector('[data-active]')?.removeAttribute('data-active');
  button.dataset.active = '';
  select.setAttribute('aria-activedescendant', button.id);
  button.scrollIntoView({ block: 'nearest', inline: 'nearest', behavior: 'instant' });
}
function commit(index) {
  if (!current) return;
  const { select, options } = current;
  if (!options[index] || !enabled(options[index]) || select.matches(':disabled')) return close();
  const input = suggested(select);
  const changed = input ? select.value !== options[index].value : select.selectedIndex !== index;
  const value = options[index].value;
  close();
  select.focus({ preventScroll: true });
  if (!changed) return;
  if (input) select.value = value; else select.selectedIndex = index;
  committing = true;
  try {
    select.dispatchEvent(new Event('input', { bubbles: true }));
    select.dispatchEvent(new Event('change', { bubbles: true }));
  } finally { committing = false; }
}
function open(select) {
  close();
  const input = suggested(select);
  if ((!dropdown(select) && !input) || select.matches(':disabled')) return;
  const options = [...(input ? suggestion.list.options : select.options)].filter(option => !input || option.value.toLocaleLowerCase().includes(select.value.toLocaleLowerCase()));
  if (!options.some(visible)) return;
  const popup = document.createElement('div');
  popup.id = `select-menu-${++serial}`;
  popup.className = 'config-inbound-menu-items';
  popup.setAttribute('role', 'listbox');
  popup.setAttribute('popover', 'manual');
  popup.setAttribute('aria-label', select.getAttribute('aria-label') || [...select.labels || []].map(label => label.textContent.trim()).join(' ') || select.name);
  let group, optionHost = popup;
  options.forEach((option, index) => {
    if (!visible(option)) return;
    const parent = option.parentElement;
    if (parent.tagName === 'OPTGROUP' && parent !== group) {
      group = parent;
      optionHost = document.createElement('div');
      optionHost.setAttribute('role', 'group');
      optionHost.setAttribute('aria-label', parent.label);
      popup.append(optionHost);
      const heading = document.createElement('div');
      heading.className = 'notice-message';
      heading.textContent = parent.label;
      heading.setAttribute('aria-hidden', 'true');
      optionHost.append(heading);
    } else if (parent.tagName !== 'OPTGROUP') { group = null; optionHost = popup; }
    const button = document.createElement('button');
    button.type = 'button';
    button.tabIndex = -1;
    button.className = 'notice-message';
    button.id = `${popup.id}-${index}`;
    button.dataset.selectIndex = String(index);
    button.setAttribute('role', 'option');
    button.setAttribute('aria-selected', String(input ? select.value === option.value : option.selected));
    button.disabled = !enabled(option);
    button.textContent = option.label || option.value;
    optionHost.append(button);
  });
  const attributes = ['aria-expanded', 'aria-controls', 'aria-activedescendant'].map(name => [name, select.getAttribute(name)]);
  select.setAttribute('aria-expanded', 'true');
  select.setAttribute('aria-controls', popup.id);
  (select.closest('dialog') || document.body).append(popup);
  const watch = new MutationObserver(records => {
    if (!select.isConnected || select.matches(':disabled') || select.getAttribute('aria-expanded') !== 'true' ||
        records.some(record => select.contains(record.target) &&
          (record.target !== select || record.type === 'childList' || record.attributeName !== 'aria-expanded'))) close();
  });
  current = { select, popup, options, attributes, watch, active: -1, search: '', searchedAt: 0 };
  select.focus({ preventScroll: true });
  popup.showPopover();
  if (!input) activate(select.selectedIndex);
  current.anchor = select.getBoundingClientRect();
  watch.observe(document.body, { childList: true, subtree: true, attributes: true,
    attributeFilter: ['disabled', 'hidden', 'label', 'value', 'selected', 'aria-expanded'] });
  popup.addEventListener('pointerdown', event => { if (event.pointerType !== 'touch') event.preventDefault(); });
  popup.addEventListener('click', event => {
    const button = event.target.closest('[data-select-index]');
    if (button && !button.disabled) commit(Number(button.dataset.selectIndex));
  });
}

export function installSelectMenus() {
  if (installed || !HTMLElement.prototype.showPopover || !CSS.supports('top', 'anchor(bottom)') ||
      !CSS.supports('position-try-fallbacks', 'flip-block') || !CSS.supports('width', 'anchor-size(width)')) return;
  installed = true;
  document.addEventListener('pointerdown', event => {
    if (event.target.matches?.('input[list]')) prepareSuggestion(event.target);
    if (dropdown(event.target) && !event.target.matches(':disabled')) {
      event.preventDefault();
      event.target.focus({ preventScroll: true });
    } else if (current && !current.popup.contains(event.target)) close();
  }, true);
  document.addEventListener('click', event => {
    if ((!dropdown(event.target) && !suggested(event.target)) || event.target.matches(':disabled')) return;
    event.preventDefault();
    if (current?.select === event.target) close(); else open(event.target);
  }, true);
  document.addEventListener('keydown', event => {
    const select = current?.popup.contains(event.target) ? current.select : event.target;
    const input = suggested(select);
    if ((!dropdown(select) && !input) || select.matches(':disabled') || event.ctrlKey || event.metaKey || event.isComposing) return;
    if (input && !['ArrowDown', 'ArrowUp', 'Enter', 'Escape', 'Tab'].includes(event.key)) return;
    if (input && event.key === 'Enter' && (!current || current.active < 0)) { close(); return; }
    if (event.key === 'Escape' && current) {
      event.preventDefault();
      event.stopPropagation();
      close();
      select.focus({ preventScroll: true });
      return;
    }
    if (event.key === 'ArrowUp' && event.altKey && current) {
      event.preventDefault();
      if (current.active >= 0) commit(current.active); else close();
      return;
    }
    if (event.key === 'Tab') {
      if (current) { if (current.active >= 0) commit(current.active); else close(); }
      return;
    }
    const navigation = ['ArrowDown', 'ArrowUp', 'Home', 'End'];
    const spaceSearch = event.key === ' ' && current?.search && performance.now() - current.searchedAt <= 700;
    if (!spaceSearch && (['Enter', ' ', 'F4'].includes(event.key) || navigation.includes(event.key))) {
      event.preventDefault();
      if (current && (['Enter', 'F4'].includes(event.key) || event.key === ' ' && !current.search)) {
        if (current.active >= 0) commit(current.active); else close();
        return;
      }
      if (!current) { open(select); if (!current || !navigation.includes(event.key)) return; }
      current.search = '';
      const indices = current.options.map((option, index) => enabled(option) && visible(option) ? index : -1).filter(index => index >= 0);
      const offset = indices.indexOf(current.active);
      const next = event.key === 'Home' ? 0 : event.key === 'End' ? indices.length - 1 :
        Math.max(0, Math.min(indices.length - 1, offset < 0 ? (event.key === 'ArrowUp' ? indices.length - 1 : 0) : offset + (event.key === 'ArrowUp' ? -1 : 1)));
      activate(indices[next]);
      return;
    }
    if (event.key.length !== 1 || event.altKey) return;
    event.preventDefault();
    if (!current) open(select);
    if (!current) return;
    const now = performance.now();
    current.search = now - current.searchedAt > 700 ? event.key : current.search + event.key;
    current.searchedAt = now;
    const query = current.search.toLocaleLowerCase();
    const repeat = [...query].every(char => char === query[0]);
    const start = repeat ? current.active + 1 : Math.max(0, current.active);
    for (let offset = 0; offset < current.options.length; offset++) {
      const index = (start + offset) % current.options.length, option = current.options[index];
      if (enabled(option) && visible(option) && option.label.toLocaleLowerCase().startsWith(repeat ? query[0] : query)) { activate(index); break; }
    }
  }, true);
  document.addEventListener('focusin', event => {
    if (current?.popup.contains(event.target)) return;
    if (current && event.target !== current.select) close();
    if (!suggested(event.target)) prepareSuggestion(event.target);
  });
  document.addEventListener('input', event => { if (!committing && !event.isComposing && suggested(event.target)) open(event.target); });
  document.addEventListener('change', event => { if (event.target === current?.select) close(); });
  document.addEventListener('reset', close, true);
  document.addEventListener('beforetoggle', event => {
    if (event.target.tagName !== 'DIALOG') return;
    close();
    if (event.newState === 'closed' && event.target.contains(suggestion?.input)) releaseSuggestion();
  }, true);
  document.addEventListener('scroll', event => {
    if (!current || current.popup.contains(event.target)) return;
    const bounds = current.select.getBoundingClientRect();
    if (Math.abs(bounds.left - current.anchor.left) > 1 || Math.abs(bounds.top - current.anchor.top) > 1) close();
  }, true);
  window.addEventListener('resize', close);
  window.visualViewport?.addEventListener('resize', close);
  window.addEventListener('hashchange', () => { close(); releaseSuggestion(); });
  window.addEventListener('blur', close);
}
