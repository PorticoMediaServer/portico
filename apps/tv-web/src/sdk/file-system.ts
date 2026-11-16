/**
 * `@amazon-devices/kepler-file-system` as the Fire TV bridge's record store (`storage.ts`) uses it,
 * in a TV browser: the app's own storage (`localStorage`), which only this app can read and which
 * is kept across restarts and updates and removed with the app, on both LG and Samsung sets.
 * A TV browser has no keychain; this is the same level of protection as the Fire TV app's
 * private folder. Each file is one key; a write is complete or absent, never half done.
 */
const PREFIX = 'portico.file:';

function store(): Storage {
  const storage = globalThis.localStorage;
  if (!storage) throw new Error('App storage is unavailable.');
  return storage;
}

export const KeplerFileSystem = {
  exists: (path: string): Promise<boolean> => Promise.resolve(store().getItem(PREFIX + path) !== null),
  readFileAsString(path: string, _encoding: string): Promise<string> {
    const text = store().getItem(PREFIX + path);
    return text === null ? Promise.reject(new Error('No such record.')) : Promise.resolve(text);
  },
  writeStringToFile(path: string, text: string, _encoding: string): Promise<void> {
    try { store().setItem(PREFIX + path, text); return Promise.resolve(); } catch (error) { return Promise.reject(error); }
  },
  removeFile(path: string): Promise<void> {
    store().removeItem(PREFIX + path);
    return Promise.resolve();
  },
};
