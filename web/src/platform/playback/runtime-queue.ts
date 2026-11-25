import type {PlaybackSnapshot} from '@core/index.ts';
import type {CatalogueSelectionQueuePort} from '@core/catalogue-selection.ts';
import {QueueClient, type SegmentInput} from '@core/playback-v1/queue.ts';
import {SessionsClient} from '@core/playback-v1/sessions.ts';
import type {QueueHeader, Session} from '@core/playback-v1/types.ts';
import type {HomeSelectionPlaybackRequest} from '@core/home-selection/types.ts';
import type {SelectionContext} from '@core/selection-model/types.ts';
import type {PlayerOffersData} from '@core/player-offers.ts';
import type {PlaybackItem} from '@core/playback-v1/item.ts';
import {playbackFence, sameFence, type DockTransition, type LocalPlaybackFence, type PlaybackDock, type PlaybackFence, type PlaybackPolicy, type PolicyCommand, type PolicyView, type QueuePlaybackFence} from '../../../../packages/player-core/src/index.ts';
import type {RuntimeGroupBinding} from './group-binding.ts';
import type {WebLocalQueue} from './local-queue.ts';
import {currentPolicyQueue, type WebPlaybackPolicyAction} from './policy.ts';
import {markPresentedInPlayer} from './presented.ts';
import {createWebQueueListPort, type WebQueueListLifetime, type WebQueueListPort} from './queue-list.ts';
import {captureQueueSource, type WebPlaybackQueueSource} from './queue-source.ts';
import {selectionSegments, selectionStartSeconds} from './selection.ts';
import {entryStartDisplay, queueEntrySubject} from './subject.ts';
import type {WebPlaybackVersionChoice, WebPlaybackVersionChoices} from './versions.ts';
import type {WebPlaybackAuthority, WebPlaybackEnvironment, WebPlaybackInfoRead, WebPlaybackRuntimeSnapshot, WebPlaybackScreenLifetime, WebPlaybackSource, WebPlaybackStartDisplay, WebPlaybackVersionStart} from './types.ts';
import {METADATA_RETRY_MS, START_DEADLINE_MS, START_HELD_POLL_MS, problem, startDeadlineError, startStopped, validSeconds, type Bookmark, type GroupTransportCommand, type LocalQueueLease, type Stack, type Starting} from './runtime-types.ts';

export type QueueRuntimeHost = Readonly<{
  authority(): WebPlaybackAuthority;
  source(): WebPlaybackSource;
  env(): WebPlaybackEnvironment;
  dock(): PlaybackDock;
  policy(): PlaybackPolicy;
  life(): AbortController;
  stack(): Stack;
  setStack(stack: Stack): void;
  snapshot(): WebPlaybackRuntimeSnapshot;
  group(): RuntimeGroupBinding | undefined;
  bookmarks(): Map<string, Bookmark>;
  retiredFences(): Set<string>;
  versionChoices(): WeakMap<WebPlaybackVersionChoices, Readonly<{isCurrent(): boolean; signal: AbortSignal; failedVersionId?: string}>>;
  catalogueEnqueuing(): boolean;
  setCatalogueEnqueuing(value: boolean): void;
  policyView(): PolicyView | null;
  policyAdvanceFence(): QueuePlaybackFence | undefined;
  setPolicyAdvanceFence(fence: QueuePlaybackFence | undefined): void;
  runtimeProblem(): string | undefined;
  setRuntimeProblem(code: string | undefined): void;
  setReplacing(value: boolean): void;
  nextOperation(): number;
  operation(): number;
  assertCurrent(): void;
  current(stack?: Stack): boolean;
  matches(fence: PlaybackFence): boolean;
  require(fence: PlaybackFence): Stack;
  key(fence: PlaybackFence): string;
  selectionCurrent(context: SelectionContext, signal: AbortSignal, current: (context: SelectionContext) => boolean): void;
  retainMediaStart(signal?: AbortSignal, isCurrent?: () => boolean): () => void;
  retainedQueueIds(stack: Stack): readonly string[];
  makeStack(mode?: Stack['mode']): Stack;
  activate(stack: Stack): void;
  retire(stack: Stack): void;
  replace(mode?: Stack['mode']): Stack;
  rememberCurrent(): void;
  bindLocalQueue(stack: Stack, lease: LocalQueueLease): WebLocalQueue;
  awaitLocalPaused(stack: Stack, serial: number): Promise<LocalPlaybackFence>;
  awaitFence(stack: Stack, serial: number): Promise<PlaybackFence>;
  beginStarting(display: WebPlaybackStartDisplay | undefined, retry: () => Promise<void>, options?: Readonly<{watchdog?: boolean}>): Starting;
  retryLifetime(): WebPlaybackScreenLifetime;
  reportStartProblem(error: unknown, display: WebPlaybackStartDisplay | undefined, retry: () => Promise<void>, serial: number, sentence?: string): 'shown' | 'quiet' | 'unhandled';
  clearSourceResources(stack: Stack): void;
  readItemBounded(itemId: string, signal: AbortSignal, current: () => boolean): Promise<PlaybackItem>;
  offers(stack: Stack, playback: PlaybackSnapshot): PlayerOffersData | null;
  bindSubtitles(stack: Stack, offers: PlayerOffersData, key: string): void;
  groupTransport(command: GroupTransportCommand, fence: PlaybackFence, seconds?: number): Promise<boolean>;
  resume(fence: PlaybackFence): Promise<void>;
  subscribe(listener: () => void): () => void;
  setTimer(fn: () => void, ms: number): unknown;
  clearTimer(timer: unknown): void;
  sync(): void;
}>;

/** A start the server never answers must not stall Play silently (P2-74 E-web-17): after
 * START_DEADLINE_MS the start is retired through its ordinary lifetime and reported. */
