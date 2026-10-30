import {decodeGetAdminStorageUsageResponse, decodeGetDataStorageUsageResponse} from '../../../../../packages/contracts/src/decoders.ts';
import {routePath} from '../../../../../packages/contracts/src/routes.ts';

export type StorageCategory = Readonly<{
  id: string; name: string; description: string; bytes: number; bytesText: string;
  cleanable: boolean; regenerable: boolean; present: boolean; fileCount: number; oldestDays: number; retentionKey: string;
}>;
export type StorageReport = Readonly<{
  stateDirectory: string; stateDirectoryOnNetwork?: boolean; totalBytes: number; totalBytesText: string; truncated: boolean; categories: readonly StorageCategory[];
}>;
/** The server's data-folder layout: the folders the space row draws and the
 * notices the drive row shows. */
export type DataUsage = Readonly<{
  dataDirectory: string;
  transcodeDirectory: string;
  transcodeBytes: number;
  folders: readonly Readonly<{name: string; bytes: number}>[];
  notices: readonly Readonly<{code: string; message: string}>[];
}>;
export type StorageSnapshot = Readonly<{phase: 'idle' | 'loading' | 'ready' | 'error'}> & Readonly<{report?: StorageReport; usage?: DataUsage; error?: boolean}>;

const idleStorageSnapshot: StorageSnapshot = Object.freeze({phase: 'idle'});

export type StorageTransport = Readonly<{request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>}>;

/** The generated decoder owns the wire shape; this keeps only the drawn facts. */
export function storageReport(value: unknown, serverId: string): StorageReport {
  const decoded = decodeGetAdminStorageUsageResponse(value);
  if (decoded.serverId !== serverId) throw Object.assign(new Error('other_server'), {code: 'other_server'});
  const result = decoded.result;
  // Optional additive producer field; official decoder generation is checkpoint-owned.
  const raw = value as {result?: {stateDirectoryOnNetwork?: unknown}};
  const onNetwork = raw.result?.stateDirectoryOnNetwork;
  if (onNetwork !== undefined && typeof onNetwork !== 'boolean') throw new Error('invalid_storage_network_fact');
  return Object.freeze({stateDirectory: result.stateDirectory, ...(onNetwork === undefined ? {} : {stateDirectoryOnNetwork: onNetwork}), totalBytes: result.totalBytes, totalBytesText: result.totalBytesText, truncated: result.truncated,
    categories: Object.freeze(result.categories.map(category => Object.freeze({id: category.id, name: category.name, description: category.description, bytes: category.bytes,
      bytesText: category.bytesText, cleanable: category.cleanable, regenerable: category.regenerable, present: category.present, fileCount: category.fileCount,
      oldestDays: category.oldestDays, retentionKey: category.retentionKey}))) });
}

/** The data-folder usage, cross-checked against the measured state folder so a
 * stale or wrong-server answer cannot replace the drawn rows. */
export function dataUsage(value: unknown, stateDirectory: string): DataUsage {
  const result = decodeGetDataStorageUsageResponse(value);
  if (!result.dataDirectory || result.dataDirectory !== stateDirectory) throw new Error('other_server');
  return Object.freeze({dataDirectory: result.dataDirectory, transcodeDirectory: result.transcodeDirectory ?? '', transcodeBytes: result.transcodeBytes ?? 0,
    folders: Object.freeze(result.folders.map(folder => Object.freeze({name: folder.name, bytes: folder.bytes}))),
    notices: Object.freeze(result.notices.map(notice => Object.freeze({code: notice.code, message: notice.message})))});
}

/** One selected-server owner read, shared by every storage row on the page. */
export class SettingsStorageSource {
  private state: StorageSnapshot = idleStorageSnapshot;
  private readonly listeners = new Set<() => void>();
  private readonly lifetime = new AbortController();
  private reading?: Promise<void>;
  constructor(private readonly transport: StorageTransport, private readonly serverId: string, private readonly isCurrent: () => boolean, private readonly reportError: (error: unknown) => void = () => {}) {}
  getSnapshot = (): StorageSnapshot => this.current() ? this.state : idleStorageSnapshot;
  subscribe = (listener: () => void): (() => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private current() {return !this.lifetime.signal.aborted && this.isCurrent();}
  private publish(state: StorageSnapshot) {if (this.current()) {this.state = Object.freeze(state);for (const listener of [...this.listeners]) listener();}}
  load = (): Promise<void> => {
    if (!this.current() || this.reading || this.state.phase === 'ready') return this.reading ?? Promise.resolve();
    const task = Promise.resolve().then(async () => {
      this.publish({phase: 'loading'});
      if (!this.current()) return;
      try {
        const [value, layout] = await Promise.all([
          this.transport.request<unknown>(routePath('get_admin_storage_usage'), 'GET', undefined, this.lifetime.signal),
          this.transport.request<unknown>(routePath('get_data_storage_usage'), 'GET', undefined, this.lifetime.signal),
        ]);
        if (!this.current()) return;
        const report = storageReport(value, this.serverId);
        this.publish({phase: 'ready', report, usage: dataUsage(layout, report.stateDirectory)});
      } catch (error) {
        if (!this.current()) return;
        const failure = error && typeof error === 'object' ? error as {status?: number; code?: string} : {};
        if (failure.status === 401 || failure.status === 403 || ['unauthorized', 'forbidden', 'not_permitted'].includes(failure.code ?? '')) {
          this.dispose();this.reportError(error);
        } else this.publish({phase: 'error', error: true});
      }
      finally {this.reading = undefined;}
    });
    this.reading = task;
    return task;
  };
  retry = (): Promise<void> => {this.state = idleStorageSnapshot;return this.load();};
  dispose(): void {if (!this.lifetime.signal.aborted) {this.lifetime.abort();this.listeners.clear();this.state = idleStorageSnapshot;}}
}
