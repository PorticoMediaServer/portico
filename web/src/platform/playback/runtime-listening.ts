import {audiobookRates, listeningRates} from '@core/listening.ts';
import {PlayerChaptersService} from '@core/player-chapters.ts';
import {LyricsService} from '@core/lyrics.ts';
import {sameFence, type PlaybackDock, type PlaybackFence, type PlayerSessionChoices} from '../../../../packages/player-core/src/index.ts';
import type {RuntimeGroupBinding} from './group-binding.ts';
import {createWebListeningPort, type WebListeningLifetime, type WebListeningPort} from './listening-port.ts';
import {admitCommand, createQueueEditOpening, localEditOutput} from './queue-edit-adapter.ts';
import type {WebQueueListPort, WebQueueListSnapshot} from './queue-list.ts';
import type {WebPlaybackEnvironment, WebPlaybackRuntimeSnapshot, WebPlaybackSource} from './types.ts';
import {problem, type Stack, type WebSleepEnd} from './runtime-types.ts';

export type ListeningRuntimeHost = Readonly<{
  stack(): Stack;
  source(): WebPlaybackSource;
  env(): WebPlaybackEnvironment;
  dock(): PlaybackDock;
  choices(): PlayerSessionChoices;
  life(): AbortController;
  group(): RuntimeGroupBinding | undefined;
  snapshot(): WebPlaybackRuntimeSnapshot;
  current(stack?: Stack): boolean;
  matches(fence: PlaybackFence): boolean;
  require(fence: PlaybackFence): Stack;
  offered(fence: PlaybackFence, command: 'seek'): Stack;
  playbackRun(): number;
  subscribe(listener: () => void): () => void;
  queueList(fence: PlaybackFence, selectionLife?: WebListeningLifetime): WebQueueListPort | undefined;
  seek(fence: PlaybackFence, seconds: number): Promise<void>;
  retainMediaStart(signal?: AbortSignal, isCurrent?: () => boolean): () => void;
  setTimer(fn: () => void, ms: number): unknown;
  clearTimer(timer: unknown): void;
  sync(): void;
}>;

/** One admitted listening record over the existing chapters, lyrics and queue services.
 * It captures the exact original target/source and re-proves it after every external predicate;
 * any observed retirement disposes the record synchronously. No player/session/queue/engine is created. */
