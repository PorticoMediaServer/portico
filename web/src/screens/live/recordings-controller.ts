import {DVRClient, DVRModel, activeRecording, dvrMessage, isAccessError, mutableRecording,
  type DeletePreview, type DVRPage, type DVRQuery, type DVRSummary, type Recording, type RecordingDetail, type RecordingGroups, type RecordingStorage} from '@core/dvr.ts';
import {encodedRoutePath, routePath} from '../../../../packages/contracts/src/routes.ts';
import {recordingsWindow} from './recordings-window';
import type {ChannelApi} from '@core/channel-guide.ts';

/** The four Recording destinations this door reads. Series rules are a different
 * generation and stay outside it. */
export const recordingsViews = ['upcoming', 'recorded', 'history', 'storage'] as const;
export type RecordingsView = typeof recordingsViews[number];
export type RecordingsSort = 'recent' | 'title' | 'oldest';
/** The exact resident query. A cursor is the server's opaque token, never rebuilt here. */
export type RecordingsQuery = Readonly<{view: RecordingsView; cursor?: string; recordingId?: string}>;
export type RecordingsPhase = 'idle' | 'loading' | 'ready' | 'error' | 'retired';
/** What this viewer actually holds: the server's own recording page, detail and
 * storage facts, plus its own genuine summary/group aggregates. Nothing is
 * derived from the loaded page. */
export type RecordingsSnapshot = Readonly<{
  query: RecordingsQuery;
  phase: RecordingsPhase;
  page: DVRPage | null;
  detail: RecordingDetail | null;
  storage: RecordingStorage | null;
  summary: DVRSummary | null;
  groups: RecordingGroups | null;
  /** The order the resident groups were read with; `groups.nextCursor` is the server's. */
  groupsSort: RecordingsSort;
  paging: Readonly<{busy: boolean; error: string; previous: boolean; next: boolean}>;
  accessDenied: boolean;
  /** The last destination/refresh failure. A failed command is never folded into it. */
  error: string;
  /** A command is in flight through the same private model. */
  mutating: boolean;
  /** The last command failure; cleared by the next committed command. */
  commandError: string;
}>;
/** A command's actual outcome: the ACK and the genuine reload are separate facts. */
export type RecordingsCommandOutcome = Readonly<{committed: boolean; refreshed: boolean; busy: boolean}>;
export type RecordingsControllerOptions = Readonly<{
  api: ChannelApi;
  serverId: string;
  /** The existing DVR receipt generator; 48 lowercase hex, never a UUID alias. */
  requestId: () => Promise<string>;
  /** The owning route/principal lease. */
  signal: AbortSignal;
  /** The borrowed family/profile/role/navigation epoch that owns this screen. */
  isCurrent: () => boolean;
}>;

const sorts: readonly RecordingsSort[] = ['recent', 'title', 'oldest'];
const receiptText = /^[a-f0-9]{48}$/;
const recordingText = /^[a-f0-9]{64}$/;
const cancelled = () => new DOMException('recordings-scope-closed', 'AbortError');
const invalid = () => Object.assign(new Error('recordings-query-invalid'), {code: 'recordings-query-invalid'});
const staleRow = () => Object.assign(new Error('recordings-row-stale'), {code: 'recordings-row-stale'});
const notOffered = () => Object.assign(new Error('recordings-command-not-offered'), {code: 'recordings-command-not-offered'});
const stalePreview = () => Object.assign(new Error('recordings-delete-preview-stale'), {code: 'recordings-delete-preview-stale'});
/** The legacy keepable states: a failure that produced a file can still be kept. */
const keepable: readonly string[] = ['completed', 'incomplete-playable', 'failed'];
const isCancelled = (failure: unknown) => failure instanceof DOMException && failure.name === 'AbortError';

