import { assert } from "./assertions.mjs";

export function testSubStoreCardLayout() {
  const table = document.querySelector('[aria-label="同步节点"]');
  assert.ok(table, "同步节点表未加载");
  assert.ok(document.documentElement.scrollWidth <= innerWidth + 1, "Sub-Store 页面横向溢出");
  for (const row of table.querySelectorAll("tbody tr")) {
    const frame = row.getBoundingClientRect();
    assert.ok(row.cells[1].getBoundingClientRect().width >= 240, "长节点名称被其他列挤压");
    for (const control of row.querySelectorAll("input:not([type=checkbox]),select,button.a-button")) {
      const box = control.getBoundingClientRect();
      assert.ok(box.width >= 48, "同步参数被挤压");
      assert.ok(box.left >= frame.left - 1 && box.right <= frame.right + 1, "同步参数溢出所属来源");
      assert.ok(box.height >= (matchMedia("(pointer:coarse)").matches ? 44 : 36), `同步操作尺寸不足：${control.tagName}/${box.height}/${matchMedia("(pointer:coarse)").matches}`);
    }
  }
}