export function createStartDeadline(host: Pick<QueueRuntimeHost, 'setTimer' | 'clearTimer'>, held?: () => boolean) {
  const controller = new AbortController();
  let timer: unknown, cleared = false;
  // J-27: a refused start already drawn as its own card (a held failure) keeps its first reason: the
  // deadline waits while it shows, and a Try again from it gets a fresh START_DEADLINE_MS.
  const arm = (ms: number) => {timer = host.setTimer(fire, ms);};
  const fire = () => {
    if (cleared) return;
    if (held?.()) {arm(START_HELD_POLL_MS); waiting = true; return;}
    if (waiting) {waiting = false; arm(START_DEADLINE_MS); return;}
    controller.abort();
  };
  let waiting = false;
  arm(START_DEADLINE_MS);
  return {signal: controller.signal, expired: () => controller.signal.aborted, clear: () => {cleared = true; host.clearTimer(timer);},
    /** J-27: a start never ends silently. An awaited request that ignores the abort (a hung fetch, seen in
     * WebKit) still settles at the deadline, so the timeout is reported instead of waiting forever. */
    race: <T>(work: Promise<T>): Promise<T> => {
      if (controller.signal.aborted) {work.catch(() => {}); return Promise.reject(startDeadlineError());}
      return new Promise<T>((resolve, reject) => {
        const expire = () => {work.catch(() => {}); reject(startDeadlineError());};
        controller.signal.addEventListener('abort', expire, {once: true});
        work.then(value => {controller.signal.removeEventListener('abort', expire); resolve(value);},
          error => {controller.signal.removeEventListener('abort', expire); reject(error);});
      });
    }};
}

function cancelStack(host: QueueRuntimeHost, stack: Stack, serial: number): void {
  if (host.current(stack) && serial === host.operation()) {
    host.setReplacing(true);
    host.retire(stack);
    const next = host.makeStack();
    host.setStack(next);
    host.activate(next);
    host.setReplacing(false);
    host.sync();
  }
}

/** Info reads the current committed report afresh. It neither adopts a session nor
 * substitutes capabilities/options for source, delivery or renderer measurements. */
export async function readPlaybackInfo(host: QueueRuntimeHost, expected: QueuePlaybackFence, signal: AbortSignal): Promise<WebPlaybackInfoRead | null> {
  if (expected.kind !== 'queue' || signal.aborted) return null;
  const fence = playbackFence(expected), stack = host.require(fence), captured = host.snapshot().session;
  if (stack.mode !== 'server' || !captured || captured.kind !== 'vod' || captured.state === 'ended'
    || captured.id !== fence.sessionId || captured.itemId !== fence.itemId
    || captured.presentation.generation !== fence.generation || captured.queue?.entryId !== fence.entryId) return null;
  const isCurrent = () => !signal.aborted && host.current(stack) && host.matches(fence)
    && host.snapshot().session?.presentation.generation === captured.presentation.generation
    && host.snapshot().session?.state !== 'ended';
  if (!isCurrent()) return null;
  const retired = new AbortController(), off = host.subscribe(() => {if (!isCurrent()) retired.abort();});
  try {
    const fresh = await new SessionsClient(stack.http).get(fence.sessionId, AbortSignal.any([signal, stack.life.signal, host.life().signal, retired.signal]));
    if (!isCurrent() || fresh.id !== captured.id
      || fresh.kind !== captured.kind || fresh.role !== captured.role || fresh.state === 'ended'
      || fresh.itemId !== fence.itemId || fresh.versionId !== captured.versionId
      || fresh.presentation.generation !== fence.generation
      || fresh.queue?.queueId !== captured.queue?.queueId || fresh.queue?.entryId !== fence.entryId) return null;
    return Object.freeze({fence, sessionRevision: String(fresh.presentation.generation),
      ...(fresh.playbackInfo !== undefined ? {playbackInfo: fresh.playbackInfo} : {}),
      ...(fresh.observedPlayback !== undefined ? {observedPlayback: fresh.observedPlayback} : {}), isCurrent});
  } finally {off();}
}

/** A chooser reads real current options without stopping the playing source. Its owner keeps
 * this read alive until the chosen start settles, then closes/disposes the chooser. */
export async function readVersions(host: QueueRuntimeHost, expected: QueuePlaybackFence, signal: AbortSignal, {excludeFailed = false}: Readonly<{excludeFailed?: boolean}> = {}): Promise<WebPlaybackVersionChoices | null> {
  if (expected.kind !== 'queue') return null;
  const fence = playbackFence(expected), stack = host.require(fence), selectedVersionId = host.snapshot().session?.versionId;
  if (stack.subject?.kind !== 'video' || !selectedVersionId || signal.aborted) return null;
  const playback = stack.service.getSnapshot(), failed = playback.phase === 'error' && !playback.ended;
  if (excludeFailed && !failed) return null;
  const failedVersionId = failed ? selectedVersionId : undefined;
  const isCurrent = () => !signal.aborted && host.current(stack) && host.matches(fence)
    && host.snapshot().session?.versionId === selectedVersionId
    && (stack.service.getSnapshot().phase === 'error' && !stack.service.getSnapshot().ended) === failed;
  const options = await stack.optionsClient.options(fence.itemId, {}, AbortSignal.any([signal, stack.life.signal]));
  if (!isCurrent()) return null;
  if (options.itemId !== fence.itemId) problem('playback_version_unavailable');
  const versions = Object.freeze(options.versions.filter(version => !excludeFailed || version.id !== failedVersionId));
  const choices = Object.freeze({fence, options, versions, selectedVersionId, ...(failedVersionId ? {failedVersionId} : {}), isCurrent});
  host.versionChoices().set(choices, {isCurrent, signal, ...(excludeFailed ? {failedVersionId} : {})});
  return choices;
}

