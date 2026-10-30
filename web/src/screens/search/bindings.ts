import {forwardIdentity} from '../detail/context';
import {entryStartDisplay} from '../../platform/playback/subject';
import {SearchService, readSearchHistory, clearSearchHistory, deleteSearchHistoryEntry, type SearchEntry, type SearchGroup, type SearchQuery} from '@core/search.ts';
import {serviceI18n} from '@core/presentation/service-text.ts';
import {guideWindow, parseChannelGuide, type GuideChannel} from '@core/channel-guide.ts';
import {nowNext, type GuideProgram} from '@core/guide/index.ts';
import {legacyProgram} from '@core/guide/legacy-source.ts';
import {channelTint, programmeFactParts, type ChannelMarkModel, type FactPart} from '@portico/client-core/kit-model';
import {routePath} from '../../../../packages/contracts/src/routes.ts';
import {screenLifetime, type ScreenContext} from '../detail/context';
import {createDetailBindings} from '../detail/bindings';
import {createShowBindings} from '../detail/show-bindings';
import {SEARCH_CHIPS} from '@core/presentation/search-model.ts';
import type {SearchData, SearchGroupResult} from '@core/search.ts';
import {SearchGenresController} from './genres';
import {SearchOnNowController} from './on-now';
import {createSearchRows} from './rows';

/** Group narrowing shares counts only within the same actual base query.
 * Page size and recording do not change membership; library scope/order do. */
const summaryKey = (data: SearchData) => JSON.stringify([data.query.q, data.query.sort, data.query.direction,
  data.query.libraryIds, data.query.searchMode, data.scope.serverId, data.scope.viewerFence, data.revision]);
const requestKey = (query: SearchQuery | null) => query && JSON.stringify([query.q, query.group, query.groups, query.sort, query.direction, query.libraryIds]);

/** A live result is the channel entity row, never a media item. */
type ChannelResult = Extract<SearchEntry, {kind: 'person' | 'channel'}> & Readonly<{kind: 'channel'}>;

/** The drawn Live TV result is a channel card: its channel row and on-air programme come from
 * one bounded guide read for exactly the matched channel ids. Nothing here ranks channels or
 * follows a continuation; a refused read leaves the plain channel row instead. */
export type SearchLiveCard = Readonly<{
  channel: Readonly<{sourceId: string; id: string; name: string; number?: string; favorite?: boolean; mark?: ChannelMarkModel; logoUrl?: string}>;
  programme?: Readonly<{title: string; facts: readonly FactPart[]; progress?: number; remaining?: string; live?: boolean; isNew?: boolean; premiere?: boolean; recording?: boolean}>;
  tint?: string;
}>;

function liveProgramme(programme: GuideProgram, now: number, timezone: string, progress: number): NonNullable<SearchLiveCard['programme']> {
  const parts = programmeFactParts({startMs: programme.start, endMs: programme.end,
    episode: programme.episode ? {mode: 'seasonal', season: programme.episode.season, episode: programme.episode.number} : undefined,
    subtitle: programme.subtitle}, {t: serviceI18n().t, locale: serviceI18n().region.formatLocale, timeZone: timezone});
  const episodic = parts.some(part => part.id === 'episode-label' || part.id === 'programme-subtitle');
  const remaining = programme.end - now;
  return Object.freeze({title: programme.title,
    facts: Object.freeze(parts.filter(part => episodic ? part.id === 'episode-label' || part.id === 'programme-subtitle' : part.id === 'time-range')),
    progress, remaining: remaining < 3_600_000 ? serviceI18n().t('guide.minutesLeft', {count: Math.max(1, Math.round(remaining / 60_000))})
      : serviceI18n().t('guide.timeLeft', {time: serviceI18n().duration(Math.round(remaining / 60_000) * 60)}),
    live: programme.flags?.live, isNew: programme.flags?.new, premiere: programme.flags?.premiere, recording: programme.recording?.state === 'recording'});
}

/** Only the identity, mark, logo and actual on-air facts this guide answer carries. */
function liveCard(channel: GuideChannel, now: number, timezone: string): SearchLiveCard {
  const programmes = channel.programmes.map(legacyProgram);
  const current = nowNext(programmes, now), onNow = current.now;
  const mark = channel.mark ? Object.freeze({...channel.mark, sourceId: channel.sourceId, id: channel.id}) : undefined;
  return Object.freeze({
    channel: Object.freeze({sourceId: channel.sourceId, id: channel.id, name: channel.name,
      ...(channel.number ? {number: channel.number} : {}), ...(channel.favorite ? {favorite: true} : {}),
      ...(mark ? {mark} : {}), ...(channel.logoPath ? {logoUrl: channel.logoPath} : {})}),
    ...(onNow ? {programme: liveProgramme(onNow, now, timezone, current.progress)} : {}),
    ...(channel.mark ? {tint: channelTint(channel.mark.seed)} : {}),
  });
}

