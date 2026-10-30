import type {QueueEntry} from '@core/playback-v1/types.ts';
import type {V1PlaybackTarget, V1QueueEditIntent, V1QueueEditPage, V1QueueEditReader} from '@core/playback-v1/v1-queue-player.ts';
import type {PlaybackFence} from '../../../../packages/player-core/src/index.ts';
import type {WebListeningTarget} from './listening-port.ts';

/** One row of one actually issued canonical page, kept truthful for presentation. */
export type WebQueueEditRow = Readonly<{
  entryId: string; itemId: string; title: string; position: number;
  available: boolean; hidden: boolean; current: boolean; artworkUrl?: string; artist?: string; durationMs?: number;
}>;

/** An immutable Web presentation of one canonical bounded page (max 50 rows). */
export type WebQueueEditPage = Readonly<{
  start: number; total: number; revision: string; rows: readonly WebQueueEditRow[];
  canNext: boolean; canPrevious: boolean;
}>;

export type WebQueueEditSnapshot = Readonly<{
  phase: 'idle' | 'loading' | 'ready' | 'error' | 'retired';
  page: WebQueueEditPage | null;
  busy: boolean;
  error?: Readonly<{code: string; message: string}>;
}>;

/** The optional edit opening of the existing listening queue facade. It consumes the
 * public canonical `captureEditWindow` reader and never becomes a second queue writer:
 * every read, page step and mutation is delegated to that reader for this opening only. */
export type WebQueueEditOpening = Readonly<{
  getSnapshot(): WebQueueEditSnapshot;
  subscribe(listener: () => void): () => void;
  /** First canonical read of this opening; one flight, no duplicate. */
  open(): Promise<void>;
  next(): Promise<void>;
  previous(): Promise<void>;
  /** Only an offered adjacent available upcoming pair in the drawn page. Every command uses
   * the one lifetime this opening was captured with; another caller refuses honestly. */
  move(shown: WebQueueEditPage, entryId: string, direction: -1 | 1): Promise<void>;
  remove(shown: WebQueueEditPage, entryId: string): Promise<void>;
  dispose(): void;
}>;

export type WebQueueEditLifetime = Readonly<{signal: AbortSignal; isCurrent(): boolean}>;

export type WebQueueEditInput = Readonly<{
  /** The public canonical reader for this exact captured opening. */
  reader: V1QueueEditReader;
  /** Exact runtime/stack/source/target proof, repeated after every await. */
  current(): boolean;
  lifetime: WebQueueEditLifetime;
  /** The original accepted occurrence; the anchor is read from the page, never guessed. */
  currentEntryId: string;
  /** Hidden is a drawn-list fact only; it never widens editability. */
  hidden(entryId: string): boolean;
}>;

const busy: () => never = () => {throw Object.assign(new Error('queue_edit_busy'), {code: 'queue_edit_busy'});};
const stale: () => never = () => {throw Object.assign(new Error('queue_edit_stale'), {code: 'queue_edit_stale'});};

const empty = (): WebQueueEditSnapshot => Object.freeze({phase: 'idle', page: null, busy: false});

/** One captured edit opening over the canonical reader. Pages are private receipts by
 * identity: a Web page can only be used with the opening and canonical page that issued it. */