export async function playVersion(host: QueueRuntimeHost, request: WebPlaybackVersionStart | WebPlaybackVersionChoice, isCurrent?: (context: SelectionContext) => boolean): Promise<void> {
  const {versionId, signal} = request;
  const selection = 'choices' in request ? host.versionChoices().get(request.choices) : undefined;
  const itemId = 'choices' in request ? request.choices.fence.itemId : request.itemId;
  const check = () => {
    if ('choices' in request) { if (!selection?.isCurrent() || signal.aborted) problem('stale_playback_command'); }
    else { if (!isCurrent) problem('stale_selection_context'); host.selectionCurrent(request.context, signal, isCurrent); }
  };
  check();
  await startVersion(host, {itemId, versionId, signal, check, selection, failedVersionId: selection?.failedVersionId,
    // The accepted new output intentionally retires the old chooser fence. Only its original
    // read/action lifetime and genuine parent authority remain command prerequisites here.
    valid: lifetime => {
      if ('choices' in request) return true;
      try { host.selectionCurrent(request.context, lifetime, isCurrent!); return true; } catch { return false; }
    },
    accepted: lifetime => {
      if ('choices' in request) { if (lifetime.aborted) problem('stale_playback_command'); }
      else host.selectionCurrent(request.context, signal, isCurrent!);
    }});
}

async function startVersion(host: QueueRuntimeHost, a: Readonly<{itemId: string; versionId: string; signal: AbortSignal; check: () => void; selection?: Readonly<{signal: AbortSignal}>; failedVersionId?: string;
  valid: (lifetime: AbortSignal) => boolean; accepted: (lifetime: AbortSignal) => void}>): Promise<void> {
  const {itemId, versionId, signal, check} = a;
  if (![itemId, versionId].every(id => /^[A-Za-z0-9_.-]{1,128}$/.test(id))) problem('invalid_playback_target');
  const serial = host.nextOperation(), previous = host.stack();
  const finishRetention = host.retainMediaStart(signal, () => serial === host.operation());
  // A different version of the title already playing is the same title: its facts are truthful.
  // Anything else is named by its own queue entry once one exists; no title is invented.
  const playing = previous.itemId === itemId && previous.subject && previous.subject.kind !== 'channel' ? previous.subject : undefined;
  const posterUrl = previous.posterUrl ?? previous.backdropUrl;
  const retry = async () => {
    const life = host.retryLifetime();
    await startVersion(host, {itemId, versionId, signal: life.signal, check: () => {host.assertCurrent(); if (life.signal.aborted || !life.isCurrent()) problem('stale_playback_command');},
      valid: () => true, accepted: lifetime => {if (lifetime.aborted) problem('stale_playback_command');}});
  };
  const starting = host.beginStarting(playing && {subject: playing, ...(posterUrl ? {posterUrl} : {})}, retry), stopped = new AbortController();
  starting.stopWith(() => stopped.abort());
  try {
    const options = await previous.optionsClient.options(itemId, {}, AbortSignal.any([signal, previous.life.signal, stopped.signal]));
    check();
    if (serial !== host.operation() || !host.current(previous)) problem('stale_selection_context');
    // options.versions is readAssets(available=1) grouped by versionsOf on the server.
    if (options.itemId !== itemId || !options.versions.some(version => version.id === versionId) || versionId === a.failedVersionId) problem('playback_version_unavailable');
    const startSeconds = options.resume ? options.resume.positionMs / 1000 : 0;
    if (!validSeconds(startSeconds)) problem('invalid_playback_position');
    const stack = host.replace(); stack.expanded = true;
    starting.attach(stack);
    const lifetime = AbortSignal.any([signal, ...(a.selection ? [a.selection.signal] : []), stack.life.signal, stopped.signal]);
    const current = () => lifetime.aborted || !host.current(stack) || serial !== host.operation() ? false : a.valid(lifetime);
    const cancel = () => cancelStack(host, stack, serial);
    lifetime.addEventListener('abort', cancel, {once: true});
    try {
      stack.capacityCurrent = current;
      await stack.queue.playItem(itemId, startSeconds, undefined, undefined, undefined, {versionId}, {signal: lifetime, isCurrent: current, ...(options.container ? {from: {container: options.container}} : {})});
      a.accepted(lifetime);
      if (!host.current(stack) || serial !== host.operation()) problem('stale_selection_context');
      const started = stack.service.getSnapshot(), session = stack.queue.currentSession();
      if (started.phase === 'error' || !session || session.itemId !== itemId || session.versionId !== versionId
        || started.session?.id !== session.id || started.session.generation !== session.presentation.generation) problem('playback_version_failed');
      starting.accepted(); starting.stopWith(cancel);
      host.sync();
    } catch (error) { cancel(); throw error; }
    finally {if (stack.capacityCurrent === current) stack.capacityCurrent = undefined; lifetime.removeEventListener('abort', cancel);}
  } catch (error) {starting.end(); if (stopped.signal.aborted) throw startStopped(); throw error;}
  finally {finishRetention();}
}

