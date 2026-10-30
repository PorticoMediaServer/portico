import type {ReaderAdapter, ReaderDocument, ReaderFormat, ReaderLocation, ReaderNavigation, ReaderMetadata,
  ReaderSearchOptions, ReaderSearchResult, ReaderSnapshot, ReaderSource, ReaderView, ReaderViewport,
  ReaderZoom, ReaderSurfaceInput, ReaderPageImage, ReaderContentsTarget, ReaderMotionPolicy} from './ports.ts';
import {ReaderError, readerErrorCode, throwIfAborted} from './errors.ts';
import {validateSource} from './source.ts';
import {validateViewport} from './layout.ts';
import {captureReaderMotion} from './motion-policy.ts';
import {abortScope} from './abort.ts';

type Session<P extends object> = {abort: AbortController; detachAuthority?:()=>void; document?: ReaderDocument<P>; view?: ReaderView<P>; tail: Promise<void>; turn?: Promise<void>};
const empty = Object.freeze([]);
function copy<P extends object>(value: P): Readonly<P> { return Object.freeze({...value}); }
export function validateLocation(location: ReaderLocation, format: ReaderFormat): void {
  if (location.format !== format || !Number.isFinite(location.progression) || location.progression < 0 || location.progression > 1
    || (location.format === 'epub' ? !/^epubcfi\(.+\)$/.test(location.cfi) : !Number.isSafeInteger(location.page) || location.page < 0))
    throw new ReaderError('invalid_location');
}

/** Session scope owns all overrides, asynchronous work and mounted resources.
 * Incoming defaults remain live even when an individual field is overridden.
 * The controller never receives a persistence port or a credential. */
