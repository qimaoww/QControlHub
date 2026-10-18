import { assert, pause, waitFor, createConfigFixture as fixture } from "./config-fixture.mjs";

const input = () => document.querySelector('#live-config-form [data-code-input]');
const emit = () => input().dispatchEvent(new Event('input', {bubbles:true}));
const submit = intent => document.querySelector(`[data-live-intent="${intent}"]`).click();
const result = () => document.querySelector('[data-preset-status]')?.textContent || '';

export async function testConfigWorkflowRuntime() {
  // The source editor submits every file, preserving unrecognised values and
  // exact numeric tokens. A saved version must also become the next baseline.
  const source = await fixture('xray', {source:true});
  const common = JSON.parse(input().value);
  input().value = JSON.stringify({...common, extension:{keep:true}}, null, 2).replace('"keep": true', '"keep": 900719925474099312345'); emit();
  document.querySelectorAll('[data-config-file]')[1].click();
  input().value += '\n'; emit();
  assert(document.querySelector('[data-live-version]').textContent.includes('未保存修改'), 'saved version hides unsaved file edits');
  submit('save');
  await waitFor(()=>source.writes.length === 1 && !document.querySelector('#live-config-form[data-saving]'), 'source draft save did not finish');
  assert(source.saved().content.includes('900719925474099312345') && JSON.parse(source.saved().content).inbounds.length === 2, 'save corrupted unknown fields, precision or other inbounds');
  assert(!document.querySelector('.config-draft-summary[data-dirty="1"]') && result().includes('尚未部署'), 'save retained a dirty baseline or claimed deployment');
  source.taskStatus = 'succeeded';
  submit('validate');
  await waitFor(()=>result().includes('校验成功'), 'source validation result missing');
  assert(result().includes('尚未部署') && source.taskRequests.at(-1).expected_config_version === source.saved().version, 'validation claimed deployment or used an old version');
  source.dispose();

  // Both files survive a failed refresh. Confirmed refresh discards them only
  // once the replacement workspace and its fresh Agent read have succeeded.
  const refresh = await fixture('xray', {source:true});
  input().value += '\n'; emit();
  const commonDraft = input().value;
  document.querySelectorAll('[data-config-file]')[1].click();
  input().value += '\n'; emit();
  const selectedDraft = input().value;
  refresh.confirm = true;
  refresh.workspaceGate = Promise.reject(Object.assign(new Error('refresh unavailable'), {status:503}));
  // Attach a rejection consumer before the click starts its asynchronous read.
  refresh.workspaceGate.catch(()=>{});
  document.querySelector('[data-config-refresh]').click();
  await waitFor(()=>refresh.notices.some(message=>message.includes('当前内容已保留')), 'failed refresh did not report retained drafts');
  assert(input().value === selectedDraft, 'failed refresh lost selected draft');
  document.querySelectorAll('[data-config-file]')[0].click();
  assert(input().value === commonDraft, 'failed refresh lost another file');
  refresh.workspaceGate = null;
  document.querySelector('[data-config-refresh]').click();
  await waitFor(()=>!document.querySelector('.config-draft-summary[data-dirty="1"]') && !document.querySelector('[data-config-refreshing]'), 'confirmed refresh kept abandoned file drafts');
  refresh.dispose();

  // A source switch freezes the old editor even before its response arrives;
  // synthetic submit events must obey the same context guard as real clicks.
  const owner = await fixture('xray', {source:true, owner:true, import:true});
  owner.confirm = true;
  let release;
  owner.workspaceGate = new Promise(resolve=>{release=resolve;});
  document.querySelector('[data-live-source="managed"]').click();
  await waitFor(()=>document.querySelector('[data-config-switching]'), 'source switch did not freeze old workspace');
  const old = document.querySelector('#live-config-form');
  old.dispatchEvent(new SubmitEvent('submit', {bubbles:true, cancelable:true}));
  assert(!owner.writes.length, 'old source editor submitted during source switch');
  release();
  await waitFor(()=>owner.state.data.liveConfigSource === 'managed' && !document.querySelector('[data-config-switching]'), 'owner source switch failed');
  assert(!input().readOnly && document.querySelector('[data-live-source="managed"]').getAttribute('aria-pressed') === 'true', 'source permissions and selected state did not update');
  const pending = [];
  owner.workspaceGate = () => new Promise(resolve=>pending.push(resolve));
  document.querySelector('[data-live-source="personal"]').click();
  await waitFor(()=>pending.length === 1, 'first source race did not start');
  document.querySelector('[data-live-source="import"]').click();
  await waitFor(()=>pending.length === 2, 'latest source race did not start');
  pending[1]();
  await waitFor(()=>input().readOnly && owner.state.data.liveConfigSource === 'import', 'latest source response did not take effect');
  pending[0](); await pause();
  assert(input().readOnly && document.querySelector('[data-live-source="import"]').getAttribute('aria-pressed') === 'true', 'old source response replaced the latest source');
  owner.dispose();

  const shared = await fixture('xray', {source:true, shared:true});
  assert(document.querySelectorAll('[data-live-intent="deploy"]').length === 1 && !document.querySelector('[data-live-source="managed"]'), 'shared user has duplicate deploy or host source access');
  shared.confirm = true;
  shared.taskStatus = 'failed';
  submit('deploy');
  await waitFor(()=>result().includes('部署失败'), 'failed deployment result missing');
  assert(input() && shared.saved().version === 2, 'failed deployment discarded saved configuration');
  shared.taskStatus = 'succeeded';
  submit('deploy');
  await waitFor(()=>result().includes('部署成功'), 'deployment retry result missing');
  assert(shared.taskRequests.every(task=>task.agent_id === 'node' && task.engine === 'xray'), 'deployment retry changed its target');
  shared.dispose();

  const conflict = await fixture('xray', {source:true});
  conflict.sourceFailure = {status:409};
  input().value += '\n'; emit();
  const kept = input().value;
  submit('save');
  await waitFor(()=>document.querySelector('[data-live-save-status][role="alert"]'), 'source conflict missing');
  assert(input().value === kept && document.querySelector('[data-live-intent="save"]').disabled, 'source conflict lost draft or allowed stale retry');
  conflict.dispose();

  // Logging out during an in-flight save cannot enqueue a task or cache a
  // configuration in the next account after the write response returns.
  for (const boundary of ['save', 'task']) {
    const account = await fixture('xray', {source:true, shared:true});
    account.confirm = true;
    account[boundary === 'save' ? 'gate' : 'taskGate'] = new Promise(resolve=>{release=resolve;});
    submit('deploy');
    await waitFor(()=>boundary === 'task' ? account.taskRequests?.length : document.querySelector('#live-config-form[data-saving="1"]'), 'account submission did not start');
    await pause();
    account.dispose();
    release(); await pause();
    assert((account.taskRequests?.length || 0) === (boundary === 'task' ? 1 : 0) &&
      !account.state.data.liveSources && !account.state.data.pendingDeployTasks,
      'late account response created a task or leaked configuration/deployment cache');
  }
}
