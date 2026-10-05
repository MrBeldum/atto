// What a tab keeps across reloads: the thread it had open (this tab only)
// and the unsent draft. Storage can be missing or refuse (private mode):
// then nothing is kept.

function get(s: () => Storage, key: string): string {
  try {
    return s().getItem(key) || "";
  } catch {
    return "";
  }
}

function set(s: () => Storage, key: string, value: string) {
  try {
    if (value) s().setItem(key, value);
    else s().removeItem(key);
  } catch {
    /* not kept */
  }
}

const session = () => sessionStorage;
const local = () => localStorage;

export const loadThreadId = () => get(session, "atto-thread");
export const saveThreadId = (id: string) => set(session, "atto-thread", id);

export const loadDraft = () => get(local, "atto-draft");
export const saveDraft = (text: string) => set(local, "atto-draft", text);