export function createQueueEditOpening(input: WebQueueEditInput): WebQueueEditOpening {
  const issued = new WeakMap<WebQueueEditPage, Readonly<{opening: object; canonical: V1QueueEditPage}>>();
  const resident: WebQueueEditPage[] = [];
  const opening = {};
  const listeners = new Set<() => void>();
  let state: WebQueueEditSnapshot = empty();
  let disposed = false;
  let dead = false;
  let flight: object | null = null;
  let anchorPosition: number | undefined;

  /** Mutable record first, external predicates next, then the exact internal record again;
   * any observed refusal latches dead so a later true cannot revive it. */
  const live = (): boolean => {
    if (disposed || dead || input.lifetime.signal.aborted) return false;
    if (!input.lifetime.isCurrent()) {retire(); return false;}
    if (!input.current()) {retire(); return false;}
    if (dead || disposed || input.lifetime.signal.aborted) {retire(); return false;}
    // The last host predicate may have replaced the runtime/source and still answered true.
    if (!input.lifetime.isCurrent() || !input.current()) {retire(); return false;}
    return !dead && !disposed && !input.lifetime.signal.aborted;
  };
  const publish = (next: Partial<WebQueueEditSnapshot>) => {
    if (disposed) return;
    state = Object.freeze({...state, ...next});
    for (const listener of [...listeners]) listener();
  };
  const requireLive = () => {if (!live()) {retire(); stale();}};
  /** Reserve one flight synchronously before any publication, IO or listener. */
  const begin = (): object => {
    requireLive();
    if (flight) busy();
    const token = {};
    flight = token;
    publish({busy: true, error: undefined});
    return token;
  };
  const end = (token: object) => {if (flight === token) flight = null;};
  const recheck = (token: object) => {
    if (disposed || flight !== token || !live()) {retire(); stale();}
  };
  const retire = () => {
    if (disposed) return;
    disposed = true;
    dead = true;
    flight = null;
    listeners.clear();
    try {input.reader.dispose();} catch {}
    state = Object.freeze({phase: 'retired', page: null, busy: false});
  };
  const row = (entry: QueueEntry): WebQueueEditRow => Object.freeze({
    entryId: entry.entryId, itemId: entry.itemId, title: entry.title, position: entry.position,
    available: entry.available === true, hidden: input.hidden(entry.entryId), current: entry.entryId === input.currentEntryId,
    ...(entry.posterUrl ?? entry.stillUrl ? {artworkUrl: entry.posterUrl ?? entry.stillUrl} : {}),
    ...(entry.artist ? {artist: entry.artist} : {}),
    ...(entry.durationMs === undefined ? {} : {durationMs: entry.durationMs}),
  });
  const adopt = (canonical: V1QueueEditPage): WebQueueEditPage => {
    const rows = canonical.items.map(row);
    if (anchorPosition === undefined) {
      const anchor = rows.find(candidate => candidate.entryId === input.currentEntryId && !candidate.hidden);
      if (!anchor) {retire(); stale();}
      anchorPosition = anchor.position;
    }
    if (!live()) {retire(); stale();}
    for (const candidate of rows) {if (candidate.hidden !== input.hidden(candidate.entryId)) {retire(); stale();}}
    if (!live()) {retire(); stale();}
    const page: WebQueueEditPage = Object.freeze({start: canonical.start, total: canonical.total, revision: canonical.revision,
      rows: Object.freeze(rows), canNext: canonical.nextCursor !== undefined, canPrevious: canonical.canPrevious});
    issued.set(page, {opening, canonical});
    resident.push(page);
    while (resident.length > 2) {const dropped = resident.shift()!; issued.delete(dropped);}
    publish({phase: 'ready', page, busy: false, error: undefined});
    return page;
  };
  const fail = (message: string, code: string) => {
    if (disposed) return;
    publish({busy: false, error: Object.freeze({code, message})});
  };
  const own = (shown: WebQueueEditPage) => {
    const receipt = issued.get(shown);
    if (!receipt || receipt.opening !== opening || state.page !== shown || !resident.includes(shown)) stale();
    return receipt.canonical;
  };
  const run = async (token: object, work: () => Promise<V1QueueEditPage>) => {
    try {
      recheck(token);            // after the busy publication and any synchronous subscriber
      const next = await work();
      recheck(token);
      adopt(next);
      recheck(token);
    } catch (error) {
      if (!disposed) {retire();}
      throw error;
    } finally {end(token);}
  };
  return Object.freeze({
    getSnapshot: () => state,
    subscribe: (listener: () => void) => {if (disposed) return () => {}; listeners.add(listener); return () => {listeners.delete(listener);};},
    open: async () => {
      const token = begin();
      await run(token, () => input.reader.read());
    },
    next: async () => {
      const shown = state.page;
      if (!shown) stale();
      const canonical = own(shown);
      const token = begin();
      await run(token, () => input.reader.next(canonical));
    },
    previous: async () => {
      const shown = state.page;
      if (!shown) stale();
      const canonical = own(shown);
      const token = begin();
      await run(token, () => input.reader.previous(canonical));
    },
    move: async (shown: WebQueueEditPage, entryId: string, direction: -1 | 1) => {
      requireLive();
      const canonical = own(shown);
      const anchor = anchorPosition;
      const index = shown.rows.findIndex(candidate => candidate.entryId === entryId);
      const rowAt = index < 0 ? undefined : shown.rows[index];
      const target = index < 0 ? undefined : shown.rows[index + direction];
      if (anchor === undefined || !rowAt || !target || !rowAt.available || rowAt.hidden || rowAt.current
        || !target.available || target.hidden || target.current || target.position !== rowAt.position + direction
        || rowAt.position <= anchor || target.position <= anchor) stale();
      // The canonical plan accepts only the offered contiguous upcoming block, in its new order.
      const entryIds = direction === 1 ? [target.entryId, rowAt.entryId] : [rowAt.entryId, target.entryId];
      const intent: V1QueueEditIntent = {action: 'reorder', entryIds: Object.freeze(entryIds)};
      const token = begin();
      issued.delete(shown); // the drawn mutation is consumed immediately
      await run(token, () => input.reader.edit(canonical, intent));
    },
    remove: async (shown: WebQueueEditPage, entryId: string) => {
      requireLive();
      const canonical = own(shown);
      const anchor = anchorPosition;
      const rowAt = shown.rows.find(candidate => candidate.entryId === entryId);
      if (anchor === undefined || !rowAt || !rowAt.available || rowAt.hidden || rowAt.current || rowAt.position <= anchor) stale();
      const token = begin();
      issued.delete(shown);
      await run(token, () => input.reader.edit(canonical, {action: 'remove', entryId}));
    },
    dispose: retire,
  });
}

