import React, {useEffect, useLayoutEffect, useState, useSyncExternalStore} from 'react';
import {Text, View} from 'react-native';
import {playHistoryPlatform, playHistoryTitle, type PlayHistoryEntry} from '../../../../../../packages/client-core/src/play-history';
import {readServerAccounts, readServerPlayHistory, serverPlayHistoryQuery, type ServerPlayFilters} from '../../../../../../packages/client-core/src/admin/server-play-history';
import {readDocument} from '../../../../../../packages/client-core/src/query/admin-documents';
import {viewerQueriesFor} from '../../../shell/freshness';
import {captureReportContext} from '../../../../../../packages/client-core/src/report-context';
import {settingsRowIndex} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import {duration} from '../../../../../../packages/client-core/src/server-admin/console-words';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button, Select, TextField} from '../../../kit/controls';
import {ServerHeader} from '../../../kit/shell/ServerIdentity';
import {shellType} from '../../../kit/shell/visual';
import {StateView} from '../../../kit/states';
import {useTheme} from '../../../kit/theme';
import {SettingsPageFrame} from '../../../kit/frames';
import {useFrameMetrics} from '../../../kit/frames/metrics';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {NativeSettingsLivePorts} from '../ports';
import {NativeScreenLifetime, screenScopeEnded} from '../lifetime';
import {NativeHistorySource, historyLocation, type HistoryQuery} from './history-source';
import {nativeStorageOwner} from './storage/space-source';

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

type Framing = Readonly<{title: string; back?: Readonly<{label: string; onPress: () => void}>; notice?: React.ReactNode}>;
/** The owner's server history is separate from a viewer's personal saved titles. It is one long list on the Settings page
 * frame (header, Back, server strip, gutters): its title and Back come from the screen, and `notice`, the screen's own
 * error, leads the list's own header. */
