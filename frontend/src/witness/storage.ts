/**
 * Storage for the witness log. localStorage when it works; if it is missing, blocked (private
 * mode, disabled site data) or full, everything degrades to memory and `warning` says so. The page
 * shows the warning, because entries in memory are lost when the tab closes.
 */
export interface KeyValueStore {
  get(key: string): string | null;
  set(key: string, value: string): void;
  remove(key: string): void;
  /** False once the store has fallen back to memory. */
  readonly persistent: boolean;
  /** Why the store is not persistent, in plain words. Null when it is. */
  readonly warning: string | null;
}

export function createMemoryStore(warning: string | null = null): KeyValueStore {
  const m = new Map<string, string>();
  return {
    get: (k) => m.get(k) ?? null,
    set: (k, v) => void m.set(k, v),
    remove: (k) => void m.delete(k),
    persistent: warning === null ? true : false,
    warning,
  };
}

const PROBE = 'fourhour.storage-probe';

export function createSafeStore(getStorage: () => Storage | null = () => window.localStorage): KeyValueStore {
  let backing: Storage | null = null;
  let warning: string | null = null;
  try {
    backing = getStorage();
    if (!backing) throw new Error('localStorage is not available');
    backing.setItem(PROBE, '1');
    backing.removeItem(PROBE);
  } catch (e: unknown) {
    backing = null;
    warning = `This browser will not let the page save data (${e instanceof Error ? e.message : String(e)}). Entries are kept in memory only and are lost if this tab closes before they upload.`;
  }
  const memory = new Map<string, string>();

  const fallBack = (e: unknown, key: string, value: string | null) => {
    // Copy what we can into memory, then stop using the broken storage.
    if (backing) {
      try {
        for (let i = 0; i < backing.length; i++) {
          const k = backing.key(i);
          if (k) memory.set(k, backing.getItem(k) ?? '');
        }
      } catch {
        // nothing more to copy
      }
    }
    backing = null;
    warning = `Saving to this browser failed (${e instanceof Error ? e.message : String(e)}). New entries are kept in memory only until they upload.`;
    if (value === null) memory.delete(key);
    else memory.set(key, value);
  };

  return {
    get(key) {
      if (backing) {
        try {
          return backing.getItem(key);
        } catch (e: unknown) {
          fallBack(e, key, memory.get(key) ?? null);
        }
      }
      return memory.get(key) ?? null;
    },
    set(key, value) {
      if (backing) {
        try {
          backing.setItem(key, value);
          return;
        } catch (e: unknown) {
          fallBack(e, key, value);
          return;
        }
      }
      memory.set(key, value);
    },
    remove(key) {
      if (backing) {
        try {
          backing.removeItem(key);
          return;
        } catch (e: unknown) {
          fallBack(e, key, null);
          return;
        }
      }
      memory.delete(key);
    },
    get persistent() {
      return backing !== null;
    },
    get warning() {
      return warning;
    },
  };
}
