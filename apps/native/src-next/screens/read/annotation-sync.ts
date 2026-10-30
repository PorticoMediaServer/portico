import {ReadingError, type LocalSession, type ReadingClient, type ReadingSession} from '../../../../../packages/client-core/src';
import type {ReadingAnnotation} from '../../../../../packages/client-core/src/reading-annotations';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {sameAuthority, type ShellAuthority} from '../../shell/model';
import {
  NativeAnnotationJournal, nativeAnnotationJournalCounts, nativeAnnotationIdentity,
  type NativeAnnotationGroup, type NativeAnnotationIdentity, type NativeAnnotationOperation, type NativeAnnotationResolution,
  type NativeAnnotationUnresolved,
} from './annotation-journal';

/** The reconnect merge for the private offline annotation journal (P1-READ-OFFLINE-ANNOTATION-SYNC).
 *
 * Union by annotation id, last edit wins. The server's own `updatedAt` is the only
 * trustworthy clock: a queued edit is applied only while the server entry is still the
 * one it was based on, so an edit made elsewhere always wins and a deletion is never
 * resurrected by older offline work. Each attempt reads the annotation set and uses its
 * fresh revision as the CAS token — a retry never reuses the token of the attempt that
 * was interrupted, and the queued payload is always the original captured one.
 *
 * A replaced source, a profile change, an ended session or a withheld capability keeps
 * the payload locally and reports it as unresolved. Nothing is re-anchored, rebased onto
 * new coordinates or uploaded under another source, library or profile. */

export type NativeAnnotationSyncState = Readonly<{
  phase: 'idle' | 'syncing' | 'error';
  queued: number;
  unresolved: number;
  error?: unknown;
}>;
type Identity = Readonly<{sessionFamilyId: string; viewer: LocalSession['viewer']; authority: ShellAuthority}>;

const PAGE_LIMIT = 6, SET_LIMIT = 256, GROUP_DEADLINE_MS = 20_000;
const semantic = new Set(['invalid_document', 'invalid_response', 'invalid_revision', 'invalid_location', 'not_permitted', 'conflict', 'cancelled', 'response_size', 'source_changed']);
const retryable = (error: unknown): boolean => !(error instanceof ReadingError) || error.status === undefined && !semantic.has(error.code);
const displaced = (error: unknown): boolean => error instanceof ReadingError && (error.code === 'conflict' || error.code === 'source_changed');
type Decision = Readonly<{kind: 'apply'}> | Readonly<{kind: 'resolved'}> | Readonly<{kind: 'unresolved'; reason: NativeAnnotationUnresolved}>;
type PageState = Readonly<{revision: number; entries: readonly ReadingAnnotation[]}>;
/** The already-open reader's own annotation port. Reused when it is the same source and
 * revision, so a merge never opens a second session beside the person's live one. */
export type NativeAnnotationLivePort = Pick<ReadingSession, 'manifest' | 'signal' | 'annotations' | 'mutateAnnotation'>;
/** A retried operation whose first attempt committed but whose answer was lost is not a
 * competing edit: the same id with the same content is this device's own write. */
function sameAnnotation(entry: ReadingAnnotation, content: NativeAnnotationOperation['content']): boolean {
  if (!content || entry.kind !== content.kind || entry.text !== content.text || entry.note !== content.note || entry.colour !== content.colour) return false;
  if (entry.location.format !== content.location.format || entry.location.progression !== content.location.progression
    || (entry.location.format === 'epub' ? entry.location.cfi !== content.location.cfi || entry.location.section !== content.location.section
      : entry.location.pageIndex !== content.location.pageIndex)) return false;
  const rects = entry.rects ?? [], wanted = content.rects ?? [];
  return rects.length === wanted.length && rects.every((rect, index) => rect.x === wanted[index]!.x && rect.y === wanted[index]!.y
    && rect.width === wanted[index]!.width && rect.height === wanted[index]!.height);
}

