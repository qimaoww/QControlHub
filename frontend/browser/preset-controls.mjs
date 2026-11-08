import { assert, waitFor, createConfigFixture } from "./config-fixture.mjs";

export async function createPresetEditor(engine, options) {
  const fixture = await createConfigFixture(engine, options);
  fixture.confirm = true;
  fixture.taskStatus = "succeeded";
  fixture.select("second");
  const form = () => document.querySelector("dialog[open] form:has([name=protocol])");
  const control = name => form()?.elements.namedItem(name);
  const edit = (name, value) => {
    const element = control(name);
    assert(element, `missing ${engine}/${options.protocol} field ${name}`);
    if (element.type === "checkbox") element.checked = Boolean(value);
    else element.value = value;
    element.dispatchEvent(new Event("input", {bubbles:true}));
    element.dispatchEvent(new Event("change", {bubbles:true}));
  };
  const open = async (operation = "modify") => {
    fixture.click(operation);
    await waitFor(()=>form()?.elements.namedItem("port"), "preset editor did not open");
    if (operation === "add" && control("protocol").value !== options.protocol) {
      edit("protocol", options.protocol);
      await waitFor(()=>form()?.querySelector('button[type=submit]')?.disabled === false, "selected preset did not load");
    }
  };
  const writes = () => fixture.writes.filter(write=>write.path.endsWith("/server-inbounds"));
  const save = async (intent="deploy") => {
    const count=fixture.writes.length+1;
    await waitFor(()=>!control("panel_certificate_id")?.validationMessage.includes("正在加载证书"), "selected certificate did not finish loading");
    assert(form().checkValidity(), `invalid preset fields: ${[...form().elements].filter(control=>control.willValidate&&!control.validity.valid).map(control=>`${control.name}: ${control.validationMessage}`).join('; ')}`);
    fixture.submit(intent);
    try { await fixture.settle(count); }
    catch (error) { throw new Error(`${error.message}; feedback=${[...document.querySelectorAll('dialog[open] .a-notice')].map(e=>e.textContent).join(';')}; confirmations=${JSON.stringify(fixture.confirmations)}; calls=${fixture.calls.slice(-5).map(c=>c.path).join(',')}`); }
    const saved=writes().at(-1);
    fixture.select(saved.input.tag);
    await open();
    return saved.input;
  };
  return Object.assign(fixture,{form,control,edit,open,save,submissions:writes});
}
