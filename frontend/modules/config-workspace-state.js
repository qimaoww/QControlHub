import { onStorageAccountChange, storageAccount } from "./account-storage.js";
import { createConfigDocument } from "./config-document.js";

const storageKey = "qcontrolhub:config-workspace-drafts";
const identity = session => session?.user_id || session?.preference_id || session?.workspace_id || `${session?.role || "admin"}:${session?.username || "token"}`;
export const configWorkspaceKey = ({agentId, engine, sourceMode}) => JSON.stringify([agentId, engine, sourceMode]);

// One account-owned registry survives route renders. Draft persistence is
// limited to this browser tab and is cleared when its signed-in owner changes.
export function createConfigWorkspaceState({state}) {
  let data, account, records = new Map(), active, request = 0, retained = {};
  const clearStorage = () => { try { globalThis.sessionStorage?.removeItem(storageKey); } catch { /* Memory drafts remain usable. */ } };
  const syncAccount = () => {
    const next = identity(state.session);
    if (data === state.data && account === next) return;
    if (account && account !== next) clearStorage();
    data = state.data; account = next; records = new Map(); active = null; request++;
    retained = {};
    try {
      const saved = JSON.parse(globalThis.sessionStorage?.getItem(storageKey) || "null");
      if (saved?.account === account) retained = saved.workspaces || {};
      else if (saved) clearStorage();
    } catch { clearStorage(); }
  };
  const persist = () => {
    if (data !== state.data || account !== identity(state.session)) return;
    for (const [key, record] of records) {
      record.formEditor?.capture();
      const snapshot = record.document?.snapshot();
      if (record.document?.dirty() || record.formDrafts?.length) retained[key] = {formDrafts:record.formDrafts, baseline:record.document.baseline, version:record.saved?.version || 0, ...snapshot};
      else if (!record.conflict) delete retained[key];
    }
    try {
      if (Object.keys(retained).length) globalThis.sessionStorage?.setItem(storageKey, JSON.stringify({account, workspaces:retained}));
      else clearStorage();
    } catch { /* Storage denial/quota cannot interrupt editing. */ }
  };
  onStorageAccountChange(() => {
    if (storageAccount().id === "signed-out" || account && account !== identity(state.session)) {
      clearStorage(); records.clear(); retained = {}; active = null; request++;
    }
  });
  return {
    begin() {
      syncAccount(); persist();
      const ticket = ++request, owner = state.data, epoch = state.navigationEpoch, signal = state.routeSignal;
      return {current:() => ticket === request && owner === state.data && epoch === state.navigationEpoch && !signal?.aborted && state.route === "live-config"};
    },
    open(context) {
      syncAccount();
      const key = configWorkspaceKey(context), previous = records.get(key), draft = retained[key];
      const version = context.saved?.version || 0;
      const same = Boolean(previous) && previous?.saved?.version === context.saved?.version && previous?.document?.baseline === context.content;
      const conflict = Boolean(draft && (draft.version !== version || draft.baseline !== context.content));
      const document = same ? previous.document : createConfigDocument(context.engine, conflict ? draft.baseline : context.content,
        {inbounds:context.inbounds, selection:draft?.selection || previous?.document?.selected(), drafts:draft?.drafts, source:draft?.source, ids:draft?.ids});
      const record = {...previous, ...context, formDrafts:previous?.formDrafts || draft?.formDrafts || [], key, account, document, conflict, phase:context.content === undefined ? "loading" : "ready", operation:previous?.operation, submitting:false};
      records.set(key, record); active = record;
      document.subscribe(persist);
      document.updateInbounds(context.inbounds);
      persist();
      return record;
    },
    current:() => { syncAccount(); return active; },
    capture:persist,
    dirty:() => { syncAccount(); return Boolean(active?.document?.dirty() || active?.submitting || active?.formDrafts?.length || active?.formEditor?.dirty()); },
    commit(record, config) {
      if (data !== state.data || record.account !== identity(state.session)) return;
      record.saved = config; record.conflict = false; record.formDrafts = [];
      const snapshot = record.document?.snapshot();
      record.document = createConfigDocument(record.engine, config.content, {selection:snapshot?.selection, ids:snapshot?.ids, inbounds:record.inbounds});
      record.document.subscribe(persist);
      delete retained[record.key]; persist();
    },
    discard(record) {
      if (!record) return;
      delete retained[record.key]; records.delete(record.key);
      if (active === record) active = null;
      persist();
    },
    clear() { records.clear(); retained = {}; active = null; request++; clearStorage(); },
  };
}
