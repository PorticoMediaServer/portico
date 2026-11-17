import {subscribePresentation} from '../../shell/fact-hooks';
import {shellLanguage} from '../../shell/settings';
import {withGuardedRaw} from '@core/home-selection/list-destinations.ts';
import {RoomSessionService, type RoomSessionSnapshot} from '@core/room-session.ts';
import {readTogetherItem} from '@core/query/receiver.ts';
import type {ItemResponse} from '@core/media-item.ts';
import {subscribeFreshness} from '@core/freshness.ts';
import type {ShellAssemblyContext} from '../../shell/bootstrap';
import type {WatchBindings} from '../watch';
import {router} from '../../router';
import {TogetherItemSummaries} from './item-summaries';

export type TogetherSnapshot = Readonly<{room: RoomSessionSnapshot; items: ReadonlyMap<string, ItemResponse>}>;
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
  const service = new RoomSessionService({api: withGuardedRaw(api, context.api, guard), i18n, events: context.events});
  const listeners = new Set<() => void>();
  let snapshot: TogetherSnapshot = {room: service.getSnapshot(), items: new Map()}, key = '', offGroup: (() => void) | undefined, offPreferences: (() => void) | undefined, offFreshness: (() => void) | undefined, started = false;
  const publish = (items = snapshot.items) => {snapshot = Object.freeze({room: service.getSnapshot(), items});for (const listener of [...listeners]) listener();};
  const catalogueRevisions = new Map<string, string>();
  const summaries = new TogetherItemSummaries<ItemResponse>({current: isCurrent, publish, reportError: playback.reportError,
    read: (id, signal) => readTogetherItem(api, id, signal),
  });
  const off = service.subscribe(() => {
    const room = service.getSnapshot(), group = room.group;
    const next = isCurrent() && group && (room.phase === 'live' || room.phase === 'reconnecting') ? JSON.stringify([group.id, group.clock.entryId,
      group.queue.map(entry => [entry.entryId, entry.itemId, entry.available])]) : '';
    if (next === key) {publish();return;}key = next;
    // Publish new room metadata together, never the previous room's titles.
    if (!next || !group || !isCurrent()) {summaries.update('', []);return;}
    const ids = [...new Set(group.queue.filter(entry => entry.available && entry.itemId).map(entry => entry.itemId!))];
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
