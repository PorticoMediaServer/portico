import {LibraryContentService, type ContentEntry, type ContentSection, type ContentScope} from '../../../../../../packages/client-core/src/library-content';
import type {ListeningJourney} from '../../../../../../packages/client-core/src/listening';
import {getContainerState, setContainerState} from '../../../../../../packages/client-core/src/container-state';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import {nativeDestinationLifetime} from '../lifetime';

export type AudiobookKind = 'book' | 'author' | 'book_series';
export type AudiobookPage = Readonly<{
  entity?: ContentEntry;
  sections: readonly ContentSection[];
  listening?: ListeningJourney;
  phase: 'idle' | 'loading' | 'ready' | 'error' | 'refresh-required';
  error?: unknown;
  finished: boolean;
  continuation?: Readonly<{sectionId: string; phase: 'loading' | 'error' | 'refresh-required'; error?: unknown}>;
}>;

export type AudiobookLease = Readonly<{signal: AbortSignal; isCurrent: () => boolean}>;

type Options = Readonly<{context: NativeShellBootstrapContext; kind: AudiobookKind; libraryId: string; id: string}>;
const playable = (entries: readonly ContentEntry[]) => entries.find(entry => entry.playback);

/** One real audiobook catalog page: the shared library content producer for a
 * book, author or series. It never reads the ereader's /v1/reading/items. */
