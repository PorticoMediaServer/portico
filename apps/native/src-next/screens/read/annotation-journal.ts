import type {LocalSession} from '../../../../../packages/client-core/src';
import type {ReadingAnnotation, ReadingAnnotationContent} from '../../../../../packages/client-core/src/reading-annotations';
import {NativeQueueStore} from '../../platform/queue-storage';

/** The private, bounded offline annotation journal (P1-READ-OFFLINE-ANNOTATION-SYNC).
 *
 * The server's per-source annotation set stays the only record of truth. This journal holds
 * the edits this device could not deliver and the last server page it saw, so a downloaded
 * publication can be annotated with the existing reader UI while its lease is the only
 * source, and merged by the real session on reconnect. It is never a second annotations
 * store of record and never serves the reader in place of a reachable server.
 *
 * Storage is one protected record per server account profile: the OfflineCore publication
 * lease's own private state namespace for a downloaded source (`reader-state-annotations.json`,
 * bound by that lease to its exact entry/profile/server/receipt/document), or the native
 * protected record store for an ordinary session. Neither place is a general file, logged,
 * or addressable by URL.
 *
 * Identity is captured at queue time and re-checked before every write, so a profile switch,
 * sign-out or re-authentication cannot submit another context's edits. Device time orders
 * local edits only; merge decisions use the server's own `updatedAt` and set revision. */

export const ANNOTATION_JOURNAL_VERSION = 1 as const;
export const ANNOTATION_LEASE_STATE = 'reader-state-annotations.json';
/** The record store refuses records over 256 KiB; keep a margin under it. */
const MAX_RECORD_BYTES = 180_000;
export const MAX_JOURNAL_OPERATIONS = 256;
export const MAX_JOURNAL_GROUPS = 16;
/** The cached server page is a convenience, never the record of truth: bound it well below
 * the journal so a person's own queued edits always win the space. */
export const MAX_CACHE_ENTRIES = 50;
const MAX_CACHE_BYTES = 64_000;

export type NativeAnnotationIdentity = Readonly<{serverId: string; accountId: string; profileId: string}>;
export type NativeAnnotationFormat = 'epub' | 'pdf' | 'cbz';
export type NativeAnnotationSource = Readonly<{itemId: string; sourceId: string; sourceRevision: string; format: NativeAnnotationFormat}>;
export type NativeAnnotationUnresolved = 'stale-source' | 'source-changed' | 'server-newer' | 'deleted-elsewhere' | 'conflict' | 'refused';
export type NativeAnnotationOperation = Readonly<{
  /** The annotation's own id: one operation identity for this entry across every retry. */
  id: string;
  /** A tombstone: the person deleted the entry; older queued work must never resurrect it. */
  delete: boolean;
  /** The complete, original content frozen when the person made the edit. */
  content?: ReadingAnnotationContent;
  /** Device time, used for local ordering and tie-breaking only. Never trusted as server time. */
  observedAt: number;
  /** The server annotation-set revision this edit was made against (0 when never seen). */
  baseRevision: number;
  /** The server's `updatedAt` for this id at that base; 0 when the entry did not exist. */
  baseUpdatedAt: number;
  /** Why the current server refused or displaced it; absent while it is still ordinary offline work. */
  unresolved?: NativeAnnotationUnresolved;
}>;
export type NativeAnnotationCache = Readonly<{revision: number; entries: readonly ReadingAnnotation[]}>;
export type NativeAnnotationGroup = Readonly<{
  source: NativeAnnotationSource;
  operations: readonly NativeAnnotationOperation[];
  /** The local view's revision: the server's last observed revision plus this device's edits. */
  localRevision: number;
  cache?: NativeAnnotationCache;
}>;
export type NativeAnnotationRecord = Readonly<{version: 1; identity: NativeAnnotationIdentity; groups: readonly NativeAnnotationGroup[]}>;
export type NativeAnnotationOperationRef = Readonly<{id: string; observedAt: number}>;
export type NativeAnnotationResolution = NativeAnnotationOperationRef & (
  | Readonly<{outcome: 'applied' | 'resolved'}>
  | Readonly<{outcome: 'unresolved'; reason: NativeAnnotationUnresolved}>);
