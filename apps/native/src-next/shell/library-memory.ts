import type {ShellScope} from './model';
import {scopeKey} from './model';

export interface LibraryRecordStore {
  change<T>(key: string, update: (raw: unknown) => {record: unknown; value: T}): Promise<T>;
}

const validId = (value: unknown): value is string => typeof value === 'string' && value.length > 0 && value.length <= 256 && !/[\x00-\x1f\x7f]/.test(value);

/** Device-only, one bounded record per real server/account/profile; never a preference API. */
export class LastLibraryMemory {
  private pending = new Map<string, Promise<unknown>>();
  private readonly storage: LibraryRecordStore;
  constructor(storage: LibraryRecordStore) {this.storage = storage;}
  /** Sidebar state shares the injected device store, never the synchronized preference API. */
  async readSidebar(scope: ShellScope, current: () => boolean): Promise<boolean> {
    const key = 'next.sidebar-expanded.v1:' + scopeKey(scope);
    await this.pending.get(key)?.catch(() => {});
    if (!current()) return true;
    return this.storage.change(key, raw => ({record: raw, value: current() && typeof raw === 'boolean' ? raw : true}));
  }
  rememberSidebar(scope: ShellScope, expanded: boolean, current: () => boolean): Promise<void> {
    const key = 'next.sidebar-expanded.v1:' + scopeKey(scope);
    const write = (this.pending.get(key) ?? Promise.resolve()).catch(() => {}).then(() => {
      if (!current()) return;
      return this.storage.change(key, raw => ({record: current() ? expanded : raw, value: undefined}));
    });
    this.pending.set(key, write);
    void write.finally(() => {if (this.pending.get(key) === write) this.pending.delete(key);}).catch(() => {});
    return write;
  }
  private key(scope: ShellScope): string {return 'next.last-library.v1:' + scopeKey(scope);}
  /** A Music choice is retained independently of the last library of any kind. */
  async readMusic(scope: ShellScope): Promise<string | undefined> {
    const key = 'next.last-music-library.v1:' + scopeKey(scope);
    await this.pending.get(key);
    return this.storage.change(key, raw => ({record: raw, value: validId(raw) ? raw : undefined}));
  }
  rememberMusic(scope: ShellScope, libraryId: string): Promise<void> {
    if (!validId(libraryId)) return Promise.reject(new Error('invalid_library_identity'));
    const key = 'next.last-music-library.v1:' + scopeKey(scope);
    const write = (this.pending.get(key) ?? Promise.resolve()).catch(() => {}).then(() => this.storage.change(key, () => ({record: libraryId, value: undefined})));
    this.pending.set(key, write);
    void write.finally(() => {if (this.pending.get(key) === write) this.pending.delete(key);}).catch(() => {});
    return write;
  }
  async read(scope: ShellScope): Promise<string | undefined> {
    const key = this.key(scope);
    await this.pending.get(key);
    return this.storage.change(key, raw => ({record: raw, value: validId(raw) ? raw : undefined}));
  }
  remember(scope: ShellScope, libraryId: string): Promise<void> {
    if (!validId(libraryId)) return Promise.reject(new Error('invalid_library_identity'));
    const key = this.key(scope);
    // Ordering is explicit even when an injected store doesn't serialize writes itself.
    const write = (this.pending.get(key) ?? Promise.resolve()).catch(() => {}).then(() => this.storage.change(key, () => ({record: libraryId, value: undefined})));
    this.pending.set(key, write);
    void write.finally(() => {if (this.pending.get(key) === write) this.pending.delete(key);}).catch(() => {});
    return write;
  }
}
