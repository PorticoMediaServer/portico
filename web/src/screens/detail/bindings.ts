import {FileInfoSession} from '@core/file-info-session.ts';
import {DetailService, parseCreditPage, type DetailProjection, type DetailTarget, type PersonalIntent} from '@core/detail.ts';
import type {FileInfoFileRow} from '@core/file-info-pages.ts';
import {readListeningSimilar} from '@core/library-content.ts';
import type {ListeningViewerScope} from '@core/listening.ts';
import {PlaybackOptionsClient} from '@core/playback-v1/options.ts';
import {localV1Http} from '@core/playback-v1/local-http.ts';
import {encodedRoutePath} from '../../../../packages/contracts/src/routes.ts';
import {readTitleIdentity} from '../../platform/title-identity';
import {screenLifetime, type ScreenContext} from './context';
import {createRecommendations} from './recommendations';
import {versionDeletionService, deletionAllowed, type VersionDeletePreview} from '@core/administration.ts';
import {separateFileInfoOccurrence, canMergeTitles, mergeTitles, SPLITTABLE_KINDS} from '@core/title-identity.ts';
import {validateContentEntry, type ContentEntry} from '@core/library-content.ts';
import {titleView} from './view-state';
import {openGenre} from './genre-binding';
import type {PlaybackOptions} from '@core/playback-v1/types.ts';
import type {RefObject} from 'react';

/** A caller that already knows the real item can suppress movie-only embedded rows. */
export type WebDetailTarget = Readonly<{itemId: string; libraryId?: string; kind?: string}>;