export function captureListening(host: ListeningRuntimeHost, fence: PlaybackFence, lifetime: WebListeningLifetime): WebListeningPort | undefined {
  const stack = host.stack();
  if (fence.kind !== 'queue' || !host.matches(fence) || stack.mode !== 'server' || host.group()?.matches(fence)) return undefined;
  const view = stack.queue.getSnapshot().view, session = stack.queue.currentSession(), playback = stack.service.getSnapshot();
  const libraryId = stack.libraryId, versionId = session?.versionId;
  if (!view?.queue.id || view.queue.currentEntryId !== fence.entryId || !session || !libraryId || !versionId) return undefined;
  if (session.role !== 'local' || (session.kind !== 'vod' && session.kind !== 'audio')) return undefined;
  if (session.id !== fence.sessionId || session.itemId !== fence.itemId || session.presentation.generation !== fence.generation
    || session.queue?.queueId !== view.queue.id || session.queue.entryId !== fence.entryId) return undefined;
  const target = Object.freeze({libraryId, itemId: fence.itemId, sessionId: session.id, sessionGeneration: fence.generation,
    sourceId: fence.sourceId, versionId, playbackRun: playback.playbackRun, queueId: view.queue.id, entryId: fence.entryId});
  // Original transports are captured once; no later source lookup supplies them.
  const source = host.source(), request = source.api.request.bind(source.api), requestLyrics = source.api.requestLyrics.bind(source.api);
  const life = host.life();
  const combined = (command?: AbortSignal) => AbortSignal.any([stack.life.signal, life.signal, lifetime.signal, ...(command ? [command] : [])]);
  /** Exact original record: runtime/stack/source plus the captured target, run and queue occurrence. */
  const internal = (): boolean => {
    if (!host.current(stack) || !host.matches(fence) || host.group()?.matches(fence) || host.source() !== source) return false;
    const current = stack.queue.currentSession(), currentView = stack.queue.getSnapshot().view, currentPlayback = stack.service.getSnapshot();
    return !!current && !!currentView && stack.libraryId === target.libraryId
      && current.id === target.sessionId && current.itemId === target.itemId && current.versionId === target.versionId
      && current.presentation.generation === target.sessionGeneration && current.role === 'local'
      && current.queue?.queueId === target.queueId && current.queue.entryId === target.entryId
      && currentView.queue.id === target.queueId && currentView.queue.currentEntryId === target.entryId
      && currentPlayback.playbackRun === target.playbackRun;
  };
  /** The host callback may retire runtime or source, so the exact owner is re-proved after it. */
  const reprove = (): boolean => {
    if (!internal()) return false;
    if (lifetime.signal.aborted || !lifetime.isCurrent()) return false;
    return internal();
  };
  const bindCommand = (command: WebListeningLifetime) => ({signal: combined(command.signal),
    isCurrent: () => !command.signal.aborted && command.isCurrent() && reprove()});
  /** Caller, runtime, stack, source and queue retirement all retire this record. */
  const observe = (retired: () => void) => {
    const check = () => {if (!reprove()) retired();};
    const offs = [host.subscribe(check), stack.queue.subscribe(check)];
    const lifeOff = () => {stack.life.signal.removeEventListener('abort', check); life.signal.removeEventListener('abort', check);};
    stack.life.signal.addEventListener('abort', check, {once: true});
    life.signal.addEventListener('abort', check, {once: true});
    return () => {for (const off of offs) off(); lifeOff();};
  };
  const queue = host.queueList(fence, lifetime);
  return createWebListeningPort({fence, lifetime, target, current: reprove, request, requestLyrics,
    chaptersTarget: {libraryId, itemId: fence.itemId, sessionId: session.id, sessionGeneration: fence.generation, sourceId: fence.sourceId},
    makeChapters: api => new PlayerChaptersService({api, scope: source.contentScope, pageSize: 100}),
    makeLyrics: api => new LyricsService(api, source.contentScope, {libraryId, itemId: fence.itemId, sessionId: session.id,
      sessionGeneration: fence.generation, sourceId: fence.sourceId}),
    lyricsEnabled: () => source.settings.lyricsEnabled?.() === true, observe, ...(queue ? {queue} : {}),
    policy: (expected, revision, intent, command) => {
      if (!reprove()) return Promise.reject(new Error('stale_listening_port'));
      return stack.queue.setQueuePolicy(expected, revision, intent, bindCommand(command));
    },
    save: (queueId, name, command, onProgress) => {
      if (!reprove()) return Promise.reject(new Error('stale_listening_port'));
      const bound = bindCommand(command);
      return stack.queue.saveAsPlaylist(queueId, name, save => {if (bound.isCurrent()) onProgress?.(save);}, bound.signal);
    },
    seek: (seconds, command) => {
      const bound = bindCommand(command);
      if (!bound.isCurrent()) return Promise.reject(new Error('stale_listening_port'));
      return host.seek(fence, seconds);
    },
    openEdit: (shown: WebQueueListSnapshot, command: WebListeningLifetime) => {
      if (!reprove()) throw new Error('stale_listening_port');
      const v1Target = {sessionId: target.sessionId, generation: target.sessionGeneration, playbackRun: target.playbackRun,
        itemId: target.itemId, versionId: target.versionId, queueId: target.queueId, entryId: target.entryId};
      /** Local proof from the exact original server stack and dock. Every external getter —
       * including the group callbacks — runs before one final pure owner/output sample that is
       * the value returned, so a takeover inside a getter cannot ride a stale earlier sample. */
      const readOutput = () => localEditOutput({originalStack: stack, originalSource: source, fence, target, same: sameFence,
        read: () => {
          const owner = () => {
            const stackNow = host.stack(), dock = host.dock().getSnapshot(), snap = host.snapshot();
            const currentSession = stackNow.queue.currentSession(), currentView = stackNow.queue.getSnapshot().view, currentPlayback = stackNow.service.getSnapshot();
            return {stack: stackNow, source: host.source(), fence: snap.fence, presentation: !!snap.presentation,
              dockLocalFence: dock.local?.fence, dockRemote: dock.remote !== null,
              session: currentSession ? {id: currentSession.id, role: currentSession.role, kind: currentSession.kind, itemId: currentSession.itemId, versionId: currentSession.versionId,
                generation: currentSession.presentation.generation, queueId: currentSession.queue?.queueId, entryId: currentSession.queue?.entryId} : undefined,
              viewQueueId: currentView?.queue.id, viewEntryId: currentView?.queue.currentEntryId ?? undefined, playbackRun: currentPlayback.playbackRun};
          };
          const before = owner();
          const groupTaken = !!host.group()?.active() || !!host.group()?.matches(fence);
          // Runtime currentness is evaluated after the group callbacks, then one pure sample follows.
          const currentAfter = host.current(before.stack);
          const after = owner();
          const stable = after.stack === before.stack && after.playbackRun === before.playbackRun
            && Object.is(after.dockLocalFence, before.dockLocalFence) && Object.is(after.fence, before.fence)
            && after.viewQueueId === before.viewQueueId && after.viewEntryId === before.viewEntryId;
          return {...after, group: groupTaken, current: currentAfter && !groupTaken && stable};
        }});
      const reader = stack.queue.captureEditWindow(v1Target, shown.revision ?? '', {signal: combined(command.signal),
        isCurrent: () => admitCommand(reprove, command), readOutput});
      return createQueueEditOpening({reader, current: reprove, lifetime: command, currentEntryId: target.entryId,
        hidden: entryId => shown.entries.find(entry => entry.entryId === entryId)?.hidden === true});
    },
  });
}

