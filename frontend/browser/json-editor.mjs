import { assert, delay } from './assertions.mjs';

export async function testJSONEditor({ change, target, editor, settled, button }) {
  for (const engine of ['xray', 'sing-box', 'ss-rust']) {
    change(target().elements.engine, engine); await settled();
    const input = editor(), host = input.closest('.a-source-json'), original = input.value;
    assert.ok(host, `${engine}: missing JSON editor`);
    const paint = host.querySelector('.a-source-highlight'), gutter = host.querySelector('.a-source-lines');
    const check = () => assert.equal(paint.textContent, input.value + '\n', `${engine}: display differs from submitted text`);
    check();
    assert.ok(paint.querySelector('.a-json-key') && paint.querySelector('.a-json-string'), `${engine}: missing syntax colors`);
    assert.ok(gutter.textContent.startsWith('1\n2\n'), `${engine}: missing actual line numbers`);
    assert.equal(paint.getAttribute('aria-hidden'), 'true', 'visual text duplicated in accessibility tree');
    assert.equal(input.value, input.defaultValue, `${engine}: initial formatting produced a draft`);
    const content = '{\n  "large": 9007199254740993,\n  "enabled": true,\n  "path": "' + 'long-path/'.repeat(180) + '",\n  "unsafe": "</span><img src=x onerror=bad()>"' + ',\n  "nested": [\n' + Array.from({length:120}, (_, i) => `    ${i}`).join(',\n') + '\n  ]\n}\n';
    input.value = content; input.dispatchEvent(new Event('input', {bubbles:true}));
    input.focus(); input.setSelectionRange(4, 12); check();
    assert.ok(paint.querySelector('.a-json-number') && paint.querySelector('.a-json-literal'), 'number/bool highlighting missing');
    assert.equal(host.querySelector('img'), null, 'source was interpreted as HTML');
    input.scrollTop = 240; input.dispatchEvent(new Event('scroll')); await delay(20);
    check(); assert.ok(Math.abs(paint.scrollTop - input.scrollTop) <= 1 && Math.abs(gutter.scrollTop - input.scrollTop) <= 1, 'vertical scroll lost line alignment');
    input.scrollLeft = 180; input.dispatchEvent(new Event('scroll')); await delay(20);
    assert.ok(Math.abs(paint.scrollLeft - input.scrollLeft) <= 1, 'horizontal scroll lost text alignment');
    assert.equal(input.selectionStart, 4); assert.equal(input.selectionEnd, 12);
    assert.equal(input.value, content, 'highlighting changed precision or text');
    button('重置修改').click(); assert.equal(input.value, original); check();
    assert.ok(button('重置修改').disabled, 'reset did not restore baseline');
    assert.ok(document.documentElement.scrollWidth <= innerWidth + 1, 'long source overflowed page');
  }
  change(target().elements.engine, 'mihomo'); await settled();
  assert.ok(!editor().closest('.a-source-json'), 'JSON treatment leaked into YAML');
  change(target().elements.engine, 'xray'); await settled();
}