export function NativeServerHistory({ports, runtime, ...framing}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime} & Framing) {
  const context = ports.context, reportError = runtime.reportError, [group, setGroup] = useState<Group>();
  useLayoutEffect(() => {const next = createHistory(context, reportError);setGroup(next);next.source.start();return next.dispose;}, [context, reportError]);
  return group?.context === context ? <History group={group} runtime={runtime} {...framing}/> : null;
}
function History({group, runtime, title, back, notice}: {group: Group; runtime: NativeShellRuntime} & Framing) {
  const {source} = group, theme = useTheme(), frame = useFrameMetrics(), t = runtime.text.t as typeof defaultI18n.t;
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  const [search, setSearch] = useState('');
  useEffect(() => {const timer = setInterval(() => {const current = source.getSnapshot();if (current.phase === 'ready' && current.pages === 1) source.refresh();}, 15000);return () => clearInterval(timer);}, [source]);
  const table = settingsRowIndex.get('history.plays')!.row;
  const filter = (id: string) => table.control.filters?.find(filter => filter.id === id);
  const change = (patch: Partial<HistoryQuery>) => {if (group.current()) source.change({...state.query, ...patch});};
  const people = [{value: '', label: t(filter('person')!.options[0]!.label)}, ...state.people.map(person => ({value: person.accountId, label: person.username || '—'}))];
  if (state.query.accountId && !people.some(person => person.value === state.query.accountId)) {
    const found = state.entries.find(entry => entry.accountId === state.query.accountId);
    // Preserve a selected real identity through an asynchronous people refresh.
    people.push({value: state.query.accountId, label: found?.user ?? t('server.plays.user')});
  }
  const drawEntry = ({item: entry}: {item: PlayHistoryEntry}) => {
    const place = historyLocation(entry.location), platform = playHistoryPlatform(entry, {t});
    const played = new Intl.DateTimeFormat(defaultI18n.locale, {dateStyle: 'medium', timeStyle: 'short', timeZone: state.report.timeZone}).format(new Date(entry.startedAt));
    return <View style={{paddingVertical: 16, gap: 6, borderBottomWidth: 1, borderBottomColor: theme.colors.lineSoft}}>
      <Text style={[shellType(16, 700), {color: theme.colors.text}]}>{playHistoryTitle(entry, {t})}</Text>
      <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 8}}><Text style={[shellType(14, 600), {color: theme.colors.text}]}>{entry.user}</Text>{entry.profile && entry.profile !== entry.user && <Text style={[shellType(14, 400, 20), {color: theme.colors.textSecondary}]}>{entry.profile}</Text>}</View>
      {(entry.device || platform) && <Text style={[shellType(14, 400, 20), {color: theme.colors.textSecondary}]}>{[entry.device, platform].filter(Boolean).join(' · ')}</Text>}
      <Text style={[shellType(14, 400, 20), {color: theme.colors.textSecondary}]}>{played}</Text>
      <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 12}}>
        {entry.completed ? <Text style={[shellType(14, 400, 20), {color: theme.colors.textSecondary}]}>{t('server.jobState.succeeded')}{entry.watchedMs !== undefined ? ` · ${duration(entry.watchedMs / 1000)}` : ''}</Text> : entry.watchedMs !== undefined ? <Text style={[shellType(14, 400, 20), {color: theme.colors.textSecondary}]}>{duration(entry.watchedMs / 1000)}</Text> : null}
        {place && <Text style={[shellType(14, 400, 20), {color: theme.colors.textTertiary}]}>{t(place === 'home' ? 'next.kit.session.local' : 'next.kit.session.remote')}</Text>}
      </View>
    </View>;
  };
  return <SettingsPageFrame title={title} back={back}
    server={<ServerHeader serverName={group.context.serverName} viewerRole="owner" audience="owner" variant={frame.form === 'phone' ? 'phone' : 'page'}/>}
    list={{testID: 'server-history-list', data: state.entries, keyExtractor: entry => entry.id, renderItem: drawEntry,
    ListHeaderComponent: <View style={{gap: 12}}>
      {notice}
      <TextField testID="server-history-search" label={t(table.control.search!)} value={search} onChangeText={setSearch} maxLength={160} returnKeyType="search" onSubmitEditing={() => change({search: search.trim() || undefined})}/>
      <View style={{flexDirection: 'row', gap: 8}}><Button variant="glass" size="compact" onPress={() => change({search: search.trim() || undefined})}>{t(table.control.search!)}</Button><Button variant="glass" size="compact" disabled={state.phase === 'loading'} onPress={() => source.refresh()}>{t('action.tryAgain')}</Button></View>
      <Select testID="server-history-media" rowLabel={t(filter('media')!.label)} value={state.query.media ?? 'all'} options={filter('media')!.options.map(option => ({value: option.value, label: t(option.label)}))} onValueChange={value => change({media: value === 'all' ? undefined : value})}/>
      <Select testID="server-history-person" rowLabel={t(filter('person')!.label)} value={state.query.accountId ?? ''} options={people} onValueChange={value => change({accountId: value || undefined})}/>
      {state.peopleCursor && <Button variant="glass" size="compact" loading={state.peopleLoading} onPress={() => source.morePeople()}>{t('web.people.morePeople')}</Button>}
      {state.peopleError !== undefined && <StateView variant="inline" {...runtime.error(state.peopleError, () => source.retryPeople(), 'server-console')}/>}
      <Select testID="server-history-period" rowLabel={t(filter('range')!.label)} value={state.query.period} options={filter('range')!.options.map(option => ({value: option.value, label: t(option.label)}))} onValueChange={value => {if (value === 'today' || value === '7d' || value === '30d' || value === 'all') change({period: value});}}/>
      <Select testID="server-history-sort" rowLabel={t('web.filters.sortBy')} value={state.query.sort} options={(table.control.columns ?? []).filter(column => column.sort && column.id).map(column => ({value: column.id!, label: t(column.label)}))} onValueChange={sort => change({sort})}/>
      <Select testID="server-history-direction" rowLabel={t('builder.preset.order')} value={state.query.direction} options={[{value: 'desc', label: t('web.filters.direction', {kind: 'other', dir: 'desc'})}, {value: 'asc', label: t('web.filters.direction', {kind: 'other', dir: 'asc'})}]} onValueChange={direction => {if (direction === 'asc' || direction === 'desc') change({direction});}}/>
      {state.error !== undefined && <StateView variant="inline" {...runtime.error(state.error, () => source.retry(), 'server-console')}/>}
    </View>,
    ListEmptyComponent: state.phase === 'ready' ? <Text style={[shellType(14, 400, 20), {color: theme.colors.textSecondary, marginTop: 20}]}>{t(table.control.empty!)}</Text> : state.phase === 'loading' || state.phase === 'idle' ? <StateView title={t('status.loading')}/> : null,
    ListFooterComponent: <View style={{gap: 14, marginTop: 16}}>
      {!!state.nextCursor && <Button variant="glass" loading={state.phase === 'loading'} disabled={state.phase !== 'ready'} onPress={() => source.more()}>{t('action.showMore')}</Button>}
    </View>}}/>;
}