/** On a book part with known chapters, Previous and Next move by chapter: a seek within the
 * part, else a jump to the chapter's part at the chapter's start (MU-7). False: not a book
 * with chapters here, so the queue moves as for any other item. */
export async function chapterMove(host: ListeningRuntimeHost, stack: Stack, fence: PlaybackFence, direction: 'previous' | 'next'): Promise<boolean> {
  if (fence.kind !== 'queue') return false;
  const target = stack.queue.chapterTarget(direction);
  if (target === undefined) return false;
  if (target === null) return true;
  await stack.queue.playChapter(target, seconds => host.seek(fence, seconds));
  return true;
}

/** A row of the book's chapter list (MU-2): a seek within the playing part, else that
 * chapter's part from the chapter's start, in this run's queue. */
export async function playBookChapter(host: ListeningRuntimeHost, fence: PlaybackFence, chapterId: string): Promise<void> {
  if (fence.kind !== 'queue' || host.group()?.matches(fence)) problem('playback_command_unavailable');
  const stack = host.offered(fence, 'seek'), chapter = stack.queue.bookChaptersView()?.chapters.find(row => row.id === chapterId);
  if (!chapter) problem('playback_command_unavailable');
  const finishRetention = host.retainMediaStart(stack.life.signal, () => host.current(stack));
  try {
    await stack.queue.playChapter(chapter, seconds => host.seek(fence, seconds));
    if (host.current(stack)) host.sync();
  } finally {finishRetention();}
}

/** desktop-player f3: the drawn sleep timer. Only this stack's own local playback is
 * paused when the time is up; a group is never paused on a member's behalf. */
export function sleepTimer(host: ListeningRuntimeHost, fence: PlaybackFence): Readonly<{minutes: number; endsAt: number}> | Readonly<{end: WebSleepEnd}> | undefined {
  if (fence.kind !== 'queue' || !host.matches(fence)) return undefined;
  const stack = host.stack(), timer = stack.sleepTimer;
  if (!timer) {
    // "End of this title, chapter or book" is the listening service's own timer (MU-6).
    const end = stack.queue.listening.getSnapshot().timer?.choice;
    return end === 'item' || end === 'chapter' || end === 'book' ? Object.freeze({end}) : undefined;
  }
  // It lasts the whole run, across the queue's next tracks and parts (MU-3). A new playback is
  // a new run: retire the armed record rather than show a timer that no longer applies.
  if (timer.run !== host.playbackRun()) { clearSleepTimer(host, stack); return undefined; }
  return Object.freeze({minutes: timer.minutes, endsAt: timer.endsAt});
}

