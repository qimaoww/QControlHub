import { node } from './ui.js';

// The node and panel resource displays share the theme's existing meter.
export function metricTrack(value, available, thresholds = true) {
  const percent = Math.min(100, Math.max(0, Number(value) || 0));
  const fill = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  fill.setAttribute('width', `${available ? percent : 0}%`); fill.setAttribute('height', '100%');
  fill.setAttribute('class', thresholds && percent > 90 ? 'bad' : thresholds && percent > 75 ? 'warn' : '');
  const rect = document.createElementNS(fill.namespaceURI, 'rect');
  rect.setAttribute('width', '100%'); rect.setAttribute('height', '100%'); fill.append(rect);
  return node('div', { className: 'a-probe-track', 'aria-hidden': 'true' }, fill);
}