export type NativeAnnotationView = Readonly<{revision: number; entries: readonly ReadingAnnotation[]}>;

/** The OfflineCore publication lease's private state namespace, structurally. The lease,
 * not this module, owns the file, the exact download binding and its lifetime. */
export type NativeAnnotationLeaseState = Readonly<{
  current: () => boolean;
  readState: (name: string) => Promise<string>;
  writeState: (name: string, json: string) => Promise<void>;
}>;

type Change = (update: (current: NativeAnnotationRecord | undefined) => NativeAnnotationRecord | null, assert?: () => void) => Promise<void>;
type Backend = Readonly<{read: (assert?: () => void) => Promise<NativeAnnotationRecord | undefined>; change: Change}>;

const groupKey = (source: NativeAnnotationSource) => JSON.stringify([source.itemId, source.sourceId, source.sourceRevision, source.format]);
const sameIdentity = (a: NativeAnnotationIdentity, b: NativeAnnotationIdentity) => a.serverId === b.serverId && a.accountId === b.accountId && a.profileId === b.profileId;
const object = (value: unknown): value is Record<string, unknown> => typeof value === 'object' && value !== null && !Array.isArray(value);
const text = (value: unknown, max: number): value is string => typeof value === 'string' && new TextEncoder().encode(value).length <= max;
const integer = (value: unknown, max = Number.MAX_SAFE_INTEGER): value is number => typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 && value <= max;
const format = (value: unknown): value is NativeAnnotationFormat => value === 'epub' || value === 'pdf' || value === 'cbz';
const reason = (value: unknown): value is NativeAnnotationUnresolved => value === 'stale-source' || value === 'source-changed' || value === 'server-newer'
  || value === 'deleted-elsewhere' || value === 'conflict' || value === 'refused';

/** FNV-1a over the profile tuple names the protected record. A collision is detected by
 * reading the stored identity back; a mismatch is treated as another profile's record. */
export function nativeAnnotationJournalKey(identity: NativeAnnotationIdentity): string {
  let hash = 0x811c9dc5;
  const input = `${identity.serverId}\u0000${identity.accountId}\u0000${identity.profileId}`;
  for (let index = 0; index < input.length; index++) {hash ^= input.charCodeAt(index); hash = Math.imul(hash, 0x01000193) >>> 0;}
  return `reading-annotations:v1:${hash.toString(16).padStart(8, '0')}`;
}

export const nativeAnnotationIdentity = (viewer: LocalSession['viewer']): NativeAnnotationIdentity =>
  Object.freeze({serverId: viewer.serverId, accountId: viewer.accountId, profileId: viewer.profileId});