const NO_LIVE_CARDS: ReadonlyMap<string, SearchLiveCard> = new Map();

/** One session is retained by the parent while Search is off screen; never browser storage. */
export function createSearchBindings(context: ScreenContext) {
  const life = screenLifetime(context);
  const service = new SearchService({api: life.api, scope: context.scope});
  const results = createSearchRows(service, life.current);
  const genres = new SearchGenresController({api: life.api, signal: life.signal(), isCurrent: life.current, slot: 'backdrop', limit: 8});
  const onNow = new SearchOnNowController({api: life.api, serverId: context.scope.serverId,
    timezone: serviceI18n().region.timeZone ?? Intl.DateTimeFormat().resolvedOptions().timeZone,
    isCurrent: life.current, signal: life.signal(), limit: 4});
  let text = '', group: SearchGroup | undefined;
  let resultKey = '';
  let summary: Readonly<{key: string; groups: readonly Pick<SearchGroupResult, 'id' | 'status' | 'totalCount' | 'totalCountExact'>[]}> | undefined;
  const visible = new Set<SearchEntry>();
  const commandContext: ScreenContext = forwardIdentity(context, {effects: {...context.effects, async play(target: Parameters<ScreenContext['effects']['play']>[0], signal: AbortSignal, callerCurrent = () => true) {
    life.assert();signal.throwIfAborted();
    const captured = results.getSnapshot(), query = requestKey(captured.query), source = resultKey;
    const current = () => {
      const state = results.getSnapshot();
      return life.current() && callerCurrent() && state.phase === 'ready' && state.generation === captured.generation
        && requestKey(state.query) === query && resultKey === source;
    };
    if (results.getSnapshot().phase !== 'ready' || !query || !source || !current()) throw new DOMException('Search playback changed', 'AbortError');
    await context.effects.play(target, signal, current);
    if (!current()) throw new DOMException('Search playback changed', 'AbortError');
  }}});
  const topTitle = createDetailBindings(commandContext), topShow = createShowBindings(commandContext);
  /** Live TV results are decorated from their own matched guide rows, never from another
   * query's answer: every read is retired by the next query, a newer search answer, a guide
   * viewer-fence change, the screen lifetime, or disposal. */
  let cards: ReadonlyMap<string, SearchLiveCard> = NO_LIVE_CARDS, liveKey = '', liveGeneration = 0, liveController: AbortController | undefined;
  const liveListeners = new Set<() => void>();
  const publishLive = (next: ReadonlyMap<string, SearchLiveCard>) => { cards = next; for (const listener of [...liveListeners]) listener(); };
  const retireLive = () => { liveController?.abort(); liveController = undefined; liveGeneration++; };
  const syncLive = () => {
    const state = results.getSnapshot(), data = state.data;
    const result = data?.groups.find(value => value.id === 'live-tv');
    const entries = result?.status === 'success' ? result.items.filter((entry): entry is ChannelResult => entry.kind === 'channel') : [];
    const key = data && state.phase === 'ready' ? JSON.stringify([state.generation, requestKey(state.query), data.scope.viewerFence, data.revision, entries.map(entry => [entry.id, entry.navigation.sourceId ?? ''])]) : '';
    if (key === liveKey) return;
    liveKey = key; retireLive();
    if (!key || !entries.length || !data) { if (cards.size) publishLive(NO_LIVE_CARDS); return; }
    const bySource = new Map<string, string[]>();
    for (const entry of entries) {
      const sourceId = entry.navigation.sourceId ?? '';
      if (!sourceId) continue;
      bySource.set(sourceId, [...(bySource.get(sourceId) ?? []), entry.id]);
    }
    if (!bySource.size) { if (cards.size) publishLive(NO_LIVE_CARDS); return; }
    const generation = liveGeneration, controller = new AbortController(), signal = life.signal(controller.signal);
    liveController = controller;
    const still = () => life.current() && !signal.aborted && generation === liveGeneration && liveKey === key && results.getSnapshot().phase === 'ready';
    void (async () => {
      const next = new Map<string, SearchLiveCard>();
      const timezone = serviceI18n().region.timeZone;
      // A confirmed zone is required: no read and no card under a guessed UTC.
      if (!timezone) { publishLive(NO_LIVE_CARDS); return; }
      for (const [sourceId, ids] of bySource) {
        const route = {kind: 'live-source' as const, sourceId, timezone, search: '', ...guideWindow()};
        for (let start = 0; start < ids.length; start += 50) {
          const query = new URLSearchParams({...route, channels: ids.slice(start, start + 50).join(','), limit: '50'});
          const raw = await life.api.request<unknown>(routePath('get_guide') + '?' + query, 'GET', undefined, signal);
          if (!still()) return;
          const guide = parseChannelGuide(raw, context.scope.serverId, route);
          const now = Date.now();
          for (const channel of guide.channels) next.set(channel.id, liveCard(channel, now, timezone));
        }
      }
      if (!still()) return;
      publishLive(next);
    })().catch(() => { if (still()) publishLive(NO_LIVE_CARDS); });
  };
  const off = results.subscribe(() => {
    const state = results.getSnapshot(), data = state.data;
    try {
      if (!data) { visible.clear(); resultKey = ''; return; }
      const key = JSON.stringify([summaryKey(data), data.query.group, data.query.groups]);
      if (!data.query.group) summary = Object.freeze({key: summaryKey(data), groups: Object.freeze(state.groups.map(({id, status, totalCount, totalCountExact}) => Object.freeze({id, status, totalCount, totalCountExact})))});
      if (key !== resultKey || !state.pagination.cursor) visible.clear();
      resultKey = key;
      for (const result of state.groups) for (const entry of result.items) visible.add(entry);
    } finally { syncLive(); }
  });
  let selectionAttempt = 0, readingOffered = false;
  const select = async (q: string, options: Omit<SearchQuery, 'q'> = {}) => {
    life.assert(); const attempt = ++selectionAttempt; text = q; group = options.group;
    if (q.trim()) { genres.cancel(); onNow.cancel(); }
    const defaults = !options.group && !options.groups, includedReading = readingOffered;
    const groups = SEARCH_CHIPS.flatMap(chip => chip.id === 'all' || chip.id === 'reading' && !readingOffered ? [] : [chip.id]);
    const work = service.select(q, defaults ? {...options, groups} : options), generation = service.getSnapshot().generation;
    await work;
    const state = service.getSnapshot();
    if (!life.current() || attempt !== selectionAttempt || state.generation !== generation || state.phase !== 'ready' || state.data?.query.q !== q.trim()) return;
    readingOffered = state.data.capabilities.groups.some(value => value.id === 'reading' && value.available);
    // Older servers never receive an unknown default group. Once actually offered,
    // add Reading by a read-only repeat; an explicit history gesture is recorded once.
    if (defaults && readingOffered && !includedReading) await service.select(q, {...options, groups: SEARCH_CHIPS.flatMap(chip => chip.id === 'all' ? [] : [chip.id]), record: false});
  };
  const live = {
    getSnapshot: () => cards,
    subscribe: (listener: () => void) => { liveListeners.add(listener); return () => { liveListeners.delete(listener); }; },
  };
  return {
    context: commandContext, service, results, genres, onNow, select, topTitle, topShow, live,
    session: () => ({text, group}),
    summary: () => {
      const state = results.getSnapshot(), data = state.data;
      return life.current() && state.phase === 'ready' && data && summary?.key === summaryKey(data) ? summary.groups : [];
    },
    recent: (signal?: AbortSignal) => readSearchHistory(life.api, context.scope, life.signal(signal)),
    clearRecent: () => { life.assert(); return clearSearchHistory(life.api, life.signal()); },
    removeRecent: (query: string) => { life.assert(); return deleteSearchHistoryEntry(life.api, context.scope, query, life.signal()); },
    open(entry: SearchEntry) {
      life.assert();
      if (results.getSnapshot().phase !== 'ready' || !visible.has(entry)) throw new DOMException('Search result changed', 'AbortError');
      context.effects.openEntry(entry);
    },
    async play(entry: SearchEntry) {
      life.assert();
      if (results.getSnapshot().phase !== 'ready' || !visible.has(entry) || !('libraryId' in entry) || !entry.libraryId || !entry.playback || entry.available !== true) throw new DOMException('Search playback changed', 'AbortError');
      await commandContext.effects.play({libraryId: entry.libraryId, itemId: entry.playback.itemId, startSeconds: entry.playback.startSeconds ?? 0, display: entryStartDisplay(entry)}, life.signal(), () => visible.has(entry));
    },
    canPlayArtist(entry: SearchEntry) {
      return life.current() && results.getSnapshot().phase === 'ready' && visible.has(entry)
        && entry.kind === 'artist' && !!context.effects.playArtist;
    },
    async playArtist(entry: SearchEntry) {
      life.assert();
      const effect = context.effects.playArtist, captured = results.getSnapshot();
      const query = requestKey(captured.query), source = resultKey;
      const current = () => {
        const state = results.getSnapshot();
        return life.current() && state.phase === 'ready' && state.generation === captured.generation
          && requestKey(state.query) === query && resultKey === source && visible.has(entry);
      };
      if (!effect || entry.kind !== 'artist' || !query || !source || !current()) throw new DOMException('Search artist changed', 'AbortError');
      await effect(entry.id, life.signal(), current);
      if (!current()) throw new DOMException('Search artist changed', 'AbortError');
    },
    dispose() { text = ''; group = undefined; summary = undefined; resultKey = ''; visible.clear(); off(); results.dispose(); retireLive(); cards = NO_LIVE_CARDS; liveListeners.clear(); genres.dispose(); onNow.dispose(); life.dispose(); topTitle.dispose(); topShow.dispose(); service.cancel(); service.dispose(); },
  };
}
export type SearchBindings = ReturnType<typeof createSearchBindings>;
