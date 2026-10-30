import {PlayerChaptersService, type PlayerChaptersTarget} from '../../../../../packages/client-core/src/player-chapters';
import type {LibraryContentApi, ContentScope} from '../../../../../packages/client-core/src/library-content';
import type {NativeListeningReadPort, NativeListeningReadSnapshot, NativePlaybackReadLifetime} from './types';
import {encodedRoutePath} from '../../../../../packages/contracts/src/routes';

type Facts = Extract<NativeListeningReadSnapshot, {phase: 'current'}>;
type ReadFacts = Pick<Facts, 'rate' | 'preferences'>;

/** One read lease over existing listening/native facts and the canonical chapter reader.
 * This intentionally contains no playback/policy commands, timer or start/retry adapter. */
export function createNativeListeningReadPort(options: Readonly<{
  target: PlayerChaptersTarget & Readonly<{sourceId: string}>;
  scope: ContentScope;
  lifetime: NativePlaybackReadLifetime;
  current: () => boolean;
  facts: () => ReadFacts;
  request: LibraryContentApi['request'];
  subscribe: readonly ((listener: () => void) => () => void)[];
}>): NativeListeningReadPort {
  const target = Object.freeze({...options.target}), scope = Object.freeze({...options.scope});
  if (!target.sourceId) throw new Error('native_listening_source_required');
  const chapterPath = encodedRoutePath('get_playback_sessions_id_chapters', {id: encodeURIComponent(target.sessionId)});
  const abort = new AbortController(), listeners = new Set<() => void>(), offs: (() => void)[] = [];
  let retired = false;
  let snapshot: NativeListeningReadSnapshot = Object.freeze({phase: 'retired'});
  const valid = () => {
    try {return !retired && !abort.signal.aborted && !options.lifetime.signal.aborted && options.lifetime.isCurrent() && options.current();}
    catch {return false;}
  };
  const notify = () => {for (const listener of [...listeners]) {try {listener();} catch { /* An observer cannot change this read or its retirement. */ }}};
  const retire = () => {
    if (retired) return;
    retired = true; snapshot = Object.freeze({phase: 'retired'});
    abort.abort(); options.lifetime.signal.removeEventListener('abort', retire);
    for (const off of offs.splice(0)) {try {off();} catch { /* Finish retiring every original reader. */ }}
    chapters.dispose(); notify(); listeners.clear();
  };
  const assert = () => {if (!valid()) {retire(); throw new Error('native_listening_read_retired');}};
  const chapters = new PlayerChaptersService({scope, api: {request: async <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
    assert();
    if (method !== 'GET' || body !== undefined || path !== chapterPath && !path.startsWith(chapterPath + '?')) throw new Error('native_listening_read_only');
    const read = new AbortController(), cancel = () => read.abort();
    abort.signal.addEventListener('abort', cancel, {once: true});
    if (signal?.aborted) cancel(); else signal?.addEventListener('abort', cancel, {once: true});
    try {
      assert(); if (read.signal.aborted) throw new Error('native_listening_read_retired');
      const value = await options.request<T>(path, 'GET', undefined, read.signal);
      assert(); if (read.signal.aborted) throw new Error('native_listening_read_retired');
      return value;
    } finally {abort.signal.removeEventListener('abort', cancel); signal?.removeEventListener('abort', cancel);}
  }}});
  const project = (): Facts => {
    const facts = options.facts(), page = chapters.getSnapshot(), data = page.data;
    return Object.freeze({phase: 'current',
      rate: Object.freeze({selected: facts.rate.selected, ...(facts.rate.effective === undefined ? {} : {effective: facts.rate.effective}),
        ...(facts.rate.supported === undefined ? {} : {supported: Object.freeze([...facts.rate.supported])})}),
      preferences: Object.freeze({phase: facts.preferences.phase,
        values: facts.preferences.values ? Object.freeze({revision: facts.preferences.values.revision, bookRate: facts.preferences.values.bookRate, passoutMinutes: facts.preferences.values.passoutMinutes}) : null,
        ...(facts.preferences.audiobook ? {audiobook: Object.freeze({skipBackMs: facts.preferences.audiobook.skipBackMs,
          skipForwardMs: facts.preferences.audiobook.skipForwardMs, resumeRewindMs: facts.preferences.audiobook.resumeRewindMs})} : {})}),
      chapters: Object.freeze({phase: page.phase, pagination: Object.freeze({cursor: page.pagination.cursor, canPrevious: page.pagination.canPrevious}),
        data: data ? Object.freeze({scope: Object.freeze({serverId: data.scope.serverId, libraryId: data.scope.libraryId, itemId: data.scope.itemId,
          viewerFence: data.scope.viewerFence, sessionId: data.scope.sessionId, generation: data.scope.generation, sourceId: data.scope.sourceId}),
          revision: data.revision, status: data.status, reason: data.reason, duration: data.duration, totalCount: data.totalCount, nextCursor: data.nextCursor,
          chapters: Object.freeze(data.chapters.map(chapter => Object.freeze({id: chapter.id, index: chapter.index, title: chapter.title,
            startSeconds: chapter.startSeconds, endSeconds: chapter.endSeconds, ...(chapter.thumbnailUrl === undefined ? {} : {thumbnailUrl: chapter.thumbnailUrl})})))}) : null}),
    });
  };
  const update = () => {
    if (!valid()) {retire(); return;}
    try {snapshot = project();} catch {retire(); return;}
    if (!valid()) {retire(); return;}
    notify();
  };
  const read = async (work: () => Promise<void>) => {assert(); await work(); assert();};
  const watch = (subscribe: (listener: () => void) => () => void) => {
    if (retired) return;
    try {const off = subscribe(update); if (retired) off(); else offs.push(off);} catch {retire();}
  };
  options.lifetime.signal.addEventListener('abort', retire, {once: true});
  watch(chapters.subscribe);
  for (const subscribe of options.subscribe) watch(subscribe);
  update();
  return Object.freeze({
    current: () => {if (!valid()) retire(); return !retired;},
    getSnapshot: () => {if (!valid()) retire(); return snapshot;},
    subscribe: (listener: () => void) => {if (retired) return () => {}; listeners.add(listener); return () => {listeners.delete(listener);};},
    readChapters: () => read(() => chapters.select(target)),
    readNextChapterPage: () => read(() => chapters.next()),
    readPreviousChapterPage: () => read(() => chapters.previous()),
    dispose: retire,
  });
}