/** Append checked catalogue segments without inventing a Home document context. */
export function catalogueQueue(host: QueueRuntimeHost): CatalogueSelectionQueuePort | undefined {
  host.assertCurrent();
  const initialStack = host.stack();
  if (initialStack.mode !== 'server' || initialStack.local || initialStack.channel || host.group()?.active() || host.dock().getSnapshot().remote) return undefined;
  return {enqueueSegments: async (segments, placement, lifetime) => {
    if (host.catalogueEnqueuing()) problem('queue_busy');
    host.setCatalogueEnqueuing(true);
    let finishRetention = () => {};
    try {
      const stack = host.stack(), serial = host.operation();
      let expectedQueueId = stack.queue.getSnapshot().view?.queue.id;
      const check = () => {
        host.assertCurrent();
        if (stack.mode !== 'server' || stack.local || stack.channel || host.group()?.active() || host.dock().getSnapshot().remote
          || serial !== host.operation() || stack.queue.getSnapshot().view?.queue.id !== expectedQueueId) problem('queue_control_unavailable');
        if (lifetime.signal.aborted || !lifetime.isCurrent() || !host.current(stack)) problem('stale_selection_context');
      };
      check();
      if (!['next', 'end'].includes(placement) || !segments.length || segments.length > 256) problem('invalid_queue_placement');
      finishRetention = host.retainMediaStart(lifetime.signal, () => serial === host.operation());
      const signal = AbortSignal.any([lifetime.signal, stack.life.signal, host.life().signal]);
      const client = new QueueClient({send: value => {
        check();
        return stack.http.send({...value, signal: value.signal ? AbortSignal.any([value.signal, signal]) : signal});
      }}, host.env().key);
      const header = stack.queue.getSnapshot().view?.queue;
      if (!header) {
        if (stack.queue.getSnapshot().phase === 'preparing' || stack.service.getSnapshot().session) problem('queue_not_ready');
        const created = await client.create(segments, {signal, retainQueueIds: host.retainedQueueIds(stack)});
        check(); await stack.queue.open(created.queue.id, signal, () => {check(); return true;});
        expectedQueueId = created.queue.id; check(); host.sync(); return created.queue;
      }
      let queue = await client.header(header.id, signal); check();
      // Each next insertion is after current; reverse requests preserve pick order.
      for (const segment of placement === 'next' ? [...segments].reverse() : segments) {
        check(); queue = (await client.addSegment(queue.id, queue.revision, placement, segment)).queue; check();
      }
      await stack.queue.open(queue.id, signal, () => {check(); return true;}); check(); host.sync(); return queue;
    } finally {host.setCatalogueEnqueuing(false); finishRetention();}
  }};
}

export async function playFromQueue(host: QueueRuntimeHost, source: WebPlaybackQueueSource, context: WebPlaybackScreenLifetime, options: Readonly<{shuffle?: boolean; display?: WebPlaybackStartDisplay}> = {}): Promise<void> {
  host.assertCurrent();
  if (context.signal.aborted || !context.isCurrent()) problem('stale_selection_context');
  const captured = captureQueueSource(source);
  if (options.shuffle !== undefined && typeof options.shuffle !== 'boolean') problem('invalid_queue_playback_source');
  await playQueueSegments(host, [{source: captured, order: {mode: options.shuffle ? 'shuffle' : 'source'}}], context, undefined, options.display);
}

export async function playQueueSegments(host: QueueRuntimeHost, segments: readonly SegmentInput[], context: WebPlaybackScreenLifetime, startSeconds?: number, display?: WebPlaybackStartDisplay): Promise<QueueHeader> {
  const check = () => {
    host.assertCurrent();
    if (context.signal.aborted || !context.isCurrent()) problem('stale_selection_context');
  };
  check();
  if (!Array.isArray(segments) || !segments.length || startSeconds !== undefined && !validSeconds(startSeconds)) problem('invalid_queue_playback_source');
  const serial = host.nextOperation();
  const finishRetention = host.retainMediaStart(context.signal, () => serial === host.operation());
  const starting = host.beginStarting(display, async () => {await playQueueSegments(host, segments, host.retryLifetime(), startSeconds, display);}), stopped = new AbortController();
  starting.stopWith(() => stopped.abort());
  let timedOut = false;
  try {
    const stack = host.replace();
    starting.attach(stack);
    // Actual metadata collapses music to its real dock in selectSource. A queue that is only ever
    // music (an album, an artist, a radio) starts in the dock: it never waits on that read to show.
    stack.expanded = !segments.every(segment => 'container' in segment.source && ['album', 'artist', 'radio'].includes(segment.source.container.kind));
    const deadline = createStartDeadline(host, () => !!stack.startFailure?.isCurrent());
    const lifetime = AbortSignal.any([context.signal, stack.life.signal, host.life().signal, deadline.signal, stopped.signal]);
    const current = () => {
      if (lifetime.aborted || !host.current(stack) || serial !== host.operation() || stack.capabilityRejected) return false;
      try {check();return true;} catch {return false;}
    };
    const cancel = () => cancelStack(host, stack, serial);
    lifetime.addEventListener('abort', cancel, {once: true});
    try {
      stack.capacityCurrent = current;
      const queue = await deadline.race(stack.queue.playSegments(segments, {startSeconds, signal: lifetime, isCurrent: current}));
      if (!current()) problem(deadline.expired() && !context.signal.aborted ? 'playback_start_timeout' : 'stale_selection_context');
      const session = stack.queue.currentSession(), started = stack.service.getSnapshot();
      if (started.phase === 'error' || !session || session.queue?.queueId !== queue.id || started.session?.id !== session.id || started.session.generation !== session.presentation.generation) problem('playback_start_failed');
      starting.accepted(); starting.stopWith(cancel);
      host.sync();return queue;
    } catch (error) {
      let admissionFailed = false;
      if (stack.capabilityRejected && host.current(stack) && serial === host.operation()) {
        try {check();admissionFailed = true;} catch { /* Original caller retired. */ }
      }
      cancel();
      if (admissionFailed) problem('playback_start_failed');
      // J-27: a start that reaches its deadline before it can play is drawn as its compact card,
      // in the player, with Try again; never a bare toast after the player has closed.
      if (deadline.expired() && !context.signal.aborted && !stopped.signal.aborted) {timedOut = true; problem('playback_start_timeout');}
      throw error;
    }
    finally {deadline.clear(); if (stack.capacityCurrent === current) stack.capacityCurrent = undefined; lifetime.removeEventListener('abort', cancel);}
  } catch (error) {
    const shown = starting.display();
    starting.end(); if (stopped.signal.aborted) throw startStopped();
    const reason = timedOut && !context.signal.aborted ? new Error('playback_start_timeout') : error;
    // A refused start already draws its own card; reportStartProblem keeps that first reason.
    const outcome = host.reportStartProblem(reason, shown, async () => {await playQueueSegments(host, segments, host.retryLifetime(), startSeconds, shown);}, serial);
    throw outcome === 'unhandled' ? reason : markPresentedInPlayer(reason);
  }
  finally {finishRetention();}
}