export class ReaderController<P extends object> {
  private readonly adapters = new Map<ReaderFormat, ReaderAdapter<P>>();
  private readonly surfaceListeners = new Set<(event: ReaderSurfaceInput) => void>();
  private readonly listeners = new Set<() => void>();
  private base: Readonly<P>;
  private overrides: Partial<P> = {};
  private session?: Session<P>;
  private lifecycleVersion = 0;
  private viewport: ReaderViewport = Object.freeze({width: 1, height: 1});
  private snapshot: ReaderSnapshot<P>;
  constructor(options: Readonly<{adapters: readonly ReaderAdapter<P>[]; preferences: Readonly<P>}>) {
    for (const adapter of options.adapters) {
      if (this.adapters.has(adapter.format)) throw new ReaderError('unsupported_format');
      this.adapters.set(adapter.format, adapter);
    }
    this.base = copy(options.preferences);
    this.snapshot = Object.freeze({status: 'closed', zoom: null, canTurnAtFit: true, toc: empty, location: null, selection: null,
      preferences: this.base, overriddenKeys: empty});
  }
  /** Stable until an actual publication; safe for useSyncExternalStore. */
  getSnapshot = (): ReaderSnapshot<P> => this.snapshot;
  subscribe = (listener: () => void): (() => void) => { this.listeners.add(listener); return () => this.listeners.delete(listener); };
  subscribeSurfaceInput = (listener: (event: ReaderSurfaceInput) => void): (() => void) => {
    this.surfaceListeners.add(listener); return () => this.surfaceListeners.delete(listener);
  };
  refreshViewState() {
    const view = this.session?.view;
    const zoom = view?.getZoom?.() ?? null, canTurnAtFit = view?.canTurnAtFit?.() ?? true;
    const prior = this.snapshot.zoom;
    if (canTurnAtFit === this.snapshot.canTurnAtFit && (zoom === null ? prior === null
      : prior !== null && zoom.scale === prior.scale && zoom.fit === prior.fit)) return;
    this.publish({zoom: zoom === null ? null : copy(zoom), canTurnAtFit});
  }
  private publish(patch: Partial<ReaderSnapshot<P>>) {
    this.snapshot = Object.freeze({...this.snapshot, ...patch});
    for (const listener of this.listeners) listener();
  }
  private readonly searchResults = new WeakMap<ReaderSearchResult, {session: Session<P>; signal: AbortSignal}>();
  private current(session: Session<P>) { return this.session === session && !session.abort.signal.aborted; }
  private effective(): Readonly<P> { return copy({...this.base, ...this.overrides}); }
  private publishPreferences() {
    this.publish({preferences: this.effective(), overriddenKeys: Object.freeze(Object.keys(this.overrides) as (keyof P)[])});
  }
  /** Canonical consumers may refresh their projection for each new book. */
  protected preferencesAtOpen(): Readonly<P> { return this.base; }
  async open(options: Readonly<{source: ReaderSource; format: ReaderFormat; container: HTMLElement; location?: ReaderLocation; viewport?: ReaderViewport; metadata?: ReaderMetadata;
    /** Retire cached pages, DOM and engine work with the authorised session. */
    signal?:AbortSignal; motion?:ReaderMotionPolicy}>): Promise<void> {
    const originalMotion = captureReaderMotion(options.motion);
    const lifecycleVersion = ++this.lifecycleVersion;
    // Invalidate before waiting for disposal, so simultaneous opens cannot adopt
    // a stale document/view. A late adapter result is disposed in its own turn.
    const previous = this.session;
    this.session = undefined;
    previous?.abort.abort();
    const session: Session<P> = {abort: new AbortController(), tail: Promise.resolve()};
    this.session = session;
    this.overrides = {};
    this.publish({status: 'opening', zoom: null, canTurnAtFit: true, format: options.format, sourceId: options.source.id, sourceRevision: options.source.revision, metadata: undefined,
      toc: empty, location: null, selection: null, error: undefined, preferences: this.base, overriddenKeys: empty});
    if(options.signal) {
      const authority=options.signal;
      const retire=()=>{if(this.session===session)void this.close().catch(()=>{});};
      session.detachAuthority=()=>authority.removeEventListener('abort',retire);
      authority.addEventListener('abort',retire,{once:true});
      if(authority.aborted)retire();
    }
    try {
      await this.disposeSession(previous);
      if (!this.current(session)) return;
      const preferences = this.preferencesAtOpen();
      if (preferences !== this.base) { this.base = copy(preferences); this.publishPreferences(); }
      validateSource(options.source);
      if (options.location) validateLocation(options.location, options.format);
      const adapter = this.adapters.get(options.format);
      if (!adapter) throw new ReaderError('unsupported_format');
      this.viewport = Object.freeze(options.viewport ?? {width: options.container.clientWidth, height: options.container.clientHeight});
      validateViewport(this.viewport);
      const document = await adapter.open({source: options.source, signal: session.abort.signal});
      if (!this.current(session)) { await document.dispose(); return; }
      session.document = document;
      if (document.format !== options.format || document.id !== options.source.id || document.revision !== options.source.revision) throw new ReaderError('invalid_book');
      const metadata = copy(options.metadata === undefined ? document.metadata : {...document.metadata, ...options.metadata,
        title: options.metadata.title, author: options.metadata.author, language: options.metadata.language});
      const view = await document.mount({container: options.container, preferences: this.snapshot.preferences,
        viewport: this.viewport, metadata, motion: originalMotion, signal: session.abort.signal,
        onLocation: location => {
          if (!this.current(session)) return;
          validateLocation(location, options.format);
          this.publish({location: copy(location)});
        },
        onSurfaceInput: event => {
          if (!this.current(session)) return;
          // Fixed views may zoom internally before reporting gesture activity.
          // Chrome must observe measured fit/zoom before deciding edge taps.
          if (event.kind === 'activity') this.refreshViewState();
          for (const listener of this.surfaceListeners) {
            if (!this.current(session)) break;
            listener(event);
          }
        },
        onSelection: selection => {
          if (!this.current(session)) return;
          this.publish({selection: selection ? Object.freeze({...selection, rects: Object.freeze(selection.rects.map(copy)), ...(selection.pageRects ? {pageRects:Object.freeze(selection.pageRects.map(copy))} : {})}) : null});
        }});
      if (!this.current(session)) { await view.dispose(); return; }
      session.view = view;
      // Defaults may have changed while the adapter was opening/mounting.
      await this.enqueue(async (mounted, active) => {
        await mounted.applyPreferences(this.snapshot.preferences, active);
        if (options.location) await mounted.navigate({kind: 'location', location: options.location}, active);
      });
      if (!this.current(session)) return;
      const location = view.getLocation();
      if (location) validateLocation(location, options.format);
      this.refreshViewState();
      this.publish({status: 'ready', metadata, toc: freezeToc(document.toc),
        location: location ? copy(location) : this.snapshot.location});
      // Resolving EPUB outline anchors can read every chapter. It must never
      // hold first paint, navigation or in-book search behind those reads.
      if (document.loadToc && this.current(session)) {
        void Promise.resolve().then(() => {
          if (this.current(session)) return document.loadToc!(session.abort.signal);
        }).then(toc => {
          if (!toc || !this.current(session)) return;
          const location = view.getLocation();
          if (location) validateLocation(location, options.format);
          this.publish({toc: freezeToc(toc), location: location ? copy(location) : this.snapshot.location});
        }).catch(error => {
          if (this.current(session)) this.publish({error: readerErrorCode(error)});
        });
      }
    } catch (error) {
      if (!this.current(session)) return;
      session.abort.abort();
      this.session = undefined;
      await this.disposeSession(session);
      // A replacement or explicit close can complete while failed resources
      // retire. That newer lifecycle owns the snapshot, including when closed.
      if (this.lifecycleVersion !== lifecycleVersion) return;
      this.publish({status: 'error', error: readerErrorCode(error), selection: null});
    }
  }
  private async disposeSession(session?: Session<P>) {
    if (!session) return;
    session.detachAuthority?.();
    session.abort.abort();
    // A view error must not prevent its document/ZIP/blob disposal.
    try { await session.view?.dispose(); }
    finally { await session.document?.dispose(); }
  }
  async close(): Promise<void> {
    this.lifecycleVersion++;
    const session = this.session;
    this.session = undefined;
    session?.abort.abort(); this.overrides = {};
    this.snapshot = Object.freeze({status: 'closed', zoom: null, canTurnAtFit: true, toc: empty, location: null, selection: null,
      preferences: this.base, overriddenKeys: empty});
    for (const listener of this.listeners) listener();
    await this.disposeSession(session);
  }
  private enqueue(operation: (view: ReaderView<P>, signal: AbortSignal) => Promise<void>): Promise<void> {
    const session = this.session;
    if (!session?.view || !this.current(session)) return Promise.reject(new ReaderError('closed'));
    const next = session.tail.then(async () => {
      throwIfAborted(session.abort.signal);
      await operation(session.view!, session.abort.signal);
      if (this.current(session)) this.refreshViewState();
      throwIfAborted(session.abort.signal);
    });
    session.tail = next.catch(error => { if (this.current(session)) this.publish({error: readerErrorCode(error)}); });
    return next;
  }
  navigate(target: ReaderNavigation, callerSignal?: AbortSignal): Promise<void> {
    const captured: ReaderNavigation = target.kind === 'location'
      ? Object.freeze({kind: 'location', location: copy(target.location)}) : Object.freeze({...target});
    if (captured.kind === 'location') validateLocation(captured.location, this.snapshot.format!);
    if (captured.kind === 'progression' && (!Number.isFinite(captured.value) || captured.value < 0 || captured.value > 1))
      return Promise.reject(new ReaderError('invalid_location'));
    const session = this.session, turn = captured.kind === 'next' || captured.kind === 'previous';
    // Physical held keys/taps never accumulate behind an in-flight page turn.
    if (turn && session?.turn && this.current(session)) return callerSignal?.aborted ? Promise.reject(new ReaderError('aborted')) : Promise.resolve();
    const operation = this.enqueue(async (view, signal) => {
      const task = abortScope([signal, ...(callerSignal ? [callerSignal] : [])]);
      try {throwIfAborted(task.signal); await view.navigate(captured, task.signal); throwIfAborted(task.signal);}
      finally {task.dispose();}
    });
    if (turn && session) {
      session.turn = operation;
      const clear = () => {if (session.turn === operation) session.turn = undefined;};
      void operation.then(clear, clear);
    }
    return operation;
  }
  /** Only an actual result of this exact session/query can request a search mark. */
  navigateSearchResult(result: ReaderSearchResult, callerSignal?: AbortSignal): Promise<void> {
    const grant = this.searchResults.get(result);
    if (!grant || !this.current(grant.session) || grant.signal.aborted) return Promise.reject(new ReaderError('invalid_location'));
    // A display-only hit is still bound to this query/session, but it has no
    // proved engine location; navigating it would invent one.
    if (result.location === undefined) return Promise.reject(new ReaderError('invalid_location'));
    const location = copy(result.location);
    return this.enqueue(async (view, signal) => {
      const task = abortScope([signal, grant.signal, ...(callerSignal ? [callerSignal] : [])]);
      try {
        throwIfAborted(task.signal);
        if (!this.current(grant.session)) throw new ReaderError('closed');
        await view.navigate({kind: 'location', location}, task.signal);
        throwIfAborted(task.signal);
        if (!this.current(grant.session)) throw new ReaderError('closed');
        await view.showSearchResult?.(location, grant.signal);
        throwIfAborted(task.signal);
      } finally {task.dispose();}
    });
  }
  clearSearchResult(): Promise<void> {
    return this.enqueue(async (view, signal) => {await view.showSearchResult?.(null, signal);});
  }
  navigateContents(target: ReaderContentsTarget, callerSignal?: AbortSignal): Promise<void> {
    const session = this.session, document = session?.document;
    const captured = Object.freeze({...target});
    if (!Number.isSafeInteger(captured.index) || captured.index < 0) return Promise.reject(new ReaderError('invalid_location'));
    if (!session || !document || !this.current(session)) return Promise.reject(new ReaderError('closed'));
    if (document.id !== captured.sourceId || document.revision !== captured.sourceRevision) return Promise.reject(new ReaderError('invalid_source'));
    if (document.format !== 'epub' || !document.resolveContents) return Promise.reject(new ReaderError('unsupported_operation'));
    const resolve = document.resolveContents.bind(document);
    return this.enqueue(async (view, signal) => {
      const task = abortScope([signal, ...(callerSignal ? [callerSignal] : [])]);
      try {
        throwIfAborted(task.signal);
        if (!this.current(session) || session.document !== document) throw new ReaderError('closed');
        if (document.id !== captured.sourceId || document.revision !== captured.sourceRevision) throw new ReaderError('invalid_source');
        const location = await resolve(captured.index, task.signal);
        throwIfAborted(task.signal);
        if (!this.current(session) || session.document !== document) throw new ReaderError('closed');
        if (document.id !== captured.sourceId || document.revision !== captured.sourceRevision) throw new ReaderError('invalid_source');
        validateLocation(location, 'epub');
        await view.navigate({kind: 'location', location}, task.signal);
      } finally { task.dispose(); }
    });
  }
  updatePreferences(preferences: Readonly<P>): Promise<void> {
    this.base = copy(preferences); this.publishPreferences();
    return this.session?.view ? this.enqueue((view, signal) => view.applyPreferences(this.snapshot.preferences, signal)) : Promise.resolve();
  }
  setOverride(patch: Partial<P>): Promise<void> {
    if (!this.session || this.snapshot.status === 'error') return Promise.reject(new ReaderError('closed'));
    for (const key of Object.keys(patch) as (keyof P)[]) if (!(key in this.base) || patch[key] === undefined)
      return Promise.reject(new ReaderError('invalid_preferences'));
    this.overrides = {...this.overrides, ...patch}; this.publishPreferences();
    return this.session.view ? this.enqueue((view, signal) => view.applyPreferences(this.snapshot.preferences, signal)) : Promise.resolve();
  }
  clearOverrides(keys?: readonly (keyof P)[]): Promise<void> {
    if (keys) { for (const key of keys) delete this.overrides[key]; } else this.overrides = {};
    this.publishPreferences();
    return this.session?.view ? this.enqueue((view, signal) => view.applyPreferences(this.snapshot.preferences, signal)) : Promise.resolve();
  }
  resize(viewport: ReaderViewport): Promise<void> {
    validateViewport(viewport); this.viewport = copy(viewport);
    return this.enqueue((view, signal) => view.resize(this.viewport, signal));
  }
  setZoom(zoom: ReaderZoom): Promise<void> {
    if (zoom.kind === 'scale' && (!Number.isFinite(zoom.value) || zoom.value <= 0 || zoom.value > 8))
      return Promise.reject(new ReaderError('invalid_preferences'));
    return this.enqueue((view, signal) => { if (!view.setZoom) throw new ReaderError('unsupported_operation'); return view.setZoom(zoom, signal); });
  }
  getZoom() { return this.session?.view?.getZoom?.() ?? null; }
  pan(offset: Readonly<{x: number; y: number}>): Promise<void> {
    if (!Number.isFinite(offset.x) || !Number.isFinite(offset.y)) return Promise.reject(new ReaderError('invalid_preferences'));
    return this.enqueue((view, signal) => { if (!view.pan) throw new ReaderError('unsupported_operation'); return view.pan(offset, signal); });
  }
  /** One authorized document supplies owned previews; no second open/cache. */
  async page(index: number, options: Readonly<{width: number; signal: AbortSignal}>): Promise<ReaderPageImage> {
    const session = this.session, document = session?.document;
    if (!session || !document || !this.current(session) || this.snapshot.status !== 'ready') throw new ReaderError('closed');
    if (!Number.isSafeInteger(index) || index < 0 || (document.metadata.pageCount !== undefined && index >= document.metadata.pageCount))
      throw new ReaderError('invalid_location');
    if (!Number.isSafeInteger(options.width) || options.width < 32 || options.width > 2048) throw new ReaderError('limit_exceeded');
    if (!document.page) throw new ReaderError('unsupported_operation');
    const scope = abortScope([options.signal, session.abort.signal]), signal=scope.signal;
    let abort!: () => void;
    const stopped = new Promise<never>((_,reject)=>{
      abort=()=>reject(new ReaderError('aborted'));signal.addEventListener('abort',abort,{once:true});
    });
    try {
      throwIfAborted(signal);
      const image = await Promise.race([document.page(index,{width:options.width,signal}),stopped]);
      throwIfAborted(signal);if (!this.current(session)) throw new ReaderError('aborted');
      if (!['image/jpeg','image/png','image/webp'].includes(image.mime) || !(image.bytes instanceof Uint8Array)
        || !image.bytes.length || image.bytes.length > 16*1024*1024 || !Number.isSafeInteger(image.width) || image.width < 1
        || !Number.isSafeInteger(image.height) || image.height < 1) throw new ReaderError('invalid_book');
      return Object.freeze({...image,bytes:image.bytes.slice()});
    } finally {signal.removeEventListener('abort',abort);scope.dispose();}
  }
  async *search(query: string, options: ReaderSearchOptions): AsyncIterable<ReaderSearchResult> {
    const session = this.session;
    if (!session?.document || !this.current(session)) throw new ReaderError('closed');
    if (!query.trim() || query.length > 1024 || !Number.isSafeInteger(options.limit) || options.limit < 1 || options.limit > 500)
      throw new ReaderError('limit_exceeded');
    const scope = abortScope([options.signal, session.abort.signal]), signal=scope.signal;
    let iterator: AsyncIterator<ReaderSearchResult>|undefined;
    let abort!:()=>void;
    const stopped = new Promise<never>((_,reject)=>{abort=()=>reject(new ReaderError('aborted'));signal.addEventListener('abort',abort,{once:true});});
    let count = 0;
    try {
    throwIfAborted(signal);
    iterator = session.document.search(query, {...options, signal})[Symbol.asyncIterator]();
    for (;;) {
      throwIfAborted(signal);
      const next = await Promise.race([iterator.next(),stopped]);
      if (next.done) return;
      throwIfAborted(signal);
      if (!this.current(session)) throw new ReaderError('aborted');
      const result = next.value;
      if (result.location !== undefined) validateLocation(result.location, session.document.format);
      const match = result.match;
      if (match && (!Number.isSafeInteger(match.start) || match.start < 0 || !Number.isSafeInteger(match.length) || match.length < 1
        || match.start + match.length > result.excerpt.length)) throw new ReaderError('invalid_book');
      const captured = Object.freeze({...result, ...(result.location === undefined ? {} : {location: copy(result.location)}), ...(match ? {match: copy(match)} : {})});
      this.searchResults.set(captured, {session, signal: options.signal});
      yield captured;
      if (++count >= options.limit) return;
    }
    } finally {
      signal.removeEventListener('abort',abort);scope.dispose();
      if (iterator) {
        if (signal.aborted) {
          try { const closing=iterator.return?.(); if (closing) void closing.catch(()=>{}); } catch {}
        } else await iterator.return?.();
      }
    }
  }
}
function freezeToc(items: readonly import('./ports.ts').ReaderTocItem[]): readonly import('./ports.ts').ReaderTocItem[] {
  return Object.freeze(items.map(item => Object.freeze({...item, location: copy(item.location),
    ...(item.children ? {children: freezeToc(item.children)} : {})})));
}