function annotationContent(raw: unknown): ReadingAnnotationContent {
  if (!object(raw) || typeof raw.id !== 'string' || !/^[A-Za-z0-9_-]{1,64}$/.test(raw.id) || (raw.kind !== 'bookmark' && raw.kind !== 'highlight')
    || !text(raw.text, 8192) || !text(raw.note, 4096) || (raw.colour !== '' && raw.colour !== 'yellow' && raw.colour !== 'green' && raw.colour !== 'blue' && raw.colour !== 'pink')
    || !object(raw.location)) throw new Error('native_annotation_journal_invalid');
  return raw as ReadingAnnotationContent;
}
function annotationEntry(raw: unknown): ReadingAnnotation {
  if (!object(raw) || !integer(raw.createdAt) || !integer(raw.updatedAt) || raw.updatedAt < raw.createdAt) throw new Error('native_annotation_journal_invalid');
  return Object.freeze({...annotationContent(raw), createdAt: raw.createdAt, updatedAt: raw.updatedAt});
}
function operation(raw: unknown): NativeAnnotationOperation {
  if (!object(raw) || typeof raw.id !== 'string' || !/^[A-Za-z0-9_-]{1,64}$/.test(raw.id) || typeof raw.delete !== 'boolean'
    || !integer(raw.observedAt) || !integer(raw.baseRevision) || !integer(raw.baseUpdatedAt)
    || raw.unresolved !== undefined && !reason(raw.unresolved)) throw new Error('native_annotation_journal_invalid');
  return Object.freeze({id: raw.id, delete: raw.delete, observedAt: raw.observedAt, baseRevision: raw.baseRevision, baseUpdatedAt: raw.baseUpdatedAt,
    ...(raw.unresolved === undefined ? {} : {unresolved: raw.unresolved}),
    ...(raw.delete ? {} : {content: annotationContent(raw.content)})});
}
function cacheValue(raw: unknown): NativeAnnotationCache {
  if (!object(raw) || !integer(raw.revision) || !Array.isArray(raw.entries) || raw.entries.length > MAX_CACHE_ENTRIES) throw new Error('native_annotation_journal_invalid');
  const entries = raw.entries.map(annotationEntry);
  if (new Set(entries.map(entry => entry.id)).size !== entries.length) throw new Error('native_annotation_journal_invalid');
  return Object.freeze({revision: raw.revision, entries: Object.freeze(entries)});
}
function record(raw: unknown, identity: NativeAnnotationIdentity): NativeAnnotationRecord | undefined {
  if (raw == null || raw === '') return undefined;
  if (typeof raw === 'string') {try {raw = JSON.parse(raw);} catch {throw new Error('native_annotation_journal_invalid');}}
  if (!object(raw) || raw.version !== ANNOTATION_JOURNAL_VERSION || !object(raw.identity) || !Array.isArray(raw.groups)
    || typeof raw.identity.serverId !== 'string' || typeof raw.identity.accountId !== 'string' || typeof raw.identity.profileId !== 'string') throw new Error('native_annotation_journal_invalid');
  if (!sameIdentity(raw.identity as NativeAnnotationIdentity, identity)) return undefined; // another profile's record: never read or overwrite
  const groups = raw.groups.map(group => {
    if (!object(group) || !object(group.source) || !Array.isArray(group.operations) || typeof group.source.itemId !== 'string'
      || typeof group.source.sourceId !== 'string' || typeof group.source.sourceRevision !== 'string' || !format(group.source.format)
      || group.localRevision !== undefined && !integer(group.localRevision)) throw new Error('native_annotation_journal_invalid');
    const operations = group.operations.map(operation);
    if (new Set(operations.map(value => value.id)).size !== operations.length) throw new Error('native_annotation_journal_invalid');
    const cache = group.cache === undefined ? {} : {cache: cacheValue(group.cache)};
    return Object.freeze({source: Object.freeze({itemId: group.source.itemId, sourceId: group.source.sourceId,
      sourceRevision: group.source.sourceRevision, format: group.source.format}), operations: Object.freeze(operations),
      localRevision: (group.localRevision as number | undefined) ?? 0, ...cache});
  });
  const total = groups.reduce((sum, group) => sum + group.operations.length, 0);
  if (groups.length > MAX_JOURNAL_GROUPS || total > MAX_JOURNAL_OPERATIONS) throw new Error('native_annotation_journal_invalid');
  return Object.freeze({version: ANNOTATION_JOURNAL_VERSION, identity: Object.freeze({...identity}), groups: Object.freeze(groups)});
}
const bytes = (value: unknown) => new TextEncoder().encode(JSON.stringify(value)).length;
const empty = (identity: NativeAnnotationIdentity): NativeAnnotationRecord => Object.freeze({version: ANNOTATION_JOURNAL_VERSION, identity: Object.freeze({...identity}), groups: Object.freeze([])});

/** The ordinary online session's protected record store. */
function nativeQueueBackend(identity: NativeAnnotationIdentity, storage: Pick<NativeQueueStore, 'change'>): Backend {
  const key = nativeAnnotationJournalKey(identity);
  const turn = <T,>(update: (current: NativeAnnotationRecord | undefined) => {record: unknown; value: T}, assert?: () => void) =>
    storage.change<T>(key, raw => {assert?.(); return update(record(raw, identity));}, undefined, assert);
  return {
    read: (assert) => turn(current => ({record: current ?? null, value: current}), assert),
    change: (update, assert) => turn(current => {
      const next = update(current);
      if (next && bytes(next) > MAX_RECORD_BYTES) throw new Error('native_annotation_journal_full');
      return {record: next, value: undefined};
    }, assert).then(() => undefined),
  };
}
/** A downloaded publication's journal, in the OfflineCore lease's own state namespace. The
 * lease serializes its reads and writes; this adapter adds one read-modify-write turn and
 * re-checks the lease's liveness before writing. */