/** One reading of the original output. Reads may call arbitrary getters, so the caller
 * supplies the same witness twice; a stale dock that became remote, a successor stack/source,
 * a changed fence/run/session or a group takeover reports unknown. */
export type WebQueueEditOutputRead = Readonly<{
  stack: object; source: object; current: boolean; fence?: PlaybackFence; presentation: boolean;
  dockLocalFence?: PlaybackFence; dockRemote: boolean; group: boolean;
  session?: Readonly<{id: string; role: string; kind: string; itemId?: string; versionId?: string; generation: number; queueId?: string; entryId?: string}>;
  viewQueueId?: string; viewEntryId?: string; playbackRun?: number;
}>;

export type WebQueueEditOutputProof = Readonly<{
  originalStack: object; originalSource: object; fence: PlaybackFence; target: WebListeningTarget;
  same(a: PlaybackFence | undefined, b: PlaybackFence): boolean;
  read(): WebQueueEditOutputRead;
}>;

const v1TargetOf = (target: WebListeningTarget): V1PlaybackTarget => Object.freeze({sessionId: target.sessionId, generation: target.sessionGeneration,
  playbackRun: target.playbackRun, itemId: target.itemId, versionId: target.versionId, queueId: target.queueId, entryId: target.entryId});

/** Strict admission: the original owner is re-proved after the command predicate (which can
 * replace or retire the runtime and still answer true), then the signal is checked last. */
export function admitCommand(owner: () => boolean, command: WebQueueEditLifetime): boolean {
  if (command.signal.aborted) return false;
  if (!owner()) return false;
  if (command.signal.aborted || !command.isCurrent()) return false;
  if (command.signal.aborted) return false;
  return owner() && !command.signal.aborted;
}

/** Local proof for one edit opening: exact server stack, local dock fence, no remote/group,
 * the original accepted session/queue occurrence/run/source/version, re-read after getters. */
export function localEditOutput(proof: WebQueueEditOutputProof): Readonly<{kind: 'local'; target: V1PlaybackTarget}> | Readonly<{kind: 'unknown'}> {
  const unknown = Object.freeze({kind: 'unknown' as const});
  const accept = (r: WebQueueEditOutputRead): boolean =>
    r.stack === proof.originalStack && r.source === proof.originalSource && r.current && r.presentation
    && !!r.fence && proof.same(r.fence, proof.fence)
    && !!r.dockLocalFence && proof.same(r.dockLocalFence, proof.fence) && !r.dockRemote && !r.group
    && !!r.session && r.session.role === 'local' && (r.session.kind === 'vod' || r.session.kind === 'audio')
    && r.session.id === proof.target.sessionId && r.session.itemId === proof.target.itemId
    && r.session.versionId === proof.target.versionId && r.session.generation === proof.target.sessionGeneration
    && r.session.queueId === proof.target.queueId && r.session.entryId === proof.target.entryId
    && r.viewQueueId === proof.target.queueId && r.viewEntryId === proof.target.entryId
    && r.playbackRun === proof.target.playbackRun;
  const first = proof.read();
  if (!accept(first)) return unknown;
  const second = proof.read();
  if (!accept(second)) return unknown;
  return Object.freeze({kind: 'local' as const, target: v1TargetOf(proof.target)});
}
