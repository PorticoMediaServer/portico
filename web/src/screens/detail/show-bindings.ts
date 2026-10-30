import {ShowWorkspaceService, saveShowSettings, type ShowSettings, type ShowWorkspaceData, type ShowWorkspaceRoute, type WorkspaceShow} from '@core/show-workspace.ts';
import type {ContentEntry} from '@core/library-content.ts';
import {createDetailBindings} from './bindings';
import {screenLifetime, type ScreenContext} from './context';
import {getContainerState, setContainerFlag, setContainerState, type ContainerState} from '@core/container-state.ts';
import {createRecommendations} from './recommendations';
import {titleView} from './view-state';
import {openGenre} from './genre-binding';
import {browseCapabilitiesPath, parseBrowseCapabilities, validateBrowseQuery, type BrowseNode} from '@core/browse.ts';
import {preferenceValue} from '@core/preferences.ts';

export function createShowBindings(context: ScreenContext) {
  const life = screenLifetime(context), service = new ShowWorkspaceService({api: life.api, scope: context.scope});
  const episode = createDetailBindings(context), primary = createDetailBindings(context), listedPlayback = createDetailBindings(context);
  const recommendations = createRecommendations(context);
  const view = titleView('episodes', ['episodes', 'related', 'details'], life.assert);
  let episodeLayoutPreference: 'auto' | 'tiles' | 'list' | undefined;
  const updateEpisodeLayout = () => {
    if (!life.current()) return;
    const snapshot = context.preferences.getSnapshot();
    if (!snapshot.registry?.fields.some(field => field.key === 'appearance.episodesLayout')) return;
    const value = preferenceValue<'auto' | 'tiles' | 'list'>(snapshot, 'appearance.episodesLayout');
    if (value !== 'auto' && value !== 'tiles' && value !== 'list') return;
    if (value !== episodeLayoutPreference) { episodeLayoutPreference = value; view.defaultLayout(value === 'list' ? 'list' : 'grid'); }
  };
  updateEpisodeLayout();
  const offPreferences = context.preferences.subscribe(updateEpisodeLayout);
  let viewIdentity = '';
  let subject: Readonly<{libraryId: string; showId: string; title: string}> | undefined;
  const offSubject = service.subscribe(() => { const data = service.getSnapshot().data; if (data && service.getSnapshot().phase === 'ready') subject = {libraryId: data.scope.libraryId, showId: data.show.id, title: data.show.title}; });
  const seasonReads = new WeakMap<ContainerState, Readonly<{data: ShowWorkspaceData; seasonId: string}>>();
  let personal: ContainerState | undefined, busy = false, intent = 0, disposed = false;
  let selection: AbortController | undefined;
  let playbackRequest: AbortController | undefined;
  const listeners = new Set<() => void>(), publish = () => { for (const listener of listeners) listener(); };
  const current = (data: ShowWorkspaceData) => {
    life.assert();
    if (service.getSnapshot().phase !== 'ready' || service.getSnapshot().data !== data) throw new DOMException('Show revision changed', 'AbortError');
  };
  return {
    context, service, episode, primary, current, recommendations, view,
    async browseFacet(data: ShowWorkspaceData, field: 'network' | 'studio', value: string) {
      current(data);
      const actual = field === 'network' ? data.show.network : data.show.studio;
      if (actual !== value) throw new DOMException('Show fact changed', 'AbortError');
      if (!context.effects.openBrowse) throw new DOMException('Browse destination unavailable', 'AbortError');
      const active = life.signal(), capabilities = parseBrowseCapabilities(await life.api.request(browseCapabilitiesPath(data.scope.libraryId, ''), 'GET', undefined, active));
      current(data); active.throwIfAborted();
      if (capabilities.library.id !== data.scope.libraryId) throw new DOMException('Browse vocabulary changed', 'AbortError');
      const query: BrowseNode = {variant: 'predicate', field, operator: 'equals', value};
      validateBrowseQuery(capabilities, query);
      context.effects.openBrowse({libraryId: data.scope.libraryId, query});
    },
    browseGenre(data: ShowWorkspaceData, genre: string) {
      current(data);
      if (!data.show.genres?.includes(genre)) throw new DOMException('Show genre changed', 'AbortError');
      return openGenre(context, life.api, data.scope.libraryId, genre, () => current(data), life.signal());
    },
    subject() { const route = service.getSnapshot().route; return life.current() && route?.showId === subject?.showId && route?.libraryId === subject?.libraryId ? subject?.title : undefined; },
    personal: {getSnapshot: () => personal, getBusy: () => busy, subscribe: (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; }},
    async select(route: ShowWorkspaceRoute, signal?: AbortSignal) {
      life.assert(); signal?.throwIfAborted(); const generation = ++intent;
      playbackRequest?.abort(); listedPlayback.service.cancel();
      selection?.abort(); const controller = new AbortController(); selection = controller;
      const active = life.signal(signal ? AbortSignal.any([controller.signal, signal]) : controller.signal);
      const cancel = () => { if (generation === intent) { service.cancel(); episode.service.cancel(); primary.service.cancel(); } };
      active.addEventListener('abort', cancel, {once: true});
      recommendations.clear(); personal = undefined; publish(); episode.service.cancel(); primary.service.cancel();
      try {
        await service.select(route); life.assert(); active.throwIfAborted(); if (generation !== intent) return;
        const data = service.getSnapshot().data;
        if (data) { const identity = JSON.stringify([data.scope.libraryId, data.show.id]); if (identity !== viewIdentity) { viewIdentity = identity; view.reset(); } }
        if (data) { const value = await getContainerState(life.api, 'show', data.show.id, active); if (generation !== intent) return; current(data); personal = value; publish(); }
        if (data?.nextUp?.libraryId && data.nextUp.playback) await primary.select({libraryId: data.nextUp.libraryId, itemId: data.nextUp.playback.itemId}, active);
      } finally { active.removeEventListener('abort', cancel); }
    },
    async expand(data: ShowWorkspaceData, entry: ContentEntry) {
      current(data);
      if (!data.episodes.sections.some(section => section.entries.includes(entry))) throw new DOMException('Episode changed', 'AbortError');
      const anchor = service.getSnapshot().anchor;
      if (anchor.expandedEpisodeId === entry.id) { service.updateAnchor({...anchor, expandedEpisodeId: null}); episode.service.cancel(); return; }
      service.updateAnchor({...anchor, expandedEpisodeId: entry.id, focusedEpisodeId: entry.id});
      await episode.select({libraryId: data.scope.libraryId, itemId: entry.id});
    },
    async playEpisode(data: ShowWorkspaceData, entry: ContentEntry, signal?: AbortSignal) {
      current(data); signal?.throwIfAborted();
      if (!data.episodes.sections.some(section => section.entries.includes(entry)) || entry.available !== true || entry.playback?.itemId !== entry.id) throw new DOMException('Episode playback changed', 'AbortError');
      playbackRequest?.abort(); const controller = new AbortController(); playbackRequest = controller;
      const active = life.signal(signal ? AbortSignal.any([controller.signal, signal]) : controller.signal);
      try {
        // A tile's old offer does not authorize a later start. Read the episode's
        // current actions without changing the episode panel or its expansion.
        await listedPlayback.select({libraryId: data.scope.libraryId, itemId: entry.id}, active);
        current(data); active.throwIfAborted();
        const fresh = listedPlayback.service.getSnapshot().data;
        const action = fresh?.actions.find(value => value.enabled && value.playback && value.id !== 'start_over');
        if (!fresh || !action) throw new DOMException('Episode playback unavailable', 'AbortError');
        await listedPlayback.play(fresh, action.id, undefined, active);
      } finally { if (playbackRequest === controller) playbackRequest = undefined; }
    },
    async readSeasonState(data: ShowWorkspaceData, signal?: AbortSignal) {
      current(data); const seasonId = data.selected.seasonId;
      if (!seasonId) throw new DOMException('Season selection changed', 'AbortError');
      const state = await getContainerState(life.api, 'season', seasonId, life.signal(signal));
      current(data); signal?.throwIfAborted();
      if (data.selected.seasonId !== seasonId || state.libraryId !== data.scope.libraryId) throw new DOMException('Season selection changed', 'AbortError');
      seasonReads.set(state, {data, seasonId}); return state;
    },
    async setSeasonWatched(data: ShowWorkspaceData, before: ContainerState, value: boolean) {
      current(data); const held = seasonReads.get(before);
      if (!held || held.data !== data || held.seasonId !== data.selected.seasonId || busy) throw new DOMException('Season state changed', 'AbortError');
      busy = true; publish();
      try {
        await setContainerState(life.api, 'season', held.seasonId, {expectedRevision: before.revision, watched: value}, life.signal());
        current(data); seasonReads.delete(before); await service.refresh();
      } finally { busy = false; publish(); }
    },
    async playSeason(data: ShowWorkspaceData, signal?: AbortSignal) {
      current(data); const seasonId = data.selected.seasonId, effect = context.effects.playSeason;
      if (!seasonId || !effect) throw new DOMException('Season playback unavailable', 'AbortError');
      const active = life.signal(signal);
      const stillSelected = () => life.current() && service.getSnapshot().phase === 'ready' && service.getSnapshot().data === data && service.getSnapshot().data?.selected.seasonId === seasonId;
      await effect(seasonId, active, stillSelected);
      current(data); active.throwIfAborted();
    },
    closeEpisode() { life.assert(); service.updateAnchor({...service.getSnapshot().anchor, expandedEpisodeId: null}); episode.service.cancel(); },
    async revalidate(refresh?: Parameters<ShowWorkspaceService['revalidate']>[0]) {
      life.assert(); await service.revalidate(refresh);
      const data = service.getSnapshot().data; if (!data) return;
      const value = await getContainerState(life.api, 'show', data.show.id, life.signal(refresh?.signal)); current(data); personal = value; publish();
      const nextUp = data.nextUp;
      if (nextUp?.libraryId && nextUp.playback && primary.service.getSnapshot().target?.itemId !== nextUp.playback.itemId) await primary.select({libraryId: nextUp.libraryId, itemId: nextUp.playback.itemId}, refresh?.signal);
      else await primary.service.revalidate(refresh);
    },
    async setMyList(data: ShowWorkspaceData, value: boolean) {
      current(data); const before = personal; if (!before || busy) return; busy = true; publish();
      try { const result = await setContainerFlag(life.api, 'show', data.show.id, 'watchlisted', value, life.signal()); current(data); if (before === personal) { personal = result; publish(); } }
      finally { busy = false; publish(); }
    },
    async setFavorite(data: ShowWorkspaceData, value: boolean) {
      current(data); const before = personal; if (!before || busy) return; busy = true; publish();
      try { const result = await setContainerFlag(life.api, 'show', data.show.id, 'favorite', value, life.signal()); current(data); if (before === personal) { personal = result; publish(); } }
      finally { busy = false; publish(); }
    },
    async setWatched(data: ShowWorkspaceData, value: boolean) {
      current(data); const before = personal; if (!before || busy) return; busy = true; publish();
      try { const result = await setContainerState(life.api, 'show', data.show.id, {expectedRevision: before.revision, watched: value}, life.signal()); current(data); if (before === personal) { personal = result; publish(); } await service.refresh(); }
      catch (error) {
        if (life.current() && (error as {status?: number})?.status === 412) { const latest = await getContainerState(life.api, 'show', data.show.id, life.signal()); current(data); personal = latest; publish(); }
        throw error;
      }
      finally { busy = false; publish(); }
    },
    canManage() { return life.current() && context.identity?.primaryProfile === true && context.identity.capabilities?.manageServer === true; },
    async setSettings(data: ShowWorkspaceData, settings: ShowSettings, signal?: AbortSignal) {
      current(data);
      if (context.identity?.primaryProfile !== true || context.identity.capabilities?.manageServer !== true || settings.revision !== data.settings.revision) throw new DOMException('Show management changed', 'AbortError');
      const api = {request: <T,>(path: string, method?: string, body?: unknown) => life.api.request<T>(path, method, body, signal)};
      // The canonical endpoint authorizes this command inside the revision-checked transaction.
      await saveShowSettings(api, data.scope.libraryId, data.show.id, settings);
      current(data); signal?.throwIfAborted(); await service.refresh();
    },
    cancel() { if (disposed) return; intent++; selection?.abort(); playbackRequest?.abort(); listedPlayback.service.cancel(); service.cancel(); episode.service.cancel(); primary.service.cancel(); recommendations.clear(); personal = undefined; publish(); },
    dispose() { if (disposed) return; disposed = true; intent++; selection?.abort(); playbackRequest?.abort(); subject = undefined; offSubject(); offPreferences(); episodeLayoutPreference = undefined; viewIdentity = ''; life.dispose(); view.dispose(); recommendations.dispose(); personal = undefined; busy = false; publish(); listeners.clear(); listedPlayback.dispose(); episode.dispose(); primary.dispose(); service.cancel(); service.dispose(); },
  };
}
export type ShowBindings = ReturnType<typeof createShowBindings>;

/**
 * The drawn Details facts of one show, taken only from the workspace producer's stored
 * show record. File facts never appear here (ruling: no file details on any page); the
 * season/episode totals are the visible counts the page already shows.
 */
export function showDetailFacts(show: WorkspaceShow, seasonTotalCount: number, language: string) {
  const displayLanguage = (code?: string) => {
    if (!code) return undefined;
    try { return new Intl.DisplayNames(language, {type: 'language'}).of(code) ?? code; } catch { return code; }
  };
  const creators = [...new Map((show.credits ?? [])
    .filter(credit => /creator|created by|showrunner/i.test(`${credit.department ?? ''} ${credit.role ?? ''}`))
    .map(credit => [credit.name.toLocaleLowerCase(language), credit])).values()];
  return Object.freeze({
    firstAired: show.firstAired,
    status: show.status,
    network: show.network,
    studio: show.studio,
    country: show.country,
    genres: show.genres ?? Object.freeze([]),
    creators: Object.freeze(creators),
    rating: show.contentRating,
    language: displayLanguage(show.originalLanguage),
    onServer: seasonTotalCount > 0 ? Object.freeze({seasons: seasonTotalCount, episodes: show.episodeCount}) : undefined,
    addedAt: show.addedAt,
  });
}