function leaseBackend(state: NativeAnnotationLeaseState, identity: NativeAnnotationIdentity): Backend {
  let tail: Promise<unknown> = Promise.resolve();
  const live = () => {if (!state.current()) throw new Error('native_annotation_journal_retired');};
  const turn = <T,>(work: () => Promise<T>): Promise<T> => {
    const run = tail.then(async () => {live(); const value = await work(); live(); return value;});
    tail = run.catch(() => {});
    return run;
  };
  return {
    read: () => turn(async () => record(await state.readState(ANNOTATION_LEASE_STATE), identity)),
    change: (update, assert) => turn(async () => {
      const current = record(await state.readState(ANNOTATION_LEASE_STATE), identity);
      assert?.(); live();
      const next = update(current);
      if (next && bytes(next) > MAX_RECORD_BYTES) throw new Error('native_annotation_journal_full');
      // The lease has no delete: an emptied journal is an empty record with this identity.
      const written = next ?? (current ? empty(identity) : undefined);
      if (!written) return;
      assert?.(); live();
      await state.writeState(ANNOTATION_LEASE_STATE, JSON.stringify(written));
    }),
  };
}

/** The journal keeps one record per profile, in whichever protected store this reader
 * actually has: the downloaded publication's lease state, or the native record store.
 * Every mutation is a serialized read-modify-write; network work never happens inside it. */
export class NativeAnnotationJournal {
  private readonly local: Pick<NativeQueueStore, 'change'>;
  private identity?: NativeAnnotationIdentity;
  private backend?: Backend;
  constructor(storage: Pick<NativeQueueStore, 'change'> = new NativeQueueStore()) {this.local = storage;}
  /** Bind this journal to the captured profile, optionally to a downloaded source's lease
   * namespace. Called when the reader's real session opens; identity never changes silently. */
  bind(identity: NativeAnnotationIdentity, lease?: NativeAnnotationLeaseState): void {
    this.identity = Object.freeze({...identity});
    this.backend = lease ? leaseBackend(lease, identity) : nativeQueueBackend(identity, this.local);
  }
  unbind(): void {this.backend = undefined;}
  private store(identity: NativeAnnotationIdentity): Backend {
    if (this.identity && !sameIdentity(this.identity, identity)) throw new Error('native_annotation_journal_foreign_profile');
    this.identity ??= Object.freeze({...identity});
    return this.backend ?? (this.backend = nativeQueueBackend(identity, this.local));
  }
  snapshot = async (identity: NativeAnnotationIdentity): Promise<NativeAnnotationRecord | undefined> => this.store(identity).read();
  list = async (identity: NativeAnnotationIdentity): Promise<readonly NativeAnnotationGroup[]> => (await this.snapshot(identity))?.groups ?? [];
  /** The local annotation view for this source: the last server page, plus every queued edit. */
  view = async (identity: NativeAnnotationIdentity, source: NativeAnnotationSource): Promise<NativeAnnotationView> =>
    nativeAnnotationView(await this.snapshot(identity), source);

