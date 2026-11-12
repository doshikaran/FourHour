/**
 * Keeps the witness key pair across reloads. A non-extractable CryptoKey cannot be written to
 * localStorage, but IndexedDB stores CryptoKey objects as they are, still non-extractable. If
 * IndexedDB is unavailable the pair lives in memory and the page warns that a reload means
 * registering a new device.
 */
export interface KeyStore {
  load(policyId: string): Promise<CryptoKeyPair | null>;
  save(policyId: string, pair: CryptoKeyPair): Promise<void>;
  remove(policyId: string): Promise<void>;
  readonly persistent: boolean;
}

export function createMemoryKeyStore(): KeyStore {
  const m = new Map<string, CryptoKeyPair>();
  return {
    load: async (id) => m.get(id) ?? null,
    save: async (id, pair) => void m.set(id, pair),
    remove: async (id) => void m.delete(id),
    persistent: false,
  };
}

const DB = 'fourhour-witness';
const STORE = 'keys';

function openDb(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(DB, 1);
    req.onupgradeneeded = () => req.result.createObjectStore(STORE);
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error ?? new Error('IndexedDB open failed'));
    req.onblocked = () => reject(new Error('IndexedDB is blocked'));
  });
}

function tx<T>(db: IDBDatabase, mode: IDBTransactionMode, fn: (s: IDBObjectStore) => IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    const t = db.transaction(STORE, mode);
    const req = fn(t.objectStore(STORE));
    t.oncomplete = () => resolve(req.result);
    t.onerror = () => reject(t.error ?? new Error('IndexedDB transaction failed'));
    t.onabort = () => reject(t.error ?? new Error('IndexedDB transaction aborted'));
  });
}

/** IndexedDB-backed key store; resolves to a memory store if IndexedDB cannot be opened. */
export async function createKeyStore(): Promise<KeyStore> {
  if (typeof indexedDB === 'undefined') return createMemoryKeyStore();
  let db: IDBDatabase;
  try {
    db = await openDb();
  } catch {
    return createMemoryKeyStore();
  }
  return {
    load: async (id) => {
      const v = await tx<unknown>(db, 'readonly', (s) => s.get(id));
      if (v && typeof v === 'object' && 'privateKey' in v && 'publicKey' in v) return v as CryptoKeyPair;
      return null;
    },
    save: async (id, pair) => void (await tx(db, 'readwrite', (s) => s.put({ privateKey: pair.privateKey, publicKey: pair.publicKey }, id))),
    remove: async (id) => void (await tx(db, 'readwrite', (s) => s.delete(id))),
    persistent: true,
  };
}
