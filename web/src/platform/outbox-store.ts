import type {OutboxEntry, OutboxStore} from '../../../packages/client-core/src/outbox.ts';

/** The outbox's entries in IndexedDB, one record per entry, so two tabs never overwrite each other's. */
export class IndexedDBOutboxStore implements OutboxStore {
  private database: Promise<IDBDatabase> | undefined;
  constructor(private readonly factory: IDBFactory = globalThis.indexedDB, private readonly name = 'portico.outbox') {}

  private open(): Promise<IDBDatabase> {
    this.database ??= new Promise<IDBDatabase>((resolve, reject) => {
      const request = this.factory.open(this.name, 1);
      request.onupgradeneeded = () => { request.result.createObjectStore('entries', {keyPath: 'id'}); };
      request.onerror = () => reject(request.error ?? new Error('This browser could not open its storage.'));
      request.onblocked = () => reject(new Error('This browser could not open its storage.'));
      request.onsuccess = () => { const db = request.result; db.onversionchange = () => { db.close(); this.database = undefined; }; resolve(db); };
    });
    this.database.catch(() => { this.database = undefined; });
    return this.database;
  }
  private async run<T>(mode: IDBTransactionMode, work: (store: IDBObjectStore) => IDBRequest<T>): Promise<T> {
    const db = await this.open();
    return new Promise<T>((resolve, reject) => {
      // A write is done only once it is on disk: the entry must survive the tab closing right after.
      const tx = db.transaction('entries', mode, mode === 'readwrite' ? {durability: 'strict'} : undefined);
      const request = work(tx.objectStore('entries'));
      tx.oncomplete = () => resolve(request.result);
      tx.onabort = tx.onerror = () => reject(tx.error ?? request.error ?? new Error('This browser could not save to its storage.'));
    });
  }
  list(): Promise<OutboxEntry[]> { return this.run('readonly', store => store.getAll() as IDBRequest<OutboxEntry[]>); }
  async put(entry: OutboxEntry): Promise<void> { await this.run('readwrite', store => store.put(entry)); }
  async delete(id: string): Promise<void> { await this.run('readwrite', store => store.delete(id)); }
}