/** The exact merge rule, kept in one place so the retry path cannot drift from the first read. */
export function nativeAnnotationDecision(state: PageState, operation: NativeAnnotationOperation): Decision {
  const entry = state.entries.find(value => value.id === operation.id);
  if (operation.delete) {
    if (!entry) return {kind: 'resolved'}; // Already deleted: the tombstone stands, nothing to resurrect.
    if (entry.updatedAt > operation.baseUpdatedAt) return {kind: 'unresolved', reason: 'server-newer'};
    return {kind: 'apply'};
  }
  if (!operation.content) return {kind: 'resolved'};
  if (!entry) return operation.baseUpdatedAt > 0 ? {kind: 'unresolved', reason: 'deleted-elsewhere'} : {kind: 'apply'};
  if (sameAnnotation(entry, operation.content)) return {kind: 'resolved'}; // Our own committed retry.
  // Equal timestamps are the untouched base; anything newer is another edit that wins.
  return entry.updatedAt > operation.baseUpdatedAt ? {kind: 'unresolved', reason: 'server-newer'} : {kind: 'apply'};
}

export class NativeAnnotationSync {
  private readonly context: NativeShellBootstrapContext;
  private readonly journal: NativeAnnotationJournal;
  private readonly listeners = new Set<() => void>();
  private state: NativeAnnotationSyncState = Object.freeze({phase: 'idle', queued: 0, unresolved: 0});
  private running?: Promise<void>;
  private life = new AbortController();
  private disposed = false;
  constructor(context: NativeShellBootstrapContext, journal = new NativeAnnotationJournal()) {this.context = context; this.journal = journal;}
  getSnapshot = () => this.state;
  subscribe = (listener: () => void): (() => void) => {if (this.disposed) return () => {}; this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private publish(next: NativeAnnotationSyncState) {
    if (this.disposed) return;
    this.state = Object.freeze(next);
    for (const listener of [...this.listeners]) listener();
  }
  private capture(): Identity | undefined {
    const session = this.context.session();
    if (!session || !this.context.current()) return undefined;
    return Object.freeze({sessionFamilyId: session.sessionFamilyId, viewer: Object.freeze({...session.viewer}), authority: Object.freeze({...this.context.authority()})});
  }
  /** Fresh authorization for this exact profile and session family, checked before every write. */
  private fresh(identity: Identity): boolean {
    const live = this.context.session();
    return !this.disposed && !this.life.signal.aborted && this.context.current() && !!live
      && live.sessionFamilyId === identity.sessionFamilyId && sameAuthority(identity.authority, this.context.authority())
      && live.viewer.authority === identity.viewer.authority && live.viewer.serverId === identity.viewer.serverId
      && live.viewer.accountId === identity.viewer.accountId && live.viewer.profileId === identity.viewer.profileId;
  }
  private assertFresh(identity: Identity): void {
    if (!this.fresh(identity)) throw new ReadingError('cancelled');
  }
  /** Only the annotations method is needed; the source and lifetime stay captured by the caller. */
  private async pages(session: Pick<NativeAnnotationLivePort, 'annotations'>, source: NativeAnnotationGroup['source'],
    signal: AbortSignal, assert: () => void): Promise<PageState> {
    let cursor = '', revision: number | undefined;
    const entries: ReadingAnnotation[] = [], ids = new Set<string>(), cursors = new Set<string>();
    for (let page = 0; page < PAGE_LIMIT; page++) {
      assert();
      const out = await session.annotations(cursor, revision, signal);
      assert();
      if (out.sourceId !== source.sourceId || out.sourceRevision !== source.sourceRevision) throw new ReadingError('source_changed');
      if (revision !== undefined && out.revision !== revision) throw new ReadingError('conflict');
      revision = out.revision;
      if (entries.length + out.entries.length > SET_LIMIT) throw new ReadingError('invalid_document');
      for (const entry of out.entries) {
        if (ids.has(entry.id)) throw new ReadingError('invalid_document');
        ids.add(entry.id);entries.push(entry);
      }
      if (!out.nextCursor) return {revision, entries};
      if (cursors.has(out.nextCursor)) throw new ReadingError('invalid_document');
      cursors.add(out.nextCursor);cursor = out.nextCursor;
    }
    // A bounded prefix is never a complete annotation set or cache/merge base.
    throw new ReadingError('invalid_document');
  }
  /** Returns true when transport failure means the whole pass should stop and wait for a reconnect. */
  private async syncGroup(identity: Identity, group: NativeAnnotationGroup, live?: NativeAnnotationLivePort): Promise<boolean> {
    const deadline = new AbortController(), timer = setTimeout(() => deadline.abort(new ReadingError('cancelled')), GROUP_DEADLINE_MS);
    const signal = deadline.signal;
    const journalIdentity: NativeAnnotationIdentity = nativeAnnotationIdentity(identity.viewer);
    const retire = () => deadline.abort(new ReadingError('cancelled'));
    this.life.signal.addEventListener('abort', retire, {once: true});
    if (this.life.signal.aborted) retire();
    let releaseSource = () => {};
    let client: ReadingClient | undefined, session: NativeAnnotationLivePort | undefined, owned: ReadingSession | undefined;
    const reuse = !!live && !live.signal.aborted && live.manifest.canAnnotate === true
      && typeof live.annotations === 'function' && typeof live.mutateAnnotation === 'function'
      && live.manifest.sourceId === group.source.sourceId && live.manifest.revision === group.source.sourceRevision;
    const assertCurrent = () => {
      this.assertFresh(identity);
      if (signal.aborted || session?.signal.aborted) throw new ReadingError('cancelled');
    };
    const mark = async (reason: NativeAnnotationUnresolved) => {
      const resolutions: NativeAnnotationResolution[] = group.operations.map(operation => ({id: operation.id, observedAt: operation.observedAt, outcome: 'unresolved', reason}));
      assertCurrent();
      await this.journal.resolve(journalIdentity, group.source, resolutions, assertCurrent);
      assertCurrent();
    };
    try {
      assertCurrent();
      if (reuse) session = live;
      else {
        client = this.context.api.reading({viewer: identity.viewer, isCurrent: () => this.fresh(identity), signal});
        const versions = await client.versions(group.source.itemId, signal);
        assertCurrent();
        if (!versions.versions.some(value => value.sourceId === group.source.sourceId)) {
          await mark('stale-source'); // No such downloadable/current source for this item.
          return false;
        }
        session = owned = await client.open({itemId: group.source.itemId, sourceId: group.source.sourceId}, signal);
      }
      assertCurrent();
      const sourceSignal = session.signal;
      sourceSignal.addEventListener('abort', retire, {once: true});
      releaseSource = () => sourceSignal.removeEventListener('abort', retire);
      if (sourceSignal.aborted) retire();
      assertCurrent();
      const manifest = session.manifest;
      if (manifest.itemId !== group.source.itemId || manifest.sourceId !== group.source.sourceId) {await mark('source-changed'); return false;}
      if (manifest.revision !== group.source.sourceRevision) {await mark('source-changed'); return false;} // Replaced source: keep the captured quote, never re-anchor.
      if (manifest.canAnnotate !== true) {await mark('refused'); return false;}
      let state = await this.pages(session, group.source, signal, assertCurrent);
      // Keep the last server page this device saw so an offline open of the same source can
      // show it beside queued edits; the cache is a convenience, never a record of truth.
      await this.journal.cache(journalIdentity, group.source, state.revision, state.entries, assertCurrent);
      for (const operation of group.operations) {
        if (!this.fresh(identity) || signal.aborted) return true;
        let decision = nativeAnnotationDecision(state, operation);
        if (decision.kind === 'apply') {
          const mutation = operation.delete
            ? {operation: 'delete' as const, id: operation.id, expectedRevision: state.revision}
            : {...operation.content!, operation: 'upsert' as const, expectedRevision: state.revision};
          try {
            const saved = await session.mutateAnnotation(mutation, signal);
            assertCurrent();
            if (saved.revision !== state.revision + 1) throw new ReadingError('invalid_document');
            state = await this.pages(session, group.source, signal, assertCurrent);
            await this.journal.cache(journalIdentity, group.source, state.revision, state.entries, assertCurrent);
            decision = {kind: 'resolved'};
          } catch (error) {
            if (signal.aborted || !this.fresh(identity) || (error instanceof ReadingError && error.code === 'cancelled')) return true;
            if (displaced(error)) {
              // One fresh read may explain the displacement; the payload is unchanged.
              state = await this.pages(session, group.source, signal, assertCurrent);
              const again = nativeAnnotationDecision(state, operation);
              if (again.kind !== 'apply') decision = again;
              else {
                const retry = operation.delete
                  ? {operation: 'delete' as const, id: operation.id, expectedRevision: state.revision}
                  : {...operation.content!, operation: 'upsert' as const, expectedRevision: state.revision};
                try {
                  const saved = await session.mutateAnnotation(retry, signal);
                  assertCurrent();
                  if (saved.revision !== state.revision + 1) throw new ReadingError('invalid_document');
                  state = await this.pages(session, group.source, signal, assertCurrent);
                  decision = {kind: 'resolved'};
                } catch (second) {
                  if (signal.aborted || !this.fresh(identity) || (second instanceof ReadingError && second.code === 'cancelled')) return true;
                  if (retryable(second)) throw second;
                  decision = {kind: 'unresolved', reason: displaced(second) ? 'conflict' : 'refused'};
                }
              }
            } else if (retryable(error)) {
              throw error; // Offline or transport: keep every remaining operation queued for the next reconnect.
            } else {
              decision = {kind: 'unresolved', reason: 'refused'};
            }
          }
        }
        assertCurrent();
        const resolution: NativeAnnotationResolution = decision.kind === 'resolved'
          ? {id: operation.id, observedAt: operation.observedAt, outcome: 'resolved'}
          : {id: operation.id, observedAt: operation.observedAt, outcome: 'unresolved', reason: decision.reason};
        await this.journal.resolve(journalIdentity, group.source, [resolution], assertCurrent);
        assertCurrent();
      }
    } catch (error) {
      if (this.fresh(identity) && !signal.aborted && error instanceof ReadingError && (error.code === 'not_permitted' || error.code === 'invalid_document')) {
        await mark('refused'); return false;
      }
      if (this.fresh(identity) && !signal.aborted) this.publish({...this.state, phase: 'error', error});
      return !signal.aborted && this.fresh(identity); // Transport/offline: stop the pass and keep every remaining operation.
    } finally {
      clearTimeout(timer);releaseSource();this.life.signal.removeEventListener('abort', retire);
      // Close only our preview, with the original client's authority checks still intact.
      // Never close the reused live reader, and never skip local retirement on a deadline.
      const cleanup = new AbortController(), closeTimer = setTimeout(() => cleanup.abort(), 2000);
      try {if (owned) await owned.close(cleanup.signal).catch(() => {});}
      finally {clearTimeout(closeTimer);client?.dispose();}
    }
    return false;
  }
  /** Retry every queued source for the current profile. Never reports success for a queued op. */
  flush = (live?: NativeAnnotationLivePort): Promise<void> => {
    if (this.disposed || this.life.signal.aborted) return Promise.resolve();
    if (this.running) return this.running;
    const identity = this.capture();
    if (!identity) return Promise.resolve();
    const journalIdentity: NativeAnnotationIdentity = nativeAnnotationIdentity(identity.viewer);
    const run = (async () => {
      this.publish({...this.state, phase: 'syncing'});
      try {
        const groups = await this.journal.list(journalIdentity);
        for (const group of groups) {
          if (!this.fresh(identity)) break;
          if (await this.syncGroup(identity, group, live)) break;
        }
        if (!this.fresh(identity)) {this.publish({phase: 'idle', queued: 0, unresolved: 0}); return;}
        const remaining = await this.journal.list(journalIdentity);
        this.assertFresh(identity);
        const counts = nativeAnnotationJournalCounts(remaining);
        this.publish({phase: 'idle', ...counts});
      } catch (error) {
        if (!this.fresh(identity)) {this.publish({phase: 'idle', queued: 0, unresolved: 0}); return;}
        const groups = await this.journal.list(journalIdentity).catch(() => []);
        if (!this.fresh(identity)) {this.publish({phase: 'idle', queued: 0, unresolved: 0}); return;}
        const counts = nativeAnnotationJournalCounts(groups);
        this.publish({phase: 'error', ...counts, error});
      }
    })().finally(() => {if (this.running === run) this.running = undefined;});
    this.running = run;
    return run;
  };
  dispose() {
    if (this.disposed) return;
    this.disposed = true; this.life.abort(new ReadingError('cancelled')); this.listeners.clear();
    this.state = Object.freeze({phase: 'idle', queued: 0, unresolved: 0});
  }
}