export async function playSelection(host: QueueRuntimeHost, request: HomeSelectionPlaybackRequest, current: (context: SelectionContext) => boolean) {
  host.selectionCurrent(request.context, request.signal, current);
  const segments = selectionSegments(request.items, true), serial = host.nextOperation();
  const finishRetention = host.retainMediaStart(request.signal, () => serial === host.operation());
  const first = request.items[0], pressed = first && 'entry' in first ? first.entry : undefined;
  const starting = host.beginStarting(pressed && entryStartDisplay(pressed), async () => {await playSelection(host, {...request, signal: host.life().signal}, current);}), stopped = new AbortController();
  starting.stopWith(() => stopped.abort());
  let timedOut = false;
  try {
    const stack = host.replace();
    starting.attach(stack);
    stack.expanded = true;
    const cancel = () => cancelStack(host, stack, serial);
    const deadline = createStartDeadline(host, () => !!stack.startFailure?.isCurrent()), signal = AbortSignal.any([request.signal, deadline.signal, stopped.signal]);
    signal.addEventListener('abort', cancel, {once: true});
    const originalCurrent = () => host.current(stack) && serial === host.operation() && !signal.aborted && current(request.context);
    try {
      stack.capacityCurrent = originalCurrent;
      const queue = await deadline.race(stack.queue.playSegments(segments, {startSeconds: selectionStartSeconds(request.items), signal, isCurrent: originalCurrent}));
      if (deadline.expired() && !request.signal.aborted) problem('playback_start_timeout');
      host.selectionCurrent(request.context, request.signal, current);
      if (!host.current(stack) || serial !== host.operation()) problem('stale_selection_context');
      starting.accepted(); starting.stopWith(cancel);
      host.sync();
      return {queue};
    } catch (error) {
      if (host.current(stack) && serial === host.operation()) cancel();
      if (deadline.expired() && !request.signal.aborted && !stopped.signal.aborted) {timedOut = true; problem('playback_start_timeout');}
      throw error;
    } finally {deadline.clear(); if (stack.capacityCurrent === originalCurrent) stack.capacityCurrent = undefined;signal.removeEventListener('abort', cancel);}
  } catch (error) {
    const shown = starting.display();
    starting.end(); if (stopped.signal.aborted) throw startStopped();
    const reason = timedOut && !request.signal.aborted ? new Error('playback_start_timeout') : error;
    const outcome = host.reportStartProblem(reason, shown, async () => {await playSelection(host, {...request, signal: host.life().signal}, current);}, serial);
    throw outcome === 'unhandled' ? reason : markPresentedInPlayer(reason);
  }
  finally {finishRetention();}
}

export async function enqueueSelection(host: QueueRuntimeHost, request: HomeSelectionPlaybackRequest & {placement: 'next' | 'end'}, current: (context: SelectionContext) => boolean) {
  host.selectionCurrent(request.context, request.signal, current);
  if (!['next', 'end'].includes(request.placement)) problem('invalid_queue_placement');
  const segments = selectionSegments(request.items, false), stack = host.stack();
  const finishRetention = host.retainMediaStart(request.signal, () => host.current(stack));
  try {
    const client = new QueueClient({send: value => stack.http.send({...value, signal: value.signal ? AbortSignal.any([value.signal, request.signal]) : request.signal})}, host.env().key);
    const check = () => { host.selectionCurrent(request.context, request.signal, current); if (!host.current(stack)) problem('stale_selection_context'); };
    const header = stack.queue.getSnapshot().view?.queue;
    if (!header) {
      if (stack.queue.getSnapshot().phase === 'preparing' || stack.service.getSnapshot().session) problem('queue_not_ready');
      const created = await client.create(segments, {signal: request.signal, retainQueueIds: host.retainedQueueIds(stack)});
      check();
      await stack.queue.open(created.queue.id, request.signal);
      check(); host.sync(); return {queue: created.queue};
    }
    let queue = await client.header(header.id, request.signal); check();
    // Each next insertion is immediately after current, so reverse requests retain pick order.
    const ordered = request.placement === 'next' ? [...segments].reverse() : segments;
    for (const segment of ordered) {
      check(); queue = (await client.addSegment(queue.id, queue.revision, request.placement, segment)).queue; check();
    }
    await stack.queue.open(queue.id, request.signal); check(); host.sync(); return {queue};
  } finally {finishRetention();}
}

/** The queue's own entry for the item now playing, when the loaded window holds it. */
function currentQueueEntry(stack: Stack, itemId: string) {
  const view = stack.queue.getSnapshot().view;
  return view?.items.find(entry => entry.entryId === view.queue.currentEntryId && entry.itemId === itemId);
}

/** The item's own facts did not answer. An audio queue entry carries its real kind, title,
 * album, poster and library: play on with those, so the dock draws and Pause is there (P2-74
 * E-web-14); the next source change reads the item again. Anything else keeps the failure. */
function queueFactsInstead(stack: Stack, itemId: string, current: () => boolean, error: unknown): undefined {
  if (!current()) return undefined;
  const entry = currentQueueEntry(stack, itemId), fallback = entry && queueEntrySubject(entry);
  if (!entry || !fallback) throw error;
  stack.subject = fallback; stack.itemId = itemId; stack.libraryId = entry.libraryId; stack.posterUrl = entry.posterUrl; stack.backdropUrl = undefined;
  if (fallback.kind === 'music' && !stack.placed) stack.expanded = false;
  stack.placed = true;
  return undefined;
}