/** Constructor is inert. Only select/openFileInfo/readVersions perform reads. */
export function createDetailBindings(context: ScreenContext) {
  const life = screenLifetime(context);
  const service = new DetailService({api: life.api, scope: context.scope, requestId: async () => { life.assert(); return crypto.randomUUID(); }});
  const options = new PlaybackOptionsClient(localV1Http(life.api));
  const versionReads = new WeakMap<PlaybackOptions, DetailProjection>();
  const recommendations = createRecommendations(context);
  const view = titleView('related', ['related', 'details', 'extras'], life.assert);
  let viewIdentity = '';
  let selected: DetailTarget | undefined, intent = 0, disposed = false;
  let related: 'all' | 'none' = 'all', embeddedFallback: DetailProjection | undefined;
  let subject: Readonly<{itemId: string; libraryId: string; title: string}> | undefined;
  const offSubject = service.subscribe(() => { const data = service.getSnapshot().data; if (data && service.getSnapshot().phase === 'ready') subject = {itemId: data.item.id, libraryId: data.scope.libraryId, title: data.item.title}; });
  /** Unmount cleanup may follow the route's dispose; it must never revive a retired read. */
  const cancel = () => { if (!disposed) {intent++; selected = undefined; recommendations.clear(); service.cancel();} };
  const previews = new WeakMap<VersionDeletePreview, Readonly<{data: DetailProjection; proof: Readonly<{opening: string; occurrence: string}>}>>();
  const current = (data: DetailProjection) => {
    life.assert();
    if (service.getSnapshot().phase !== 'ready' || service.getSnapshot().data !== data) throw new DOMException('Title revision changed', 'AbortError');
  };
  const select = async (target: WebDetailTarget, signal?: AbortSignal) => {
    life.assert(); const generation = ++intent;
    signal?.throwIfAborted();
    service.cancel(); recommendations.clear(); selected = undefined; embeddedFallback = undefined;
    // The title's own detail route supplies library identity for a bare /item/$itemId route; it
    // serves Reading titles too, which the timed-media item read does not (E-web-23).
    const item = target.libraryId !== undefined ? undefined : await readTitleIdentity(life.api, target.itemId, signal);
    if (item && item.id !== target.itemId) throw new Error('Title target changed');
    const libraryId = target.libraryId ?? item!.libraryId, kind = item?.kind ?? target.kind;
    life.assert(); if (generation !== intent) return;
    signal?.throwIfAborted();
    const identity = JSON.stringify([libraryId, target.itemId]);
    if (identity !== viewIdentity) { viewIdentity = identity; view.reset(); }
    selected = {libraryId, itemId: target.itemId};
    related = kind === 'movie' ? 'none' : 'all';
    const cancel = () => { if (generation === intent) service.cancel(); }; signal?.addEventListener('abort', cancel, {once: true});
    try {
      await service.select(selected, {related}); signal?.throwIfAborted();
      const data = service.getSnapshot().data;
      // A hint is an optimization, never permission to drop a nonmovie's rows
      // when the actual detail disagrees with the caller's captured kind.
      if (generation === intent && data && related === 'none' && data.item.kind !== 'movie') {
        related = 'all';await service.select(selected, {related});signal?.throwIfAborted();
      }
    } finally { signal?.removeEventListener('abort', cancel); }
  };
  const readRecommendations = async (data: DetailProjection) => {
    const generation = intent;
    const isCurrent = () => !disposed && generation === intent && life.current() && service.getSnapshot().phase === 'ready' && service.getSnapshot().data === data;
    if (!isCurrent() || data.item.kind !== 'movie' || data === embeddedFallback) return;
    await recommendations.select(data, isCurrent);
    const needsFallback = () => isCurrent() && recommendations.getSnapshot().phase === 'error' && related === 'none';
    if (!needsFallback() || !selected) return;
    // Reselecting retires the service's command queue. Wait for explicit command
    // settlement instead; a replacement projection gets its own recommendation read.
    if (service.getSnapshot().pending.length) await new Promise<void>(resolve => {
      const signal = life.signal();
      let off = () => {};
      const done = () => {off();signal.removeEventListener('abort', done);resolve();};
      const changed = () => {if (!isCurrent() || !service.getSnapshot().pending.length) done();};
      off = service.subscribe(changed);signal.addEventListener('abort', done, {once: true});changed();
    });
    if (!needsFallback() || !selected) return;
    // Only the failed full read pays for the embedded computation. Retain its
    // original target and authority; the fallback projection must not start a loop.
    related = 'all';
    await service.select(selected, {related});
    if (!disposed && generation === intent && life.current()) embeddedFallback = service.getSnapshot().data ?? undefined;
  };
  /** One File info opening, made from the accepted Detail the page shows. It never adopts a
   * newer Detail: a later one is a new opening. Retire it with its panel. */
  const openFileInfo = (data: DetailProjection, signal?: AbortSignal) => {
    current(data);
    // The Detail carries its opening only once the title is ready.
    if (!data.fileInfoOpening) return undefined;
    return new FileInfoSession({api: life.api, opening: {token: data.fileInfoOpening, scope: context.scope, target: data.scope, revision: data.revision},
      isCurrent: () => life.current() && service.getSnapshot().data === data, signal: life.signal(signal)});
  };
  /** A File info command names the exact occurrence of the opening it was read from. The page
   * must still show that same accepted Detail; the server rechecks everything else live and
   * refuses a stale opening (409) before any change. */
  const fileCommand = (data: DetailProjection, opening: string, signal?: AbortSignal) => {
    current(data); signal?.throwIfAborted();
    if (data.fileInfoOpening !== opening) throw new DOMException('File info opening changed', 'AbortError');
  };
  const commandApi = (signal?: AbortSignal) => ({request: <T,>(path: string, method?: string, body?: unknown) => life.api.request<T>(path, method, body, signal)});
  const resolveEntry = async (itemId: string, signal?: AbortSignal) => {
    const item = await readTitleIdentity(life.api, itemId, signal);
    return validateContentEntry({id: item.id, libraryId: item.libraryId, kind: item.kind, title: item.title, posterUrl: item.posterUrl, backdropUrl: item.backdropUrl, navigation: {view: 'item', entityId: item.id}});
  };
  /** music-plus f7 (MC-9): the album's own More like this, ranked by sound when the server has
   * analyzed it and by today's rule otherwise. The same captured page, api and revisions. */
  const similar = async (data: DetailProjection, signal?: AbortSignal) => {
    current(data);
    const viewer = context.identity?.viewer;
    if (!viewer || (viewer.authority !== 'local' && viewer.authority !== 'hosted')) throw new DOMException('Title authority changed', 'AbortError');
    const scope: ListeningViewerScope = {serverId: context.scope.serverId, authority: viewer.authority, accountId: viewer.accountId, profileId: viewer.profileId};
    const value = await readListeningSimilar(life.api, scope, data.item.id, {limit: 12, by: 'sound', signal: life.signal(signal)});
    current(data);
    if (value.itemId !== data.item.id || value.libraryId !== data.item.libraryId || value.revision.catalog !== data.revision.catalog || value.revision.viewer !== data.revision.viewer) throw new DOMException('Title recommendations changed', 'AbortError');
    // The scope continues the row (SimilarTitleRow).
    return {value, scope};
  };
  const versions = async (data: DetailProjection, signal?: AbortSignal) => {
    current(data); const value = await options.options(data.item.id, {}, life.signal(signal)); current(data);
    if (value.itemId !== data.item.id) throw new Error('Playback options target mismatch');
    versionReads.set(value, data);
    return value;
  };
  const binding = {
    context, service, select, current, recommendations, readRecommendations, view,
    subject() { const target = service.getSnapshot().target; return life.current() && target?.itemId === subject?.itemId && target?.libraryId === subject?.libraryId ? subject?.title : undefined; },
    openFileInfo, readVersions: versions, readSimilar: similar,
    async chooseVersion(data: DetailProjection, captured: PlaybackOptions, signal: AbortSignal, returnFocusRef?: RefObject<HTMLElement | null>): Promise<void> {
      current(data);signal.throwIfAborted();
      const action = data.actions.find(value => value.enabled && value.playback?.itemId === data.item.id && value.id !== 'start_over');
      if (!context.effects.chooseVersion || !action || versionReads.get(captured) !== data || captured.itemId !== data.item.id || captured.versions.length < 2) throw new DOMException('Version choice unavailable', 'AbortError');
      const isCurrent = () => {try {current(data);return !signal.aborted;} catch {return false;}};
      await context.effects.chooseVersion({subject: {libraryId: data.scope.libraryId, itemId: data.item.id, title: data.item.title}, options: captured,
        signal: life.signal(signal), isCurrent, returnFocusRef,
        playVersion: (versionId, active) => binding.play(data, action.id, versionId, active)});
    },
    browseGenre(data: DetailProjection, genre: string) {
      current(data);
      if (!data.metadata.genres.some(value => value.name === genre)) throw new DOMException('Title genre changed', 'AbortError');
      return openGenre(context, life.api, data.scope.libraryId, genre, () => current(data), life.signal());
    },
    async previewDelete(data: DetailProjection, opening: string, file: FileInfoFileRow, signal?: AbortSignal) {
      fileCommand(data, opening, signal);
      const proof = {opening, occurrence: file.occurrence};
      const preview = await versionDeletionService(commandApi(signal).request).preview(file.itemId, file.id, proof);
      current(data); signal?.throwIfAborted();
      if (preview.assetId !== file.id || preview.targets.length !== 1 || preview.targets[0]?.itemId !== file.itemId) throw new Error('Delete preview target mismatch');
      previews.set(preview, {data, proof}); return preview;
    },
    async deleteVersion(data: DetailProjection, preview: VersionDeletePreview, confirmation: string, operationId: string, signal?: AbortSignal) {
      const held = previews.get(preview);
      if (held?.data !== data || !deletionAllowed(preview) || confirmation !== preview.confirmation) throw new DOMException('Delete preview changed', 'AbortError');
      fileCommand(data, held.proof.opening, signal);
      const result = await versionDeletionService(commandApi(signal).request).remove(preview, confirmation, operationId, held.proof);
      current(data); signal?.throwIfAborted(); return result;
    },
    /** "Not this title": the server resolves the exact occurrence and separates that one file. */
    async splitVersion(data: DetailProjection, opening: string, occurrence: string, signal?: AbortSignal) {
      fileCommand(data, opening, signal);
      if (!SPLITTABLE_KINDS.includes(data.item.kind)) throw new Error('Title cannot be split');
      const itemId = await separateFileInfoOccurrence(commandApi(signal), data.item.id, {opening, occurrence});
      current(data); signal?.throwIfAborted(); return resolveEntry(itemId, signal);
    },
    /** `sourceOccurrence`: the open title's one listed file, when this title is the one merged away. */
    async mergeTitle(data: DetailProjection, opening: string, candidate: ContentEntry, keepId: string, signal?: AbortSignal, sourceOccurrence?: string) {
      fileCommand(data, opening, signal);
      if (!canMergeTitles(data.item, candidate) || ![data.item.id, candidate.id].includes(keepId) || candidate.libraryId !== data.scope.libraryId) throw new Error('Merge target changed');
      const fresh = await resolveEntry(candidate.id, signal); current(data);
      if (!canMergeTitles(data.item, fresh) || fresh.libraryId !== data.scope.libraryId) throw new Error('Merge target changed');
      const fromId = keepId === data.item.id ? candidate.id : data.item.id;
      // The proof names the exact source file of this opening, so it only goes with a merge of this title away.
      const proof = fromId === data.item.id && sourceOccurrence ? {opening, occurrence: sourceOccurrence} : undefined;
      const itemId = await mergeTitles(commandApi(signal), fromId, keepId, proof);
      current(data); signal?.throwIfAborted(); return resolveEntry(itemId, signal);
    },
    async readCredits(data: DetailProjection, group: 'cast' | 'crew', cursor?: string, signal?: AbortSignal) {
      current(data);
      const query = new URLSearchParams({group, limit: '40', ...(cursor ? {cursor} : {})});
      const path = encodedRoutePath('get_items_id_credits', {id: encodeURIComponent(data.item.id)});
      const page = parseCreditPage(await life.api.request(path + '?' + query, 'GET', undefined, signal), data.item.id, group);
      current(data);
      if (page.revision.catalog !== data.revision.catalog || page.revision.viewer !== data.revision.viewer) throw new DOMException('Credits revision changed', 'AbortError');
      return page;
    },
    mutate: async (data: DetailProjection, action: PersonalIntent) => { current(data); await service.mutate(action); },
    play: async (data: DetailProjection, actionId: string, versionId?: string, signal?: AbortSignal) => {
      current(data); signal?.throwIfAborted();
      const action = data.actions.find(value => value.id === actionId && value.enabled && value.playback);
      if (!action?.playback || action.playback.itemId !== data.item.id) throw new Error('Title playback action unavailable');
      let startSeconds = action.playback.startSeconds;
      if (versionId) {
        const offered = await versions(data, signal);
        if (!offered.versions.some(value => value.id === versionId)) throw new Error('Version no longer available');
        const positionMs = offered.resume?.positionMs ?? 0;
        if (!Number.isFinite(positionMs) || positionMs < 0) throw new Error('Unreadable title resume position');
        startSeconds = positionMs / 1000;
      }
      current(data);
      await context.effects.play({...action.playback, startSeconds, libraryId: data.scope.libraryId, ...(versionId ? {versionId} : {})}, life.signal(signal),
        () => life.current() && service.getSnapshot().phase === 'ready' && service.getSnapshot().data === data);
    },
    dispose() { if (disposed) return; disposed = true; intent++; selected = undefined; subject = undefined; offSubject(); viewIdentity = ''; life.dispose(); view.dispose(); recommendations.dispose(); service.cancel(); service.dispose(); },
  };
  return {...binding, cancel};
}
export type DetailBindings = ReturnType<typeof createDetailBindings>;