/** Headless, captured-parent adapter for the Recordings door. Construction performs
 * no read and mints nothing; only a mounted caller starts a bounded request. The
 * same DVRClient/DVRModel wire is used, and every read is fenced by the borrowed
 * signal and authority epoch before and after it settles. */
export function createRecordingsController(input: RecordingsControllerOptions) {
  // Captured once: a later caller cannot swap the API, the server or the receipt source.
  const {api: sourceApi, serverId, signal, isCurrent} = input;
  const requestId = input.requestId;
  const life = new AbortController();
  const listeners = new Set<() => void>();
  /** The one armed command: its mutation HTTP may only leave while this exact
   * destination generation and lease are still the live ones. Profile-wide reads
   * (summary/groups/receipt) are deliberately outside this fence. */
  let commandArmed: Readonly<{serial: number; lease: number; query: RecordingsQuery; fence: AbortController}> | undefined;
  type ModelRead = Readonly<{serial: number; epoch: number; flight: object | undefined; query: DVRQuery}>;
  let dispatchRead: ModelRead | undefined;
  const modelRequest = (path: string, read: ModelRead) => read.query.recordingId
    ? path === encodedRoutePath('get_dvr_recordings_id', {id: read.query.recordingId})
    : read.query.view === 'storage' ? path === routePath('get_dvr_storage')
      : path.startsWith(routePath('get_dvr') + '?');
  const readCurrent = (read: ModelRead) => !closed && !signal.aborted && !life.signal.aborted && read.serial === loadSerial && read.epoch === epoch
    && (read.flight === undefined || read.flight === tailFlight && tailBusy);
  const api: ChannelApi = {
    async request<T>(path: string, method?: string, body?: unknown, requestSignal?: AbortSignal): Promise<T> {
      // Capture the original synchronous model-load dispatch before consulting
      // the external current predicate; a nested model load must not lend it its receipt.
      const original = dispatchRead, read = original && modelRequest(path, original) ? original : undefined;
      if (!current() || requestSignal?.aborted || read && !readCurrent(read)) throw cancelled();
      const mutation = (method ?? 'GET') !== 'GET';
      if (mutation) {
        const armed = commandArmed;
        if (!armed || armed.fence.signal.aborted || armed.serial !== loadSerial || armed.lease !== epoch) throw cancelled();
      }
      const signals: AbortSignal[] = [life.signal];
      if (requestSignal) signals.push(requestSignal);
      if (mutation && commandArmed) signals.push(commandArmed.fence.signal);
      const value = await sourceApi.request<T>(path, method, body, AbortSignal.any(signals));
      if (!current() || read && !readCurrent(read)) throw cancelled();
      return value;
    },
  };
  // DVRModel publishes loading synchronously before calling its client. Bind
  // each original load, including its post-command refresh, across that nested
  // publication without narrowing profile-wide summary/groups/receipt reads.
  class DestinationModel extends DVRModel {
    override load(next: DVRQuery = this.getSnapshot().query) {
      const previous = dispatchRead;
      dispatchRead = Object.freeze({serial: loadSerial, epoch, flight: tailBusy ? tailFlight : undefined, query: next});
      try {return super.load(next);} finally {dispatchRead = previous;}
    }
  }
  const model = new DestinationModel(new DVRClient(api, serverId, requestId));
  let closed = false, started = false, stopModel: (() => void) | undefined;
  /** Bumped by a new page/detail/storage load and by disposal, so an older load cannot land. */
  let loadSerial = 0;
  /** Bumped by a lease change (navigation/authority) and disposal only. Summary,
   * group and receipt reads are profile-wide and may finish across a local
   * destination change on the same lease; this fences them across actual retirement. */
  let epoch = 0, summarySerial = 0, groupsSerial = 0;
  let loading = false, accessDenied = false, errorText = '', commandError = '';
  let deletion: Readonly<{recording: Recording; preview: DeletePreview; serial: number; epoch: number; query: RecordingsQuery}> | undefined;
  let query: RecordingsQuery = Object.freeze({view: 'upcoming'});
  let page: DVRPage | null = null, detail: RecordingDetail | null = null, storage: RecordingStorage | null = null;
  let summary: DVRSummary | null = null, groups: RecordingGroups | null = null, groupsSort: RecordingsSort = 'recent';
  const pages = recordingsWindow<DVRPage>(), groupPages = recordingsWindow<RecordingGroups>();
  let tailFlight: object | undefined;
  let tailBusy = false, tailError = '', tailDirection: 'next' | 'previous' = 'next';
  const paging = () => {const window = query.view === 'recorded' ? groupPages : pages;return Object.freeze({busy: tailBusy, error: tailError,
    previous: window.offer('previous') !== undefined, next: window.offer('next') !== undefined});};
  function resetPaging() {pages.reset();groupPages.reset();tailBusy = false;tailError = '';tailFlight = undefined;}
  let state: RecordingsSnapshot = Object.freeze({query, phase: 'idle', page: null, detail: null, storage: null, summary: null, groups: null,
    groupsSort: 'recent', paging: paging(), accessDenied: false, error: '', mutating: false, commandError: ''});

  function notify() { for (const listener of [...listeners]) listener(); }
  function current() {
    if (closed) return false;
    if (signal.aborted) {retire();return false;}
    const accepted = isCurrent();
    if (closed || signal.aborted || !accepted) {retire();return false;}
    return true;
  }
  function derive(): RecordingsSnapshot {
    const snapshot = model.getSnapshot();
    const error = closed ? '' : errorText;
    const phase: RecordingsPhase = closed ? 'retired' : loading ? 'loading' : error ? 'error'
      : page || detail || storage || summary || groups ? 'ready' : 'idle';
    return Object.freeze({query, phase, page, detail, storage, summary, groups, groupsSort, paging: paging(),
      accessDenied: closed ? false : accessDenied || snapshot.accessDenied, error,
      mutating: closed ? false : snapshot.mutating, commandError: closed ? '' : commandError});
  }
  function publish() {
    if (closed) return;
    state = derive();
    notify();
  }
  /** The resident page/detail/storage objects of one destination generation. */
  function dropResident() {
    page = detail = storage = null;
  }
  /** Exact resident object identity, never an id match. */
  function resident(recording: Recording): boolean {
    return (page?.recordings.includes(recording) ?? false) || detail?.recording === recording;
  }
  /** Delete is offered only by the actual current page (its own deletion flag)
   * and never for an active recording. */
  function offersDelete(recording: Recording): boolean {
    return page?.deletionAvailable === true && !activeRecording(recording);
  }
  const sameDestination = (left: Readonly<{view: string; cursor?: string; recordingId?: string}>, right: RecordingsQuery) => left.view === right.view
    && (left.cursor ?? '') === (right.cursor ?? '') && (left.recordingId ?? '') === (right.recordingId ?? '');
  /** A destination load or a lease change disarms the armed command: its receipt
   * may still be pending, but no mutation HTTP may leave for the old generation. */
  function disarmCommand() {
    if (!commandArmed) return;
    commandArmed.fence.abort();
    commandArmed = undefined;
  }
  /** One authority change erases every private fact — page, detail, storage and
   * both aggregates — and fences anything already in flight. */
  function clearFacts(denied: boolean) {
    if (closed) return;
    disarmCommand();
    epoch++;summarySerial++;groupsSerial++;resetPaging();
    dropResident();summary = groups = null;groupsSort = 'recent';
    deletion = undefined;accessDenied = denied;errorText = '';commandError = '';
    publish();
  }
  function recordFailure(failure: unknown) {
    if (isCancelled(failure)) return;
    if (isAccessError(failure)) { clearFacts(true); return; }
    errorText = dvrMessage(failure);
    publish();
  }
  /** Adopt the model's own facts after a command; the model owns the post-ACK
   * reload and its captured, genuine destination query. */
  function adoptFacts() {
    const snapshot = model.getSnapshot();
    page = snapshot.page;detail = snapshot.detail;storage = snapshot.storage;
    pages.reset(query.cursor, page ?? undefined);tailBusy = false;tailError = '';
    accessDenied = snapshot.accessDenied;loading = false;
  }
  /** Every command targets one exact resident object of a settled destination. */
  function commandTarget(recording: Recording): Recording {
    if (!current()) throw cancelled();
    if (tailBusy || derive().phase !== 'ready' || !resident(recording)) throw staleRow();
    return recording;
  }
  /** One command through the same private model. The mutation may only leave while
   * its captured destination generation and lease are live, and a known ACK is
   * never relabelled by what the later genuine reload does. */
  async function command(work: () => Promise<unknown>): Promise<RecordingsCommandOutcome> {
    if (!current()) throw cancelled();
    if (model.getSnapshot().mutating) return Object.freeze({committed: false, refreshed: false, busy: true});
    deletion = undefined;
    const armed = Object.freeze({serial: loadSerial, lease: epoch, query, fence: new AbortController()});
    commandArmed = armed;
    let ack = false, failure: unknown;
    let ran = false;
    try {
      // A refused continuation kept the original resident page. Restore that
      // exact model query before a command's genuine post-ACK refresh.
      if (!sameDestination(model.getSnapshot().query, query)) {
        await model.load(query);
        if (!current() || armed.serial !== loadSerial || armed.lease !== epoch) throw cancelled();
        const restored = model.getSnapshot();
        if (restored.accessDenied || restored.error) throw staleRow();
      }
      ran = await model.act(async () => {
        try { await work(); ack = true; }
        catch (error) { failure = error; throw error; }
      });
    } finally {
      if (commandArmed === armed) commandArmed = undefined;
    }
    if (!current()) throw cancelled();
    // A newer destination generation (replacement, refresh or A->B->A) owns its
    // facts, loading and errors. A command that finishes after that generation
    // began reports only what it knew — a server ACK — and never adopts, publishes
    // or resets the newer state. Authority clearing already happened globally
    // through the client's access listener.
    if (armed.serial !== loadSerial || armed.lease !== epoch) {
      return Object.freeze({committed: ack, refreshed: false, busy: false});
    }
    if (!ack) {
      if (isAccessError(failure)) {
        clearFacts(true);
        return Object.freeze({committed: false, refreshed: false, busy: false});
      }
      adoptFacts();
      if (!isCancelled(failure)) commandError = dvrMessage(failure);
      publish();
      return Object.freeze({committed: false, refreshed: false, busy: false});
    }
    const after = model.getSnapshot();
    commandError = '';
    adoptFacts();
    if (after.accessDenied) {
      clearFacts(true);
      return Object.freeze({committed: true, refreshed: false, busy: false});
    }
    if (!ran) {
      // The destination generation moved after the ACK; only the newer facts stay.
      publish();
      return Object.freeze({committed: true, refreshed: false, busy: false});
    }
    errorText = after.error ?? '';
    publish();
    return Object.freeze({committed: true, refreshed: !after.error, busy: false});
  }
  /** Retires this captured viewer once. A silent retirement is for a snapshot read
   * inside a render; it still erases and unsubscribes. */
  function retire(silent = false) {
    if (closed) return;
    closed = true;life.abort();
    disarmCommand();
    signal.removeEventListener('abort', onAbort);stopModel?.();stopModel = undefined;
    loadSerial++;epoch++;summarySerial++;groupsSerial++;resetPaging();
    model.dispose();
    page = detail = storage = null;summary = groups = null;groupsSort = 'recent';deletion = undefined;
    loading = false;accessDenied = false;errorText = '';commandError = '';
    state = Object.freeze({query, phase: 'retired', page: null, detail: null, storage: null, summary: null, groups: null,
      groupsSort: 'recent', paging: paging(), accessDenied: false, error: '', mutating: false, commandError: ''});
    try { if (!silent) notify(); } finally { listeners.clear(); }
  }
  function onAbort() { retire(); }
  function dispose() { retire(); }
  function start() {
    if (started || closed) return;
    started = true;
    if (!current()) return;
    signal.addEventListener('abort', onAbort, {once: true});
    stopModel = model.subscribe(sync);
  }
  /** A borrowed access change (401/403) arrives through the model's own client. */
  function sync() {
    if (closed) return;
    if (signal.aborted || !isCurrent()) { retire(); return; }
    if (model.getSnapshot().accessDenied && !accessDenied) { clearFacts(true); return; }
    publish();
  }
  function checked(value: RecordingsQuery): RecordingsQuery {
    if (!value || typeof value !== 'object') throw invalid();
    const view = value.view;
    if (!recordingsViews.includes(view)) throw invalid();
    const cursor = value.cursor ?? '', recordingId = value.recordingId ?? '';
    if (typeof cursor !== 'string' || cursor.length > 4096) throw invalid();
    if (typeof recordingId !== 'string' || recordingId !== '' && !recordingText.test(recordingId)) throw invalid();
    return Object.freeze({view, ...(cursor ? {cursor} : {}), ...(recordingId ? {recordingId} : {})});
  }
  function checkedGroups(sort: RecordingsSort, cursor: string) {
    if (!sorts.includes(sort) || typeof cursor !== 'string' || cursor.length > 4096) throw invalid();
  }
  async function load(next: RecordingsQuery = query, continuation = false): Promise<void> {
      const entry = loadSerial, entryEpoch = epoch;
      if (!current() || entry !== loadSerial || entryEpoch !== epoch) throw cancelled();
      const captured = checked(next);
      start();
      disarmCommand();
      if (!current() || entry !== loadSerial || entryEpoch !== epoch) throw cancelled();
      const serial = ++loadSerial, at = epoch, flight = continuation ? {} : undefined;
      if (!continuation) {resetPaging();}
      else {tailFlight = flight;tailBusy = true;tailError = '';}
      const previousQuery = query;
      if (!continuation && !sameDestination(captured, query)) {
        // A replacement is a new destination generation: the previous page,
        // detail and storage objects stop being resident before the new query is
        // published, and a retained error page can never revive them. Profile-wide
        // summary/group facts and the receipt lease are deliberately untouched.
        dropResident();
      }
      deletion = undefined;if (!continuation) query = captured;loading = !continuation;errorText = '';commandError = '';
      publish();
      try {
        // Publication calls external subscribers. They may synchronously replace
        // this read; never let the old tail touch the model after that replacement.
        if (!current() || serial !== loadSerial || at !== epoch || continuation && tailFlight !== flight) throw cancelled();
        await model.load(captured);
        if (!current()) throw cancelled();
        if (serial !== loadSerial || continuation && (at !== epoch || tailFlight !== flight)) throw cancelled();
        const snapshot = model.getSnapshot();
        if (continuation && snapshot.error && !snapshot.accessDenied) {
          tailBusy = false;tailError = snapshot.error;publish();return;
        }
        query = captured;
        page = snapshot.page;detail = snapshot.detail;storage = snapshot.storage;
        if (page) {if (continuation) pages.accept(captured.cursor ?? '', page);else pages.reset(captured.cursor, page);}
        tailBusy = false;tailError = '';
        accessDenied = snapshot.accessDenied;errorText = snapshot.error ?? '';
        if (accessDenied) {
          summary = null;groups = null;groupsSort = 'recent';resetPaging();
          epoch++;summarySerial++;groupsSerial++;
        }
        loading = false;
        publish();
      } catch (failure) {
        if (!current()) throw cancelled();
        if (serial === loadSerial && at === epoch) {
          loading = false;tailBusy = false;
          if (continuation) query = previousQuery;
          if (isAccessError(failure)) clearFacts(true);
          else if (!isCancelled(failure)) { if (continuation) tailError = dvrMessage(failure);else errorText = dvrMessage(failure);publish(); }
        }
        throw failure;
      }
    }
  async function navigatePage(direction: 'next' | 'previous', expected: RecordingsSnapshot): Promise<void> {
    if (!current()) throw cancelled();
    if (expected !== state || tailBusy || loading || model.getSnapshot().mutating || detail || query.view === 'storage') return;
    const window = query.view === 'recorded' ? groupPages : pages;
    const cursor = window.offer(direction);
    if (cursor === undefined) return;
    tailDirection = direction;
    if (query.view !== 'recorded') {await load({...query, cursor}, true);return;}
    const at = epoch, serial = ++groupsSerial, destination = loadSerial, sort = groupsSort, flight = {};
    tailFlight = flight;tailBusy = true;tailError = '';publish();
    try {
      if (!current() || at !== epoch || serial !== groupsSerial || destination !== loadSerial || sort !== groupsSort || tailFlight !== flight) throw cancelled();
      const value = groupPages.cached(cursor) ?? await model.client.groups(sort, cursor);
      if (!current() || at !== epoch || serial !== groupsSerial || destination !== loadSerial || sort !== groupsSort || tailFlight !== flight) throw cancelled();
      groupPages.accept(cursor, value);groups = value;tailBusy = false;tailError = '';publish();
    } catch (failure) {
      if (!current()) throw cancelled();
      if (at === epoch && serial === groupsSerial && destination === loadSerial) {
        tailBusy = false;
        if (isAccessError(failure)) clearFacts(true);
        else if (!isCancelled(failure)) {tailError = dvrMessage(failure);publish();}
      }
      throw failure;
    }
  }
  return {
    /** Only the exact resident objects of a settled destination generation pass.
     * A replacement, a refresh in flight or a failed destination refuses them. */
    contains(recording: Recording): boolean {
      if (!current()) return false;
      if (derive().phase !== 'ready') return false;
      return resident(recording);
    },
    getSnapshot(): RecordingsSnapshot {
      if (!closed && (signal.aborted || !isCurrent())) retire(true);
      return state;
    },
    subscribe(listener: () => void) {
      if (closed) return () => {};
      listeners.add(listener);
      return () => { listeners.delete(listener); };
    },
    /** Reads one exact destination. A cursor is passed through untouched, and its
     * reply's own `nextCursor` is the only next token. */
    load: (next: RecordingsQuery = query) => load(next),
    more: (expected: RecordingsSnapshot = state) => navigatePage('next', expected),
    previous: (expected: RecordingsSnapshot = state) => navigatePage('previous', expected),
    retryPage: (expected: RecordingsSnapshot = state) => navigatePage(tailDirection, expected),
    /** The server's own totals. A loaded page is never counted instead. */
    async readSummary(): Promise<DVRSummary> {
      if (!current()) throw cancelled();
      start();
      const at = epoch, serial = ++summarySerial;
      try {
        const value = await model.client.summary();
        if (!current()) throw cancelled();
        if (at !== epoch || serial !== summarySerial) throw cancelled();
        summary = value;accessDenied = false;errorText = '';
        publish();
        return value;
      } catch (failure) {
        if (!current()) throw cancelled();
        if (at === epoch && serial === summarySerial) recordFailure(failure);
        throw failure;
      }
    },
    /** The server's genuine ordered groups, with its exact counts and next cursor. */
    async readGroups(sort: RecordingsSort = 'recent', cursor = ''): Promise<RecordingGroups> {
      if (!current()) throw cancelled();
      checkedGroups(sort, cursor);
      start();
      const at = epoch, serial = ++groupsSerial, restated = sort !== groupsSort;
      groupsSort = sort;groupPages.reset();tailBusy = false;tailError = '';tailFlight = undefined;
      if (restated) groups = null;
      publish();
      try {
        if (!current() || at !== epoch || serial !== groupsSerial || groupsSort !== sort) throw cancelled();
        const value = await model.client.groups(sort, cursor);
        if (!current()) throw cancelled();
        if (at !== epoch || serial !== groupsSerial || groupsSort !== sort) throw cancelled();
        groups = value;groupPages.reset(cursor, value);accessDenied = false;errorText = '';
        publish();
        return value;
      } catch (failure) {
        if (!current()) throw cancelled();
        if (at === epoch && serial === groupsSerial) { if (restated) groups = null;recordFailure(failure); }
        throw failure;
      }
    },
    /** The same genuine 48-hex DVR operation receipt later command integration
     * awaits; mints nothing itself and is fenced like every other read. */
    async requestReceipt(): Promise<string> {
      if (!current()) throw cancelled();
      start();
      const at = epoch;
      const value = await requestId();
      if (!current()) throw cancelled();
      if (at !== epoch) throw cancelled();
      if (typeof value !== 'string' || !receiptText.test(value)) throw invalid();
      return value;
    },
    /** Cancel one exact resident recording through the same private model, using
     * the canonical mutable/active states the legacy menu offers. */
    async cancel(recording: Recording): Promise<RecordingsCommandOutcome> {
      const target = commandTarget(recording);
      if (!mutableRecording(target) && !activeRecording(target)) throw notOffered();
      return command(() => model.client.cancel(target));
    },
    /** Keep or stop keeping one exact resident recording, only in the legacy
     * keepable states; the client sends the toggled value for this revision. */
    async keep(recording: Recording): Promise<RecordingsCommandOutcome> {
      const target = commandTarget(recording);
      if (!keepable.includes(target.state)) throw notOffered();
      return command(() => model.client.keep(target));
    },
    /** Resolve a tuner conflict in this recording's favour by raising its priority
     * above every recording it actually overlaps. The server validates the value. */
    async raisePriority(recording: Recording, priority: number): Promise<RecordingsCommandOutcome> {
      const target = commandTarget(recording);
      if (!mutableRecording(target) || !Number.isSafeInteger(priority)) throw notOffered();
      const bounded = Math.min(1000, Math.max(-1000, priority));
      return command(() => model.client.update(target, {...target.options, priority: bounded}));
    },
    /** The server's own delete preview for the exact resident row, offered only by
     * the actual current page for a non-active recording. The returned object is
     * the only preview this door will confirm. */
    async deletePreview(recording: Recording): Promise<DeletePreview | undefined> {
      const target = commandTarget(recording);
      if (!offersDelete(target)) return undefined;
      start();
      const serial = loadSerial, at = epoch, forQuery = query;
      deletion = undefined;
      const preview = await model.client.deletePreview(target);
      if (!current()) throw cancelled();
      if (serial !== loadSerial || at !== epoch || !sameDestination(forQuery, query) || !resident(target) || !offersDelete(target)) {
        throw cancelled();
      }
      const bound = Object.freeze({recording: target, preview: Object.freeze({...preview}), serial, epoch: at, query: forQuery});
      deletion = bound;
      return bound.preview;
    },
    /** Confirm the exact bound preview for this row and generation through the same
     * private model. There is no caller-supplied preview, no cross-row reuse and no
     * optimistic removal: the genuine destination is reloaded only after the ACK. */
    async confirmDelete(recording: Recording): Promise<RecordingsCommandOutcome> {
      const target = commandTarget(recording);
      const bound = deletion;
      if (!bound || bound.recording !== target || bound.serial !== loadSerial || bound.epoch !== epoch
        || !sameDestination(bound.query, query) || !offersDelete(target)) throw stalePreview();
      deletion = undefined;
      return command(() => model.client.delete(target, bound.preview));
    },
    dispose,
  };
}
export type RecordingsController = ReturnType<typeof createRecordingsController>;