export function selectSource(host: QueueRuntimeHost, stack: Stack, playback: PlaybackSnapshot, session: Session | undefined): void {
  if (playback.channel || !playback.session || !playback.itemId || !session || session.id !== playback.session.id || session.presentation.generation !== playback.session.generation || session.itemId !== playback.itemId) { if (stack.sessionKey) host.clearSourceResources(stack); return; }
  const key = `${playback.intentId}:${playback.session.id}:${playback.session.generation}`;
  if (stack.sessionKey === key) return;
  host.clearSourceResources(stack);
  stack.sessionKey = key;
  const itemId = playback.itemId, abort = new AbortController(); stack.metadataAbort = abort;
  const current = () => host.current(stack) && stack.sessionKey === key && !abort.signal.aborted;
  const read = async () => {
    if (stack.metadataItemId !== itemId || !stack.libraryId || !stack.subject) {
      const item = await host.readItemBounded(itemId, AbortSignal.any([abort.signal, stack.life.signal]), current)
        .catch(error => queueFactsInstead(stack, itemId, current, error));
      if (item) {
        if (!current()) return;
        if (item.id !== itemId || !item.available) problem('invalid_playback_item');
        stack.subject = item.subject; stack.itemId = itemId; stack.metadataItemId = itemId; stack.libraryId = item.libraryId;
        stack.posterUrl = item.posterUrl; stack.backdropUrl = item.backdropUrl;
        stack.showId = item.showId; stack.seasonId = item.seasonId;
        if (stack.subject.kind === 'music' && !stack.placed) stack.expanded = false;
        stack.placed = true;
      }
    }
    if (!current()) return;
    if (!stack.libraryId) {
      // Queue facts stood in and named no library: the item's offers name it.
      await stack.offers.select({itemId, sessionId: session.id});
      const learned = stack.offers.getSnapshot().data?.scope.libraryId;
      if (!current() || !learned) return;
      stack.libraryId = learned;
    }
    stack.offerBinding = {api: host.source().api, authority: host.authority(), sessionKey: key, libraryId: stack.libraryId, itemId, sessionId: session.id, generation: session.presentation.generation, playbackRun: playback.playbackRun};
    await stack.offers.select({libraryId: stack.libraryId, itemId, sessionId: session.id});
    if (!current()) return;
    const data = host.offers(stack, stack.service.getSnapshot());
    if (data) host.bindSubtitles(stack, data, key);
    const options = await stack.optionsClient.options(itemId, {versionId: session.versionId}, abort.signal);
    if (!current()) return;
    if (options.itemId !== itemId) problem('invalid_playback_options');
    stack.options = options;
    host.sync();
  };
  // PB-5: a failed read is tried once more after a moment; if that fails too, the player says so
  // with Try again (`retryMetadata`) instead of silently going without its panels.
  const attempt = (retried: boolean): void => {
    void read().then(() => { if (current() && host.runtimeProblem() === 'playback_metadata_failed') { host.setRuntimeProblem(undefined); host.sync(); } }, () => {
      if (!current()) return;
      if (!retried) {
        const timer = host.setTimer(() => { abort.signal.removeEventListener('abort', cancel); if (current()) attempt(true); }, METADATA_RETRY_MS);
        const cancel = () => host.clearTimer(timer);
        abort.signal.addEventListener('abort', cancel, {once: true});
        return;
      }
      stack.metadataRetry = () => { stack.metadataRetry = undefined; attempt(false); };
      host.setRuntimeProblem('playback_metadata_failed'); host.sync();
    });
  };
  attempt(false);
}

export async function advancePolicy(host: QueueRuntimeHost, stack: Stack, command: Extract<PolicyCommand, {kind: 'advance'}>): Promise<void> {
  const fence = command.fence;
  host.require(fence);
  // The canonical group readiness/end report advances the room. A member's private
  // queue must never advance independently of that authoritative occurrence.
  if (host.group()?.matches(fence)) problem('group_transport_required');
  const state = stack.queue.getSnapshot(), view = currentPolicyQueue(host.snapshot(), fence);
  if (!view || !view.next.available || view.next.entryId !== command.nextEntryId || view.postPlay.nextEntryId !== command.nextEntryId) problem('playback_policy_unavailable');
  const finishRetention = host.retainMediaStart(stack.life.signal, () => host.current(stack));
  try {
    const held = state.completion, playback = stack.service.getSnapshot();
    if (held && held.sessionId === fence.sessionId && held.intentId === playback.intentId && held.nextEntryId === command.nextEntryId && playback.phase === 'ended') {
      await stack.queue.continueCompletion(command.mode);
    } else {
      if (playback.phase !== 'ready' || command.mode === 'still-watching' || view.postPlay.passoutCheckDue || command.mode === 'automatic' && (!view.postPlay.autoplay || !view.postPlay.startDuringCredits)) problem('playback_policy_unavailable');
      if (command.mode === 'automatic') await stack.queue.advanceFromCredits(view.queue.id, view.queue.revision);
      else await stack.queue.next(view.queue.id, view.queue.revision);
    }
    if (host.current(stack)) host.sync();
  } finally {finishRetention();}
}

/** Execute only the prompt the caller actually rendered, through the existing policy and
 * queue controllers. No UI-supplied position, synthetic completion or queue ordering. */
