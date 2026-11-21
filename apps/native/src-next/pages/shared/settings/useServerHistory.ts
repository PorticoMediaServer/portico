import {useEffect, useLayoutEffect, useState, useSyncExternalStore} from 'react';
import {playHistoryPlatform, playHistoryTitle, type PlayHistoryEntry} from '../../../../../../packages/client-core/src/play-history';
import {readServerAccounts, readServerPlayHistory, serverPlayHistoryQuery, type ServerPlayFilters} from '../../../../../../packages/client-core/src/admin/server-play-history';
import {repeat} from '../../../../../../packages/client-core/src/timer';
import {readDocument} from '../../../../../../packages/client-core/src/query/admin-documents';
import {viewerQueriesFor} from '../../../shell/freshness';
import {captureReportContext} from '../../../../../../packages/client-core/src/report-context';
import {settingsRowIndex} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import {duration} from '../../../../../../packages/client-core/src/server-admin/console-words';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import {NativeScreenLifetime, screenScopeEnded} from '../../../screens/settings-live/lifetime';
import {NativeHistorySource, historyLocation, type HistoryQuery} from '../../../screens/settings-live/server/history-source';
import {nativeStorageOwner} from '../../../screens/settings-live/server/storage/space-source';

export function historyReportContext(reportError: (error: unknown) => void) {
  let zone = 'UTC';
  try {zone = Intl.DateTimeFormat().resolvedOptions().timeZone || zone;}
  catch (error) {reportError(error);}
  return captureReportContext(zone);
}
function createHistory(context: NativeShellBootstrapContext, reportError: NativeShellRuntime['reportError']) {
  const lifetime = new NativeScreenLifetime(context), current = () => lifetime.current() && nativeStorageOwner(context);
  const api = {request: <T,>(path: string, method?: string, body?: unknown, caller?: AbortSignal): Promise<T> => current()
    ? lifetime.request(signal => context.api.request<T>(path, method, body, signal), caller) : Promise.reject(screenScopeEnded())};
  // The first page is the viewer's cached document (the role is part of the cache's scope); later pages are direct reads.
  // It is read with the cache's own requester, never this screen's: closing this screen only stops it waiting.
  const queries = viewerQueriesFor(context.api, {serverId: lifetime.authority.serverId, viewerId: lifetime.authority.profileId});
  const firstPage = (filters: ServerPlayFilters, signal: AbortSignal, fresh: boolean) => {
    if (!current()) return Promise.reject(screenScopeEnded());
    // This screen stops waiting when it, or the caller, is done; the shared read goes on for any other view.
    const wait = new AbortController(), stop = () => wait.abort(), sources = [signal, lifetime.signal];
    for (const source of sources) {if (source.aborted) stop(); else source.addEventListener('abort', stop, {once: true});}
    return readDocument(queries, serverPlayHistoryQuery(queries.api, filters), {fresh, signal: wait.signal})
      .finally(() => {for (const source of sources) source.removeEventListener('abort', stop);});
  };
  const source = new NativeHistorySource({
    page: (filters, cursor, signal, fresh = true) => cursor ? readServerPlayHistory(api, filters, cursor, signal) : firstPage(filters, signal, fresh),
    people: async (_cursor, signal) => ({items: await readServerAccounts(api, signal), nextCursor: ''}),
  }, current, () => historyReportContext(reportError), reportError);
  const retire = () => source.dispose();lifetime.signal.addEventListener('abort', retire, {once: true});
  return {context, source, current, dispose: () => {source.dispose();lifetime.signal.removeEventListener('abort', retire);lifetime.dispose();}};
}
type Group = ReturnType<typeof createHistory>;

const noSubscription = () => () => {};
const noSnapshot = () => undefined;

export function useServerHistory({ports, runtime}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime}) {
  const {context} = ports, {reportError} = runtime, {t} = runtime.text;
  const [group, setGroup] = useState<Group>(), [search, setSearch] = useState('');
  useLayoutEffect(() => {
    const next = createHistory(context, reportError);
    setGroup(next); setSearch(''); next.source.start();
    return next.dispose;
  }, [context, reportError]);
  const source = group?.context === context ? group.source : undefined;
  const state = useSyncExternalStore(source?.subscribe ?? noSubscription, source?.getSnapshot ?? noSnapshot, source?.getSnapshot ?? noSnapshot);
  useEffect(() => {
    if (!source) return;
    const timer = repeat(async () => {
      const current = source.getSnapshot();
      if (current.phase === 'ready' && current.pages === 1) source.refresh();
    }, {everyMs: 15_000, immediate: false, backoff: {baseMs: 15_000, maxMs: 15_000}});
    return timer.stop;
  }, [source]);
  if (!state || !source) return undefined;

  const table = settingsRowIndex.get('history.plays')!.row;
  const filter = (id: string) => table.control.filters?.find(filter => filter.id === id)!;
  const change = (patch: Partial<HistoryQuery>) => {if (group?.current()) source.change({...state.query, ...patch});};
  const searchLabel = t(table.control.search!);
  const submitSearch = () => change({search: search.trim() || undefined});
  const options = (id: string) => filter(id).options.map(option => ({value: option.value, label: t(option.label)}));
  const people = [{value: '', label: t(filter('person').options[0]!.label)}, ...state.people.map(person => ({value: person.accountId, label: person.username || '—'}))];
  if (state.query.accountId && !people.some(person => person.value === state.query.accountId)) {
    const found = state.entries.find(entry => entry.accountId === state.query.accountId);
    people.push({value: state.query.accountId, label: found?.user ?? t('server.plays.user')});
  }
  const date = new Intl.DateTimeFormat(defaultI18n.locale, {dateStyle: 'medium', timeStyle: 'short', timeZone: state.report.timeZone});
  const row = (entry: PlayHistoryEntry) => {
    const place = historyLocation(entry.location), platform = playHistoryPlatform(entry, {t});
    const watched = entry.watchedMs === undefined ? undefined : duration(entry.watchedMs / 1000);
    const title = playHistoryTitle(entry, {t}), profile = entry.profile !== entry.user ? entry.profile : undefined;
    const player = [entry.device, platform].filter(Boolean).join(' · '), played = date.format(new Date(entry.startedAt));
    const time = entry.completed ? [t('server.jobState.succeeded'), watched].filter(Boolean).join(' · ') : watched;
    const where = place ? t(place === 'home' ? 'next.kit.session.local' : 'next.kit.session.remote') : undefined;
    return {title, description: [entry.user, profile, player, played, time, where].filter((part): part is string => !!part),
      columns: {title, person: [entry.user, profile].filter(Boolean).join('\n'), player, played, for: time, where}};
  };
  return {
    state, source, search, setSearch, searchLabel, submitSearch, change, row,
    columns: (table.control.columns ?? []).flatMap(column => column.id
      ? [{id: column.id, label: t(column.label), weight: Number.parseFloat(column.width ?? '1')}] : []),
    media: options('media'), people, period: options('range'),
    sort: (table.control.columns ?? []).filter(column => column.sort && column.id).map(column => ({value: column.id!, label: t(column.label)})),
    direction: (['desc', 'asc'] as const).map(dir => ({value: dir, label: t('web.filters.direction', {kind: 'other', dir})})),
    labels: {media: t(filter('media').label), person: t(filter('person').label), period: t(filter('range').label)},
    empty: t(table.control.empty!),
  };
}