  /** The newest edit for an id wins locally; the server base and the id stay those of the
   * first queued operation, so a later local edit cannot pretend to be based on newer server data. */
  enqueue = async (identity: NativeAnnotationIdentity, source: NativeAnnotationSource, next: Omit<NativeAnnotationOperation, 'unresolved'>, assert?: () => void): Promise<void> => {
    await this.add(identity, source, next, assert, false);
  };
  /** One edit made against the local view. Refused when the guest's revision is not the
   * current local one; returns the new local revision the reader can use for its next write. */
  localMutate = async (identity: NativeAnnotationIdentity, source: NativeAnnotationSource,
    next: Omit<NativeAnnotationOperation, 'unresolved'>, expectedLocalRevision: number, assert?: () => void): Promise<number> =>
    this.add(identity, source, next, assert, true, expectedLocalRevision);
  /** Remember the last server page this device saw, so a later offline open can show it. */
  cache = async (identity: NativeAnnotationIdentity, source: NativeAnnotationSource, revision: number, entries: readonly ReadingAnnotation[], assert?: () => void): Promise<void> => {
    if (!integer(revision)) return;
    let kept: ReadingAnnotation[] = [];
    let size = 0;
    for (const entry of entries.slice(0, MAX_CACHE_ENTRIES)) {
      const entryBytes = bytes(entry);
      if (size + entryBytes > MAX_CACHE_BYTES) break;
      kept.push(entry); size += entryBytes;
    }
    const wanted = groupKey(source);
    await this.store(identity).change(current => {
      const base = current ?? empty(identity);
      const at = base.groups.findIndex(group => groupKey(group.source) === wanted);
      const existing = at < 0 ? undefined : base.groups[at];
      const group: NativeAnnotationGroup = Object.freeze({source: Object.freeze({...source}), operations: existing?.operations ?? Object.freeze([]),
        localRevision: Math.max(existing?.localRevision ?? 0, revision), cache: Object.freeze({revision, entries: Object.freeze(kept)})});
      const groups = [...base.groups];
      if (at < 0) groups.push(group); else groups[at] = group;
      const next: NativeAnnotationRecord = Object.freeze({version: ANNOTATION_JOURNAL_VERSION, identity: Object.freeze({...identity}), groups: Object.freeze(groups)});
      // An optional cache must never erase this profile's existing pending edits to fit.
      return bytes(next) > MAX_RECORD_BYTES ? current ?? null : next;
    }, assert);
  };
  /** Retire only operations that still are the exact ones the pass examined. A newer local
   * edit made during the pass keeps its place, and an unresolved operation keeps its payload. */
  resolve = async (identity: NativeAnnotationIdentity, source: NativeAnnotationSource, resolutions: readonly NativeAnnotationResolution[], assert?: () => void): Promise<void> => {
    if (!resolutions.length) return;
    const wanted = groupKey(source);
    await this.store(identity).change(current => {
      if (!current) return null;
      const groups = current.groups.map(group => {
        if (groupKey(group.source) !== wanted) return group;
        const operations = group.operations.flatMap(operation => {
          const resolution = resolutions.find(value => value.id === operation.id && value.observedAt === operation.observedAt);
          if (!resolution) return [operation];
          if (resolution.outcome !== 'unresolved') return [];
          return [{...operation, unresolved: resolution.reason} as NativeAnnotationOperation];
        });
        return {...group, operations} as NativeAnnotationGroup;
      }).filter(group => group.operations.length > 0 || group.cache !== undefined);
      if (!groups.length) return null;
      return Object.freeze({version: ANNOTATION_JOURNAL_VERSION, identity: Object.freeze({...identity}), groups: Object.freeze(groups)});
    }, assert);
  };
  /** Discards this profile's pending work (explicit erasure of the device's queued edits). */
  discard = async (identity: NativeAnnotationIdentity): Promise<void> => {
    await this.store(identity).change(() => null);
  };