export async function policyAction(host: QueueRuntimeHost, expected: QueuePlaybackFence, action: WebPlaybackPolicyAction): Promise<void> {
  const groupBinding = host.group();
  if (groupBinding?.matches(expected) && !groupBinding.active()?.you.mayControl) problem('group_control_forbidden');
  if (expected.kind !== 'queue') problem('playback_policy_unavailable');
  const fence = playbackFence(expected);
  host.require(fence); host.sync();
  const stack = host.require(fence), view = currentPolicyQueue(host.snapshot(), fence), policy = host.policyView();
  const playback = stack.service.getSnapshot();
  if (!view || !stack.expanded || !policy || playback.ended || sameFence(host.policyAdvanceFence(), fence)
    || playback.pendingSeek && action.kind !== 'undo') problem('playback_policy_unavailable');
  const prompt = policy.prompt;
  let command: PolicyCommand | null = null;
  switch (action.kind) {
    case 'skip': {
      const marker = prompt?.kind === 'skip' ? prompt.marker : prompt?.kind === 'up-next' ? prompt.offer.marker : undefined;
      if (!marker || marker.id !== action.markerId || prompt?.kind === 'up-next' && marker.kind !== 'credits' && marker.kind !== 'outro') problem('playback_policy_unavailable');
      command = host.policy().skip(fence, action.markerId);
      if (command?.kind !== 'seek' || command.markerId !== action.markerId) problem('playback_policy_unavailable');
      break;
    }
    case 'dismiss':
      if (prompt?.kind !== 'skip' || prompt.marker.id !== action.markerId || !host.policy().dismiss(fence)) problem('playback_policy_unavailable');
      break;
    case 'undo':
      if (policy.undo?.marker.id !== action.markerId) problem('playback_policy_unavailable');
      command = host.policy().undoSkip(fence, (host.env().timers?.now ?? Date.now)());
      break;
    case 'watch-credits':
      if (prompt?.kind !== 'up-next' || prompt.offer.id !== action.offerId || !host.policy().dismiss(fence)) problem('playback_policy_unavailable');
      break;
    case 'play-now':
    case 'continue': {
      const held = stack.queue.getSnapshot().completion;
      const finished = !!held && held.sessionId === fence.sessionId && held.intentId === stack.service.getSnapshot().intentId;
      if (!view.next.available || view.next.entryId !== action.nextEntryId || view.postPlay.nextEntryId !== action.nextEntryId
        || (action.kind === 'continue' ? !finished || policy.attention !== 'still-watching' : policy.attention || !finished && prompt?.kind !== 'up-next')) problem('playback_policy_unavailable');
      command = host.policy().advance(fence, action.kind === 'continue' ? 'still-watching' : 'manual');
      break;
    }
    case 'cancel-autoplay': {
      const held = stack.queue.getSnapshot().completion;
      if (!held || held.sessionId !== fence.sessionId || held.intentId !== stack.service.getSnapshot().intentId || policy.attention || policy.replay || !held.nextAvailable) problem('playback_policy_unavailable');
      host.policy().cancelAutoplay(fence);
      break;
    }
    default: problem('playback_policy_unavailable');
  }
  if (action.kind === 'skip' || action.kind === 'undo' || action.kind === 'play-now' || action.kind === 'continue') {
    if (!command) problem('playback_policy_unavailable');
    if (command.kind === 'seek') {
      const from = stack.service.getSnapshot().positionSeconds;
      if (!await host.groupTransport('seek', fence, command.positionSeconds)) stack.service.seek(command.positionSeconds);
      if (command.reason === 'manual-skip') stack.service.markerSkipped(command.markerId, 'manual', from);
    } else if (command.kind === 'advance') { host.setPolicyAdvanceFence(fence); await advancePolicy(host, stack, command); }
  }
  if (host.current(stack)) host.sync();
}

export function queueList(host: QueueRuntimeHost, fence: PlaybackFence, selectionLife?: WebQueueListLifetime): WebQueueListPort | undefined {
  const stack = host.stack();
  if (fence.kind !== 'queue' || !host.matches(fence) || stack.mode !== 'server' || host.group()?.matches(fence)) return;
  const view = stack.queue.getSnapshot().view, queueId = view?.queue.id;
  const session = stack.queue.currentSession(), playback = stack.service.getSnapshot();
  if (!view || !queueId || view.queue.currentEntryId !== fence.entryId || !session?.versionId
    || session.id !== fence.sessionId || session.itemId !== fence.itemId || session.presentation.generation !== fence.generation
    || session.queue?.queueId !== queueId || session.queue.entryId !== fence.entryId) return;
  const target = Object.freeze({sessionId: session.id, generation: fence.generation, playbackRun: playback.playbackRun,
    itemId: session.itemId, versionId: session.versionId, queueId, entryId: fence.entryId});
  const current = () => host.current(stack) && host.matches(fence) && !host.group()?.matches(fence)
    && stack.queue.currentSession()?.versionId === target.versionId && stack.queue.getSnapshot().view?.queue.id === queueId;
  const selectable = session.kind === 'vod' && session.role === 'local' && !host.group()?.active() && !!selectionLife;
  return createWebQueueListPort({fence, queueId, selectable, isCurrent: current,
    read: () => ({view: stack.queue.getSnapshot().view, workspace: stack.queue.workspace.service.getSnapshot()}),
    subscribe: listener => {const off = host.subscribe(listener), offQueue = stack.queue.subscribe(listener), offWorkspace = stack.queue.workspace.service.subscribe(listener);return () => {off();offQueue();offWorkspace();};},
    play: (entryId, revision, gesture) => {
      if (!selectable || !selectionLife || !current() || gesture.signal.aborted || !gesture.isCurrent()) problem('stale_playback_command');
      // The drawer proves this gesture before dispatch. The original route
      // then owns the command across its intended output/drawer replacement;
      // core owns the old-to-accepted output fence and late admission cleanup.
      const isCurrent = () => host.current(stack) && !host.group()?.active() && !selectionLife.signal.aborted && selectionLife.isCurrent();
      if (!isCurrent()) problem('stale_playback_command');
      return stack.queue.selectQueuedEntry(target, {queueId, expectedRevision: revision, entryId},
        {signal: AbortSignal.any([stack.life.signal, host.life().signal, selectionLife.signal]), isCurrent});
    },
    edit: (revision, intent, lifetime) => {
      const isCurrent = () => current() && !lifetime.signal.aborted && lifetime.isCurrent();
      if (!isCurrent()) problem('stale_playback_command');
      return stack.queue.editQueue(target, revision, intent,
        {signal: AbortSignal.any([stack.life.signal, host.life().signal, lifetime.signal]), isCurrent});
    },
  });
}

