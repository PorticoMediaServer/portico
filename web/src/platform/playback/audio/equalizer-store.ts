import {eqFrequencies, flatEqualizer, parseEqualizer, type EqualizerSettings} from './equalizer';

export const deviceEqualizerDatabase = 'portico-web-equalizer-v1';

/** One EQ for this app on this device. Scalar settings and ten band rows commit atomically. */
export class EqualizerStore {
  private opened?: Promise<IDBDatabase>;
  constructor(private readonly factory: IDBFactory | undefined = globalThis.indexedDB, private readonly name = deviceEqualizerDatabase) {}
  private database(): Promise<IDBDatabase> {
    return this.opened ??= new Promise((resolve, reject) => {
      if (!this.factory) { reject(new Error('equalizer_storage_unavailable')); return; }
      const request = this.factory.open(this.name, 1);
      let abandoned = false;
      request.onupgradeneeded = () => {
        request.result.createObjectStore('settings', {keyPath: 'id'});
        request.result.createObjectStore('bands', {keyPath: 'band'});
      };
      request.onerror = () => reject(request.error);
      request.onblocked = () => { abandoned = true; reject(new Error('equalizer_storage_blocked')); };
      request.onsuccess = () => { if (abandoned) { request.result.close(); return; } request.result.onversionchange = () => request.result.close(); resolve(request.result); };
    });
  }
  async load(): Promise<EqualizerSettings> {
    const db = await this.database();
    return new Promise((resolve, reject) => {
      const tx = db.transaction(['settings', 'bands'], 'readonly');
      const settings = tx.objectStore('settings').get(1);
      const bands = eqFrequencies.map((_, band) => tx.objectStore('bands').get(band));
      tx.onerror = () => reject(tx.error); tx.onabort = () => reject(tx.error);
      tx.oncomplete = () => {
        try { resolve(settings.result ? parseEqualizer({enabled: settings.result.enabled, preampDb: settings.result.preampDb, bandsDb: bands.map(r => r.result?.gainDb)}) : flatEqualizer); }
        catch (error) { reject(error); }
      };
    });
  }
  private write(tx: IDBTransaction, value: EqualizerSettings): void {
    tx.objectStore('settings').put({id: 1, enabled: value.enabled, preampDb: value.preampDb});
    value.bandsDb.forEach((gainDb, band) => tx.objectStore('bands').put({band, gainDb}));
  }
  async save(settings: EqualizerSettings): Promise<EqualizerSettings> {
    const value = parseEqualizer(settings), db = await this.database();
    return new Promise((resolve, reject) => {
      const tx = db.transaction(['settings', 'bands'], 'readwrite');
      this.write(tx, value);
      tx.oncomplete = () => resolve(value); tx.onerror = () => reject(tx.error); tx.onabort = () => reject(tx.error);
    });
  }
  /** Reset the controls to flat, retaining the switch, in the same transaction. */
  async reset(): Promise<EqualizerSettings> {
    const db = await this.database();
    return new Promise((resolve, reject) => {
      const tx = db.transaction(['settings', 'bands'], 'readwrite');
      const settings = tx.objectStore('settings').get(1);
      let value = flatEqualizer;
      settings.onsuccess = () => {
        try { value = parseEqualizer({...flatEqualizer, enabled: settings.result ? settings.result.enabled : false}); this.write(tx, value); }
        catch (error) { reject(error); tx.abort(); }
      };
      tx.oncomplete = () => resolve(value); tx.onerror = () => reject(tx.error); tx.onabort = () => reject(tx.error);
    });
  }
  async close(): Promise<void> { const pending = this.opened; this.opened = undefined; if (pending) (await pending.catch(() => undefined))?.close(); }
}

const listeners = new Set<(settings: EqualizerSettings) => void>();
let pending: Promise<unknown> = Promise.resolve();

/** Settings and the player's shortcut share this facade; no viewer or media identity is stored. */
export async function loadDeviceEqualizer(): Promise<EqualizerSettings> {
  await pending;
  const store = new EqualizerStore();
  try { return await store.load(); }
  finally { await store.close(); }
}

function changeDeviceEqualizer(change: (store: EqualizerStore) => Promise<EqualizerSettings>): Promise<EqualizerSettings> {
  const next = pending.then(async () => {
    const store = new EqualizerStore();
    let value: EqualizerSettings;
    try { value = await change(store); }
    finally { await store.close(); }
    // Storage is committed before an engine sees it. Failed engines keep truthful graph facts;
    // the saved intent remains available for a later supported playback attempt.
    const errors: unknown[] = [];
    for (const listener of [...listeners]) { try { listener(value); } catch (error) { errors.push(error); } }
    if (errors.length) throw errors[0];
    return value;
  });
  pending = next.catch(() => {});
  return next;
}

export function saveDeviceEqualizer(settings: EqualizerSettings): Promise<EqualizerSettings> {
  const value = parseEqualizer(settings);
  return changeDeviceEqualizer(store => store.save(value));
}
export function resetDeviceEqualizer(): Promise<EqualizerSettings> { return changeDeviceEqualizer(store => store.reset()); }
export function subscribeDeviceEqualizer(listener: (settings: EqualizerSettings) => void): () => void {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
}