  private async add(identity: NativeAnnotationIdentity, source: NativeAnnotationSource, next: Omit<NativeAnnotationOperation, 'unresolved'>,
    assert: (() => void) | undefined, bump: boolean, expectLocal?: number): Promise<number> {
    if (!integer(next.observedAt) || !integer(next.baseRevision) || !integer(next.baseUpdatedAt)) throw new Error('native_annotation_journal_invalid');
    const operation: NativeAnnotationOperation = next.delete
      ? Object.freeze({id: next.id, delete: true, observedAt: next.observedAt, baseRevision: next.baseRevision, baseUpdatedAt: next.baseUpdatedAt})
      : Object.freeze({id: next.id, delete: false, content: annotationContent(next.content), observedAt: next.observedAt, baseRevision: next.baseRevision, baseUpdatedAt: next.baseUpdatedAt});
    const wanted = groupKey(source);
    let revision = 0;
    await this.store(identity).change(current => {
      const base = current ?? empty(identity);
      const groups = base.groups.map(group => group);
      const at = groups.findIndex(group => groupKey(group.source) === wanted);
      const existing = at < 0 ? undefined : groups[at];
      if (expectLocal !== undefined && (existing?.localRevision ?? 0) !== expectLocal) throw new Error('native_annotation_local_conflict');
      const previous = existing?.operations.find(value => value.id === operation.id);
      const merged: NativeAnnotationOperation = previous
        ? Object.freeze({...operation, baseRevision: previous.baseRevision, baseUpdatedAt: previous.baseUpdatedAt})
        : operation;
      const operations = existing ? [...existing.operations.filter(value => value.id !== operation.id), merged] : [merged];
      const localRevision = bump ? (existing?.localRevision ?? 0) + 1 : existing?.localRevision ?? 0;
      revision = localRevision;
      const group: NativeAnnotationGroup = Object.freeze({...existing, source: Object.freeze({...source}),
        operations: Object.freeze(operations.sort((a, b) => a.observedAt - b.observedAt || a.id.localeCompare(b.id))), localRevision});
      if (at < 0) groups.push(group); else groups[at] = group;
      const total = groups.reduce((sum, value) => sum + value.operations.length, 0);
      if (groups.length > MAX_JOURNAL_GROUPS || total > MAX_JOURNAL_OPERATIONS) throw new Error('native_annotation_journal_full');
      let built: NativeAnnotationRecord = Object.freeze({version: ANNOTATION_JOURNAL_VERSION, identity: Object.freeze({...identity}), groups: Object.freeze(groups)});
      // Space belongs to the person's own edits: drop the cached server page before refusing.
      if (bytes(built) > MAX_RECORD_BYTES) {
        const trimmed = groups.map(value => value.cache === undefined ? value : {...value, cache: undefined} as NativeAnnotationGroup);
        built = Object.freeze({version: ANNOTATION_JOURNAL_VERSION, identity: Object.freeze({...identity}), groups: Object.freeze(trimmed)});
      }
      if (bytes(built) > MAX_RECORD_BYTES) throw new Error('native_annotation_journal_full');
      return built;
    }, assert);
    return revision;
  }
}

/** The local view: cached server entries overlaid with this device's queued edits, ordered by
 * id like the server's pages. The revision is local and monotonic; it is never sent as a
 * server base — queued operations keep the server revision they were actually based on. */
export function nativeAnnotationView(record: NativeAnnotationRecord | undefined, source: NativeAnnotationSource): NativeAnnotationView {
  const group = record?.groups.find(value => groupKey(value.source) === groupKey(source));
  const map = new Map<string, ReadingAnnotation>((group?.cache?.entries ?? []).map(entry => [entry.id, entry]));
  for (const op of group?.operations ?? []) {
    if (op.delete) {map.delete(op.id); continue;}
    if (op.content) map.set(op.id, Object.freeze({...op.content, createdAt: op.observedAt, updatedAt: op.observedAt}));
  }
  const entries = [...map.values()].sort((a, b) => a.id.localeCompare(b.id)).slice(0, 256);
  return Object.freeze({revision: Math.max(group?.localRevision ?? 0, group?.cache?.revision ?? 0), entries: Object.freeze(entries)});
}

/** The OfflineCore publication lease a session exposes, when this reader is reading the
 * exact downloaded source. Purely structural: this module never owns the lease or its file. */
export function nativeAnnotationLeaseOf(session: unknown): NativeAnnotationLeaseState | undefined {
  if (!session || typeof session !== 'object' || !('offline' in session)) return undefined;
  const lease = (session as {offline?: unknown}).offline;
  if (!lease || typeof lease !== 'object') return undefined;
  const value = lease as Partial<NativeAnnotationLeaseState>;
  return typeof value.current === 'function' && typeof value.readState === 'function' && typeof value.writeState === 'function'
    ? value as NativeAnnotationLeaseState : undefined;
}

/** Counts for the honest status surface: queued operations and those the server displaced. */
export function nativeAnnotationJournalCounts(groups: readonly NativeAnnotationGroup[]) {
  let queued = 0, unresolved = 0;
  for (const group of groups) for (const operation of group.operations) {queued++; if (operation.unresolved) unresolved++;}
  return {queued, unresolved};
}
