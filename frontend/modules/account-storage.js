// Browser preferences contain node IDs and log search terms. Never inherit
// another account's values on a shared browser, including legacy unscoped keys.
let account = "signed-out";

export function setStorageAccount(session) {
  account = session
    ? session.user_id || session.workspace_id || `${session.role || "user"}:${session.username || "token"}`
    : "signed-out";
}

export const accountStorage = {
  getItem(key) {
    return globalThis.localStorage?.getItem(`${key}:account:${encodeURIComponent(account)}`) ?? null;
  },
  setItem(key, value) {
    globalThis.localStorage?.setItem(`${key}:account:${encodeURIComponent(account)}`, value);
  },
};