export async function onTransition(host: QueueRuntimeHost, transition: DockTransition): Promise<void> {
  host.assertCurrent();
  if (transition.state !== host.dock().getSnapshot()) problem('stale_dock_transition');
  let retentionOperation = host.operation();
  const finishRetention = transition.effects.length ? host.retainMediaStart(host.life().signal, () => retentionOperation === host.operation()) : () => {};
  try {
    host.setReplacing(true);
    try { for (const effect of transition.effects) {
      const key = host.key(effect.playback.fence);
      if (effect.kind === 'restore') {
        const bookmark = host.bookmarks().get(key);
        const fence = effect.playback.fence;
        if (!bookmark) problem('playback_bookmark_unavailable');
        if (fence.kind === 'local') {
          const saved = bookmark.local;
          if (!saved || saved.lease.released || saved.lease.preparation.items[saved.index]?.id !== fence.entryId) problem('playback_bookmark_unavailable');
          const serial = host.nextOperation(); retentionOperation = serial;
          const stack = host.replace('local'), controller = host.bindLocalQueue(stack, saved.lease);
          stack.expanded = false;
          await controller.start({signal: stack.life.signal, isCurrent: () => host.current(stack) && serial === host.operation()}, {index: saved.index, startPositionMs: saved.positionMs, autoplay: false});
          if (!host.current(stack) || serial !== host.operation()) return;
          const accepted = await host.awaitLocalPaused(stack, serial);
          if (accepted.sessionId === fence.sessionId) problem('playback_restore_failed');
          continue;
        }
        const queueId = bookmark.queueId, channel = bookmark.channel?.channel;
        if (fence.kind === 'queue' && !queueId || fence.kind === 'channel' && !channel) problem('playback_bookmark_unavailable');
        if (fence.kind === 'channel' && channel && (channel.id !== fence.channelId || channel.provenance !== fence.channelKind || channel.sourceId !== fence.sourceId || channel.generation !== fence.channelGeneration)) problem('stale_channel_bookmark');
        const serial = host.nextOperation(); retentionOperation = serial;
        const stack = host.replace();
        stack.subject = bookmark.subject; stack.itemId = fence.kind === 'queue' ? fence.itemId : undefined; stack.libraryId = bookmark.libraryId; stack.channel = bookmark.channel;
        stack.posterUrl = bookmark.posterUrl; stack.backdropUrl = bookmark.backdropUrl; stack.expanded = false;
        if (fence.kind === 'queue') {
          if (!queueId) problem('playback_bookmark_unavailable');
          await stack.queue.restorePaused(queueId, fence.entryId, effect.playback.positionSeconds);
        } else {
          if (!channel) problem('playback_bookmark_unavailable');
          await stack.service.playChannel({kind: channel.provenance, sourceId: channel.sourceId, channelId: channel.id, generation: channel.generation}, {state: 'paused'});
        }
        if (!host.current(stack) || serial !== host.operation()) return;
        host.sync();
        const accepted = await host.awaitFence(stack, serial);
        if (stack.service.getSnapshot().intent !== 'paused') problem('playback_restore_failed');
        if (fence.kind === 'channel') {
          if (accepted.kind !== 'channel') problem('playback_restore_failed');
          const linear = stack.service.getSnapshot().linear, target = effect.playback.positionSeconds;
          if (!linear || target < Number(linear.media.windowStartUs) / 1e6 || target > Number(linear.media.windowEndUs) / 1e6) { host.setRuntimeProblem('channel_bookmark_expired'); host.sync(); problem('channel_bookmark_expired'); }
          // The attached real paused session owns this retained window. An expired bookmark
          // is reported above, never clamped or represented as a successful restoration.
          stack.service.seek(target);
        }
      } else if (host.snapshot().fence && sameFence(host.snapshot().fence, effect.playback.fence)) {
        const stack = host.stack();
        if (stack.mode === 'local') stack.web.slot.pause();else stack.service.pause();host.rememberCurrent();
        if (effect.kind === 'stop') host.retire(stack);
      } else if (!host.retiredFences().has(key)) problem('stale_dock_effect');
    } } finally { host.setReplacing(false); }
    if (host.stack().retired) { const next = host.makeStack(); host.setStack(next); host.activate(next); }
    host.sync();
  } finally {finishRetention();}
}

export async function restoreBookmark(host: QueueRuntimeHost, expected: PlaybackFence, playExplicitly = false): Promise<PlaybackFence> {
  host.assertCurrent();
  const state = host.dock().getSnapshot();
  const transition = sameFence(state.parkedAudio?.fence, expected) ? host.dock().restoreAudio() : sameFence(state.resumeVideo?.fence, expected) ? host.dock().restoreVideo() : undefined;
  if (!transition) problem('stale_playback_command');
  const serial = host.operation() + 1;
  await onTransition(host, transition);
  if (serial !== host.operation()) problem('stale_playback_command');
  const stack = host.stack(), fence = expected.kind === 'local' ? stack.local?.controller.getSnapshot().fence : await host.awaitFence(stack, serial);
  if (!fence) problem('playback_restore_failed');
  if (fence.kind !== expected.kind || fence.sessionId === expected.sessionId) problem('playback_restore_failed');
  if (playExplicitly) await host.resume(fence);
  return fence;
}
