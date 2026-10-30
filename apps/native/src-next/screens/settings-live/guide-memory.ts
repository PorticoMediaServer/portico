import type {LegacyGuideOptions} from '../../../../../packages/client-core/src/guide/legacy-source';
import {NativeQueueStore} from '../../platform/queue-storage';
import type {Viewer} from '../../../../../packages/client-core/src';
import {screenScopeEnded, type NativeScreenLifetime} from './lifetime';

export type NativeGuideEntry = Pick<LegacyGuideOptions, 'kind' | 'sourceId'>;
export type NativeGuideChoice = Readonly<{view?: 'guide' | 'list'; sort?: 'number' | 'name'}>;
export type NativeGuideMemoryPort = Readonly<{
  read: () => Promise<ReadonlyMap<string, NativeGuideChoice>>;
  write: (entry: NativeGuideEntry, choice: NativeGuideChoice) => Promise<void>;
}>;
type Store = Pick<NativeQueueStore, 'change'>;
type Scope = Readonly<{serverId: string; authority: 'local' | 'hosted'; accountId: string; profileId: string}>;
const records = new NativeQueueStore();
const object = (raw: unknown): raw is Record<string, unknown> => !!raw && typeof raw === 'object' && !Array.isArray(raw);
const identity = (raw: unknown): raw is string => typeof raw === 'string' && raw.length > 0 && raw.length <= 256 && !/[\x00-\x1f\x7f]/.test(raw);
export function nativeGuideEntryKey(entry: NativeGuideEntry): string {
  if (!['all', 'live-source', 'library-channel'].includes(entry.kind) || typeof entry.sourceId !== 'string'
    || entry.sourceId !== '' && !identity(entry.sourceId) || entry.kind === 'all' && entry.sourceId !== '') throw new Error('native_guide_memory_entry_invalid');
  return JSON.stringify([entry.kind, entry.sourceId]);
}
function validKey(key: string): boolean {
  try {const entry: unknown = JSON.parse(key); return Array.isArray(entry) && entry.length === 2 && nativeGuideEntryKey({kind: entry[0], sourceId: entry[1]}) === key;} catch {return false;}
}
function decode(raw: unknown): Map<string, NativeGuideChoice> {
  const result = new Map<string, NativeGuideChoice>();
  if (!object(raw) || !Array.isArray(raw.choices)) return result;
  for (const row of raw.choices.slice(-50)) {
    if (!object(row) || typeof row.entry !== 'string' || !validKey(row.entry)
      || row.view !== undefined && row.view !== 'guide' && row.view !== 'list'
      || row.sort !== undefined && row.sort !== 'number' && row.sort !== 'name' || row.view === undefined && row.sort === undefined) continue;
    const view = row.view === 'guide' || row.view === 'list' ? row.view : undefined, sort = row.sort === 'number' || row.sort === 'name' ? row.sort : undefined;
    result.delete(row.entry); result.set(row.entry, {...(view ? {view} : {}), ...(sort ? {sort} : {})});
  }
  return result;
}

/** One device record, explicitly partitioned by real server and viewer. The old
 * viewer-only channels record is left untouched; server preferences are not shadowed. */
export class NativeGuideMemory implements NativeGuideMemoryPort {
  private readonly key: string;
  private readonly storage: Store;
  private readonly current: () => boolean;
  constructor(storage: Store, scope: Scope, current: () => boolean) {
    if (![scope.serverId, scope.authority, scope.accountId, scope.profileId].every(identity) || !['local', 'hosted'].includes(scope.authority)) throw new Error('native_guide_memory_scope_invalid');
    this.storage = storage; this.current = current;
    this.key = 'next.guide.v1:' + JSON.stringify([scope.serverId, scope.authority, scope.accountId, scope.profileId]);
  }
  private assert() {if (!this.current()) throw screenScopeEnded();}
  read = async (): Promise<ReadonlyMap<string, NativeGuideChoice>> => {
    this.assert();
    const value = await this.storage.change(this.key, raw => {this.assert(); return {record: raw, value: decode(raw)};});
    this.assert(); return value;
  };
  write = async (entry: NativeGuideEntry, choice: NativeGuideChoice): Promise<void> => {
    this.assert(); const key = nativeGuideEntryKey(entry);
    if (Object.keys(choice).some(field => field !== 'view' && field !== 'sort') || choice.view === undefined && choice.sort === undefined
      || choice.view !== undefined && choice.view !== 'guide' && choice.view !== 'list'
      || choice.sort !== undefined && choice.sort !== 'number' && choice.sort !== 'name') throw new Error('native_guide_memory_choice_invalid');
    await this.storage.change(this.key, raw => {
      this.assert(); const choices = decode(raw), next = {...choices.get(key), ...choice};
      choices.delete(key); choices.set(key, next);
      const bounded = [...choices].slice(-50);
      return {record: {...(object(raw) ? raw : {}), choices: bounded.map(([entry, value]) => ({entry, ...value}))}, value: undefined};
    });
    // Observe the committed native record, rather than acknowledging a React update.
    await this.read();
  };
}

export function nativeGuideMemory(viewer: Viewer | undefined, lifetime: NativeScreenLifetime): NativeGuideMemory {
  lifetime.assert();
  if (!viewer || viewer.role === 'account' || viewer.serverId !== lifetime.authority.serverId || viewer.accountId !== lifetime.authority.accountId || viewer.profileId !== lifetime.authority.profileId) throw screenScopeEnded();
  const {authority} = viewer; if (authority !== 'local' && authority !== 'hosted') throw screenScopeEnded();
  return new NativeGuideMemory(records, {...viewer, authority}, lifetime.current);
}
