export const tcpButton = (root, text) => [...root.querySelectorAll('button')].find(button => button.textContent === text || button.textContent.startsWith(`${text} ·`));
export const tcpClose = dialog => dialog.querySelector('[aria-label="关闭"]').click();
export const tcpConfirmation = () => [...document.querySelectorAll('dialog[open]')].find(dialog => dialog.querySelector('h2')?.textContent === '自定义 TCP 调优' || dialog.querySelector('h2')?.textContent === '启用 BBR' || dialog.querySelector('h2')?.textContent === '切换 CUBIC' || dialog.querySelector('h2')?.textContent === '清空选择') || null;
export const tcpAccept = dialog => dialog.querySelector('button.primary').click();
export const tcpCancel = dialog => tcpButton(dialog, '取消').click();
export const tcpField = (dialog, key) => dialog.querySelector('form').elements.namedItem(key);
export const tcpSelected = (dialog, key) => tcpField(dialog, `select:${key}`);
export const tcpError = dialog => [...dialog.querySelectorAll('.a-notice')].map(item => item.textContent).filter(Boolean).join(' · ');
export const tcpSelectedKeys = dialog => [...dialog.querySelectorAll('input[type=checkbox]:checked')].map(input => input.name.slice('select:'.length)).sort();
export async function tcpSettle() {
  const deadline = performance.now() + 7000;
  while (document.querySelector('#app[aria-busy=true]')) {
    if (performance.now() > deadline) throw new Error('TCP 提交未结束');
    await new Promise(resolve => setTimeout(resolve, 25));
  }
  for (const dialog of document.querySelectorAll('dialog[open]')) if (dialog.querySelector('h2')?.textContent === '任务详情') tcpClose(dialog);
}