export class NativeAudiobookController {
  private options: Options;
  private service: LibraryContentService;
  private lifetime: ReturnType<typeof nativeDestinationLifetime>;
  private state: AudiobookPage = Object.freeze({phase: 'idle', sections: [], finished: false});
  private listeners = new Set<() => void>();
  private controller?: AbortController;
  private disposed = false;
  private mutations = new Set<AbortController>();
  private pendingSections: {sectionId: string; lease: AudiobookLease; promise: Promise<void>; settle: () => void}[] = [];
  private recovering?: object;
  private activeSection?: {sectionId: string; lease: AudiobookLease; promise: Promise<void>; settle: () => void};
  constructor(options: Options) {
    this.options = options;
    this.lifetime = nativeDestinationLifetime(options.context);
    this.service = new LibraryContentService({api: options.context.api, scope: this.scope(), pageSize: 40, continuationRecovery: 'refresh-required'});
    this.service.subscribe(() => this.read());
  }
  private scope(): ContentScope {
    return this.lifetime.scope;
  }
  getSnapshot = (): AudiobookPage => this.state;
  contains = (entry: ContentEntry): boolean => this.current() && (this.state.entity === entry
    || this.state.sections.some(section => section.entries.includes(entry)));
  subscribe = (listener: () => void): (() => void) => {
    if (this.disposed) return () => {};
    this.listeners.add(listener);
    return () => {this.listeners.delete(listener);};
  };
  private current(): boolean {
    if (this.disposed) return false;
    const live = this.lifetime.current();
    return live === true && !this.disposed;
  }
  private live(lease: AudiobookLease): boolean {
    if (this.disposed || lease.signal.aborted) return false;
    try {
      if (!this.current() || this.disposed || lease.signal.aborted || lease.isCurrent() !== true) return false;
      if (this.disposed || lease.signal.aborted || !this.current()) return false;
      return !this.disposed && !lease.signal.aborted;
    } catch {return false;}
  }
  private publish(patch: Partial<AudiobookPage>): void {
    if (!this.current()) return;
    this.state = Object.freeze({...this.state, ...patch});
    for (const listener of [...this.listeners]) listener();
  }
  private read(): void {
    if (!this.current()) return;
    const snapshot = this.service.getSnapshot(), page = snapshot.projection;
    const continuing = !!this.activeSection || !!this.recovering;
    this.publish({phase: continuing && this.state.entity && page ? 'ready' : snapshot.phase,
      entity: page && snapshot.pagination.cursor && this.state.entity ? this.state.entity : page?.entity,
      sections: snapshot.appended, listening: page && snapshot.pagination.cursor && this.state.entity ? this.state.listening : page?.listening,
      error: continuing && page ? this.state.error : snapshot.error ?? undefined});
  }
  load = async (lease: AudiobookLease): Promise<void> => {
    if (!this.live(lease)) return;
    this.controller?.abort();
    if (!this.live(lease)) return;
    const controller = new AbortController();
    const abort = () => controller.abort();
    lease.signal.addEventListener('abort', abort, {once: true});
    this.controller = controller;
    try {
      if (!this.live(lease) || controller.signal.aborted) return;
      await this.service.select({view: this.options.kind, libraryId: this.options.libraryId, entityId: this.options.id}, {context: lease});
      if (!this.live(lease) || controller.signal.aborted) return;
      this.read();
      await this.refreshFinished(controller.signal);
    } catch (error) {
      if (this.live(lease) && !controller.signal.aborted) this.publish({phase: 'error', error});
    } finally {lease.signal.removeEventListener('abort', abort);}
  };
  next = (sectionId: string, lease: AudiobookLease): Promise<void> => {
    if (!this.live(lease) || !['chapters', 'files', 'books'].includes(sectionId)
      || this.state.continuation?.phase === 'error' || this.state.continuation?.phase === 'refresh-required'
      || !this.state.sections.find(section => section.id === sectionId)?.nextCursor) return Promise.resolve();
    for (const queued of [...this.pendingSections]) {
      if (!this.live(queued.lease)) {
        const index = this.pendingSections.indexOf(queued);
        if (index >= 0) this.pendingSections.splice(index, 1);
        queued.settle();
      }
    }
    if (!this.live(lease)) return Promise.resolve();
    const existing = [this.activeSection, ...this.pendingSections].find(item => item?.sectionId === sectionId && item.lease === lease);
    if (existing) return existing.promise;
    if (this.pendingSections.length >= 3 || this.pendingSections.some(item => item.lease !== lease)
      || (this.activeSection && this.activeSection.lease !== lease && this.live(this.activeSection.lease))) return Promise.resolve();
    let settle!: () => void;
    const promise = new Promise<void>(resolve => {settle = resolve;});
    const item = {sectionId, lease, promise, settle};
    this.pendingSections.push(item);
    const stop = () => {this.pendingSections = this.pendingSections.filter(value => value !== item); settle();};
    lease.signal.addEventListener('abort', stop, {once: true});
    void promise.finally(() => lease.signal.removeEventListener('abort', stop));
    this.drain();
    return promise;
  };
  private drain(): void {
    if (this.activeSection || !this.pendingSections.length || this.disposed) return;
    let candidate = this.pendingSections.shift();
    while (candidate && (!this.live(candidate.lease))) {candidate.settle(); candidate = this.pendingSections.shift();}
    const item = candidate;
    if (!item) return;
    if (this.service.getSnapshot().phase !== 'ready') {this.pendingSections.unshift(item); return;}
    this.activeSection = item;
    this.publish({continuation: {sectionId: item.sectionId, phase: 'loading'}});
    if (this.activeSection !== item || !this.live(item.lease)) {
      if (this.activeSection === item) {this.activeSection = undefined; this.publish({continuation: undefined});}
      item.settle(); this.drain(); return;
    }
    void this.service.next(item.sectionId, item.lease).catch(() => {}).finally(() => {
      if (this.activeSection !== item) return;
      this.activeSection = undefined;
      if (this.live(item.lease)) {
        const snapshot = this.service.getSnapshot();
        this.publish({continuation: snapshot.projection && (snapshot.phase === 'error' || snapshot.phase === 'refresh-required')
          ? {sectionId: item.sectionId, phase: snapshot.phase, error: snapshot.error} : undefined});
      } else this.publish({continuation: undefined});
      item.settle();
      if (!this.state.continuation) this.drain();
    });
  }
  recover = async (lease: AudiobookLease): Promise<void> => {
    const continuation = this.state.continuation;
    if (!continuation || continuation.phase === 'loading' || !this.live(lease)) return;
    const token = {};
    this.recovering = token;
    const retire = () => {if (this.recovering === token) {this.recovering = undefined; this.publish({continuation});}};
    lease.signal.addEventListener('abort', retire, {once: true});
    this.publish({continuation: {...continuation, phase: 'loading'}});
    try {if (this.recovering === token && this.live(lease)) await this.service.retry(lease);} catch { /* retired reads leave the committed cache alone */ }
    finally {lease.signal.removeEventListener('abort', retire);}
    if (this.recovering !== token) return;
    this.recovering = undefined;
    if (!this.live(lease)) {this.publish({continuation}); return;}
    const snapshot = this.service.getSnapshot();
    this.publish({continuation: snapshot.phase === 'error' || snapshot.phase === 'refresh-required'
      ? {sectionId: continuation.sectionId, phase: snapshot.phase, error: snapshot.error} : undefined});
    if (!this.state.continuation) this.drain();
  };
  /** The book container's finished mark, read from the same container route. */
  private async refreshFinished(signal: AbortSignal): Promise<void> {
    if (this.options.kind !== 'book') return;
    try {
      const state = await getContainerState(this.options.context.api, 'book', this.options.id, signal);
      if (this.current() && !signal.aborted) this.publish({finished: state.watched});
    } catch { /* the finished mark is optional; the page itself is still real */ }
  }
  markFinished = async (lease: AudiobookLease): Promise<void> => {
    const operation = new AbortController();
    const abort = () => operation.abort();
    lease.signal.addEventListener('abort', abort, {once: true});
    this.mutations.add(operation);
    const live = () => !operation.signal.aborted && this.live(lease) && !operation.signal.aborted;
    try {
      if (!live() || this.options.kind !== 'book') return;
      const state = await getContainerState(this.options.context.api, 'book', this.options.id, operation.signal);
      if (!live()) return;
      const next = await setContainerState(this.options.context.api, 'book', this.options.id, {expectedRevision: state.revision, watched: true}, operation.signal);
      if (live()) this.publish({finished: next.watched});
    } catch (error) {
      if (live()) throw error;
    } finally {
      lease.signal.removeEventListener('abort', abort);
      this.mutations.delete(operation);
    }
  };
  private start = async (lease: AudiobookLease, entry: ContentEntry | undefined, startSeconds?: number): Promise<void> => {
    if (!this.live(lease) || !entry?.playback || !this.contains(entry)) return;
    const target = {...entry.playback, ...(startSeconds === undefined ? {} : {startSeconds})};
    await this.options.context.playback.start({entry, target, startIntent: startSeconds === undefined ? 'resume' : 'explicit', lifetime: {signal: lease.signal, isCurrent: () => this.live(lease) && this.contains(entry)}});
  };
  private books(): readonly ContentEntry[] {
    return this.state.sections.find(section => section.id === 'books')?.entries ?? [];
  }
  resumeBook = (lease: AudiobookLease, entry: ContentEntry): Promise<void> => {
    const progress = entry.bookProgress, owner = this.state.entity;
    if (!this.live(lease) || this.state.phase !== 'ready' || !owner
      || !this.books().includes(entry) || entry.kind !== 'book' || (entry.libraryId !== undefined && entry.libraryId !== this.options.libraryId)
      || progress?.bookId !== entry.id || !progress.itemId || !Number.isFinite(progress.partPositionMs) || progress.partPositionMs < 0) return Promise.resolve();
    const startSeconds = progress.partPositionMs / 1000;
    return this.options.context.playback.start({entry, target: {itemId: progress.itemId, startSeconds}, lifetime: {signal: lease.signal,
      isCurrent: () => this.live(lease) && this.state.phase === 'ready'
        && this.state.entity === owner && this.books().includes(entry) && entry.bookProgress === progress}});
  };
  private chapters(): readonly ContentEntry[] {
    return [...(this.state.sections.find(section => section.id === 'chapters')?.entries ?? []), ...(this.state.sections.find(section => section.id === 'files')?.entries ?? [])];
  }
  resume = (lease: AudiobookLease): Promise<void> => {
    const entry = this.state.entity, listening = this.state.listening, resume = listening?.resume;
    if (!this.live(lease) || this.state.phase !== 'ready'
      || this.options.kind !== 'book' || entry?.kind !== 'book' || entry.id !== this.options.id
      || (entry.libraryId !== undefined && entry.libraryId !== this.options.libraryId) || listening?.target.kind !== 'book'
      || listening.target.id !== entry.id || listening.target.libraryId !== this.options.libraryId
      || !listening.actions.includes('play') || listening.resumeUnavailable || !resume?.itemId
      || !Number.isFinite(resume.positionSeconds) || resume.positionSeconds < 0) return Promise.resolve();
    return this.options.context.playback.start({entry, target: {itemId: resume.itemId, startSeconds: resume.positionSeconds},
      lifetime: {signal: lease.signal, isCurrent: () => this.live(lease)
        && this.state.phase === 'ready' && this.state.entity === entry && this.state.listening === listening}});
  };
  startOver = (lease: AudiobookLease): Promise<void> => this.start(lease, playable(this.chapters()), 0);
  playChapter = (lease: AudiobookLease, entry: ContentEntry): Promise<void> => this.start(lease, entry, entry.playback?.startSeconds);
  dispose = (): void => {
    if (this.disposed) return;
    this.disposed = true;
    this.controller?.abort();
    for (const item of this.pendingSections) item.settle();
    this.pendingSections = [];
    for (const operation of this.mutations) operation.abort();
    this.mutations.clear();
    this.service.dispose();
    this.listeners.clear();
  };
}

/** Read-only cached data and commands scoped to one rendered opening. */
export type AudiobookScreenController = Readonly<{
  getSnapshot: NativeAudiobookController['getSnapshot']; subscribe: NativeAudiobookController['subscribe'];
  load: () => Promise<void>; markFinished: () => Promise<void>; resume: () => Promise<void>;
  next: (sectionId: string) => Promise<void>; recover: () => Promise<void>;
  startOver: () => Promise<void>; playChapter: (entry: ContentEntry) => Promise<void>;
  resumeBook: (entry: ContentEntry) => Promise<void>;
}>;