/** Minutes (0 is off), or the end of this title, chapter or book. One timer at a time. */
export async function setSleepTimer(host: ListeningRuntimeHost, fence: PlaybackFence, choice: number | WebSleepEnd): Promise<void> {
  if (fence.kind !== 'queue') problem('playback_sleep_unavailable');
  const stack = host.require(fence);
  if (typeof choice === 'string') {
    if (!['item', 'chapter', 'book'].includes(choice)) problem('playback_sleep_unavailable');
    clearSleepTimer(host, stack);
    host.sync();
    // The core verifies the chapter's timing or the book's order before it arms (MU-6).
    await stack.queue.listening.setTimer(choice);
    if (host.current(stack)) host.sync();
    return;
  }
  const minutes = choice;
  if (!Number.isSafeInteger(minutes) || ![0, 15, 30, 45, 60, 90].includes(minutes)) problem('playback_sleep_unavailable');
  clearSleepTimer(host, stack);
  if (stack.queue.listening.getSnapshot().timer) stack.queue.listening.clearTimer();
  if (!minutes) { host.sync(); return; }
  const token = {}, run = host.playbackRun();
  const timer = host.setTimer(() => {
    // Only the record that armed this callback may act; a successor timer owns the stack now.
    if (stack.sleepTimer?.token !== token) return;
    clearSleepTimer(host, stack);
    // Whatever this run is playing now is paused: the track it was set on may be long gone.
    if (!host.current(stack) || host.playbackRun() !== run) { host.sync(); return; }
    const now = host.snapshot()?.fence;
    if (now && host.group()?.matches(now)) { host.sync(); return; }
    try { stack.service.pause(); } catch { /* a retired session has nothing to pause */ }
    host.sync();
  }, minutes * 60_000);
  stack.sleepTimer = Object.freeze({minutes, endsAt: (host.env().timers?.now ?? Date.now)() + minutes * 60_000, timer, run, token});
  host.sync();
}

export function clearSleepTimer(host: Pick<ListeningRuntimeHost, 'clearTimer'>, stack: Stack): void {
  const timer = stack.sleepTimer;
  if (!timer) return;
  stack.sleepTimer = undefined;
  host.clearTimer(timer.timer);
}

/** Drawn temporary choices for this actual subject; music and live have no speed picker. */
export function speedChoices(host: ListeningRuntimeHost, fence: PlaybackFence): readonly number[] {
  const stack = host.require(fence);
  if (host.group()?.matches(fence)) return Object.freeze([]);
  return Object.freeze([...(fence.kind === 'channel' || stack.subject?.kind === 'music' ? [] : stack.subject?.kind === 'audiobook' ? audiobookRates : stack.subject?.kind === 'video' ? listeningRates : [])]);
}

export function setSpeed(host: ListeningRuntimeHost, fence: PlaybackFence, speed: number): void {
  const stack = host.require(fence);
  const fixed = stack.subject?.kind === 'music' || fence.kind === 'channel';
  if (!Number.isFinite(speed) || (fixed ? speed !== 1 : !speedChoices(host, fence).includes(speed))) problem('playback_speed_unavailable');
  host.choices().set('speed', fence.playback, speed); stack.service.setPlaybackRate(speed); stack.web.setRate(speed); host.sync();
}

export function stepSpeed(host: ListeningRuntimeHost, fence: PlaybackFence, direction: -1 | 1): void {
  host.require(fence);
  const snapshot = host.snapshot(), presentation = snapshot.presentation;
  if ((direction !== -1 && direction !== 1) || !presentation?.expanded || presentation.finished || snapshot.dock.remote?.phase === 'playing') problem('playback_speed_unavailable');
  const offered = speedChoices(host, fence), rate = presentation.rate;
  if (!offered.length) problem('playback_speed_unavailable');
  const next = direction === 1 ? offered.find(value => value > rate) : [...offered].reverse().find(value => value < rate);
  if (next !== undefined) setSpeed(host, next);
}
