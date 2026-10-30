import {subscribePresentation} from '../../shell/fact-hooks';
import {shellLanguage} from '../../shell/settings';
import {GroupSessionService, type GroupSessionSnapshot} from '@core/group-session.ts';
import {encodedRoutePath} from '../../../../packages/contracts/src/routes.ts';
import type {ItemResponse} from '@core/media-item.ts';
import {parseMediaItem} from '@core/media-item.ts';
import {subscribeFreshness} from '@core/freshness.ts';
import type {ShellAssemblyContext} from '../../shell/bootstrap';
import type {WatchBindings} from '../watch';
import {router} from '../../router';
import {TogetherItemSummaries} from './item-summaries';

export type TogetherSnapshot = Readonly<{room: GroupSessionSnapshot; items: ReadonlyMap<string, ItemResponse>}>;
/** Inert construction. The persistent Watch parent starts/disposes it for one captured session. */
export function createTogetherBindings(context: ShellAssemblyContext, playback: WatchBindings) {
  const captured = context.session(), life = new AbortController();
  let i18n = shellLanguage(context.preferences?.getSnapshot('web') ?? context.facts.preferences);
  const isCurrent = () => context.isCurrent() && !life.signal.aborted && !playback.runtime.getSnapshot().disposed
    && context.session().sessionFamilyId === captured.sessionFamilyId
    && (['serverId', 'authority', 'accountId', 'profileId', 'role'] as const).every(key => context.session().viewer[key] === captured.viewer[key]);
  const guard = () => {if (!isCurrent()) throw new DOMException('Room authority retired', 'AbortError');};
  const api = {baseUrl: context.api.baseUrl, async request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> {
    guard();const active = signal ? AbortSignal.any([signal, life.signal]) : life.signal;
    const value = await context.api.request<T>(path, method, body, active);guard();active.throwIfAborted();return value;
  }};
  const service = new GroupSessionService({api, i18n, stream: async (path, lastEventId, signal) => {
    guard();const url = new URL(path, context.api.baseUrl);
    if (!path.startsWith('/v1/groups/') || url.origin !== new URL(context.api.baseUrl).origin) throw new Error('invalid_group_stream');
    const value = await context.api.routeFetch(url.href, {signal: AbortSignal.any([signal, life.signal]), headers: {Accept: 'text/event-stream', Authorization: `Bearer ${context.session().accessToken}`,
      ...(lastEventId ? {'Last-Event-ID': lastEventId} : {})}});
    guard();return value;
  }});
  const listeners = new Set<() => void>();
  let snapshot: TogetherSnapshot = {room: service.getSnapshot(), items: new Map()}, key = '', offGroup: (() => void) | undefined, offPreferences: (() => void) | undefined, offFreshness: (() => void) | undefined, started = false;
  const publish = (items = snapshot.items) => {snapshot = Object.freeze({room: service.getSnapshot(), items});for (const listener of [...listeners]) listener();};
  const catalogueRevisions = new Map<string, string>();
  const summaries = new TogetherItemSummaries<ItemResponse>({current: isCurrent, publish, reportError: playback.reportError,
    read: async (id, signal) => {
      const raw = await api.request<ItemResponse>(encodedRoutePath('get_items_id', {id: encodeURIComponent(id)}), 'GET', undefined, signal);
      if (parseMediaItem(raw).id !== id) throw new Error('unexpected_group_item');
      return raw;
    },
  });
  const off = service.subscribe(() => {
    const room = service.getSnapshot(), group = room.group, queue = room.queue ?? group?.queue;
    const next = isCurrent() && group && (room.phase === 'live' || room.phase === 'reconnecting') ? JSON.stringify([group.id, queue?.revision ?? group.queueRevision, group.timeline.currentEntryId, group.timeline.itemId, queue?.eligibility?.currentEntryBlocked,
      queue?.entries.map(entry => [entry.entryId, entry.itemId, entry.unavailable])]) : '';
    if (next === key) {publish();return;}key = next;
    // Scope/pruning publishes the room and its metadata together. Never notify a
    // new room with the previous room's item map, even for one synchronous turn.
    if (!next || !group || !isCurrent()) {summaries.update('', []);return;}
    const currentEntry = queue?.entries.find(entry => entry.entryId === group.timeline.currentEntryId);
    const currentId = !currentEntry?.unavailable && !queue?.eligibility?.currentEntryBlocked ? group.timeline.itemId : null;
    const ids = [...new Set([currentId, ...(queue?.entries.filter(entry => !entry.unavailable).map(entry => entry.itemId) ?? [])].filter((id): id is string => !!id))];
    summaries.update(group.id, ids);
  });
  return {
    service, runtime: playback.runtime, environment: playback.environment, artwork: playback.artwork, get t() {return i18n.t;}, get number() {return i18n.number;},
    get profileName() {return context.facts.identity.profileName;}, get profileArt() {return context.facts.identity.profileArt;},
    openSettings: () => {guard();void router.navigate({to: '/settings'});},
    isCurrent, getSnapshot: () => snapshot, subscribe: (listener: () => void) => {listeners.add(listener);return () => {listeners.delete(listener);};},
    start: () => {guard();if (started) return;started = true;offGroup = playback.runtime.bindGroup(service, isCurrent, playback.reportError);
      offFreshness = subscribeFreshness(context.events, change => {
        if (!isCurrent()) return;
        if (change.type === 'libraries.changed') {catalogueRevisions.clear();summaries.invalidate(() => true);}
        else if (change.type === 'library.changed') {
          const revision = change.counters.content ?? change.revision;
          if (revision !== undefined && catalogueRevisions.get(change.resourceId) === revision) return;
          if (revision !== undefined) catalogueRevisions.set(change.resourceId, revision);
          summaries.invalidate(value => value.libraryId === change.resourceId);
        }
      });
      offPreferences = subscribePresentation(context, () => {if (isCurrent()) {i18n = shellLanguage(context.preferences?.getSnapshot('web') ?? context.facts.preferences);service.setI18n(i18n);publish();}});
    },
    reportError: playback.reportError,
    dispose: () => {if (life.signal.aborted) return;life.abort();offFreshness?.();summaries.dispose();offGroup?.();offPreferences?.();off();service.dispose();snapshot = {room: service.getSnapshot(), items: new Map()};for (const listener of [...listeners]) listener();listeners.clear();},
  };
}
export type TogetherBindings = ReturnType<typeof createTogetherBindings>;
