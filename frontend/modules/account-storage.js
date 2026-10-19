// Browser preferences contain node IDs and log search terms. Never inherit
// another account's values on a shared browser, including legacy unscoped keys.
let account = "signed-out", stable = false;
const listeners = new Set();
export const storageAccount = () => ({ id: account, stable });
export const onStorageAccountChange = callback => { listeners.add(callback); return () => listeners.delete(callback); };

export function setStorageAccount(session) {
  const previous = account;
  account = session
    ? session.user_id || session.workspace_id || session.preference_id || `${session.role || "user"}:${session.username || "token"}`
    : "signed-out";
  stable = Boolean(session?.user_id || session?.workspace_id || session?.preference_id);
  if (account !== previous) listeners.forEach(callback => callback());
}

export const accountStorage = {
  getItem(key) {
    return globalThis.localStorage?.getItem(`${key}:account:${encodeURIComponent(account)}`) ?? null;
  },
  setItem(key, value) {
    globalThis.localStorage?.setItem(`${key}:account:${encodeURIComponent(account)}`, value);
  },
};
