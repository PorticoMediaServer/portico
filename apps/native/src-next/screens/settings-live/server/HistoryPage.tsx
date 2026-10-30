import React, {useEffect, useLayoutEffect, useState, useSyncExternalStore} from 'react';
import {FlatList, Text, View} from 'react-native';
import type {RuntimeSettings} from '../../../../../../packages/client-core/src/console';
import {fetchPlayHistory, playHistoryPlatform, playHistoryTitle, type PlayHistoryEntry} from '../../../../../../packages/client-core/src/play-history';
import {DashboardDataClient} from '../../../../../../packages/client-core/src/admin/dashboard';
import {captureReportContext} from '../../../../../../packages/client-core/src/report-context';
import {applySettingsBindingPatch, readSettingsBinding, settingsBinding, settingsBindingPatch} from '../../../../../../packages/client-core/src/presentation/settings-bindings';
import {settingsRowIndex} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import {duration} from '../../../../../../packages/client-core/src/server-admin/console-words';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button, Select, TextField} from '../../../kit/controls';
import {ServerHeader} from '../../../kit/shell/ServerIdentity';
import {shellType} from '../../../kit/shell/visual';
import {StateView} from '../../../kit/states';
import {useTheme} from '../../../kit/theme';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {NativeSettingsLivePorts} from '../ports';
import {NativeScreenLifetime, screenScopeEnded} from '../lifetime';
import {NativeAdvancedSection} from './AdvancedSection';
import {NativeHistorySource, historyLocation, type HistoryQuery} from './history-source';
import {nativeStorageOwner} from './storage/space-source';
import {useFormState, useStorageForms, useStorageSet, type StorageForms} from './storage/forms';
import {settingsPageHeading, settingsPageInset} from './page-layout';

const IDS = ['runtime'] as const;
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
  const dashboard = new DashboardDataClient(api);
  const source = new NativeHistorySource({page: (filters, cursor, signal) => fetchPlayHistory(api, filters, cursor, signal), people: (filters, cursor, signal) => dashboard.people(filters, cursor, 100, signal)}, current, () => historyReportContext(reportError), reportError);
  const retire = () => source.dispose();lifetime.signal.addEventListener('abort', retire, {once: true});
  return {context, source, current, dispose: () => {source.dispose();lifetime.signal.removeEventListener('abort', retire);lifetime.dispose();}};
}
type Group = ReturnType<typeof createHistory>;

/** The owner's server history is separate from a viewer's personal saved titles. */
export function NativeServerHistory({ports, runtime, compactHeader = false}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; compactHeader?: boolean}) {
  const context = ports.context, reportError = runtime.reportError, [group, setGroup] = useState<Group>();
  const forms = useStorageForms(ports, IDS, reportError);
  useLayoutEffect(() => {const next = createHistory(context, reportError);setGroup(next);next.source.start();return next.dispose;}, [context, reportError]);
  return forms && group?.context === context ? <History key={forms.mount} group={group} forms={forms} runtime={runtime} compactHeader={compactHeader}/> : null;
}
function History({group, forms, runtime, compactHeader}: {group: Group; forms: StorageForms; runtime: NativeShellRuntime; compactHeader: boolean}) {
  const {source} = group, theme = useTheme(), t = runtime.text.t as typeof defaultI18n.t;
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot), formState = useStorageSet(forms), settings = useFormState<RuntimeSettings, unknown>(forms.forms.runtime);
  const [search, setSearch] = useState('');
  useEffect(() => {const timer = setInterval(() => {const current = source.getSnapshot();if (current.phase === 'ready' && current.pages === 1) source.refresh();}, 15000);return () => clearInterval(timer);}, [source]);
  const table = settingsRowIndex.get('history.plays')!.row, keep = settingsRowIndex.get('history.keepHistory')!.row;
  const filter = (id: string) => table.control.filters?.find(filter => filter.id === id);
  const change = (patch: Partial<HistoryQuery>) => {if (group.current()) source.change({...state.query, ...patch});};
  const days = settings.draft?.playHistoryDays, keepBinding = settingsBinding(keep.id)!;
  const keepOptions = (keep.control.options ?? []).map(option => ({value: String(option.value), label: t(option.label), disabled: false}));
  const stored = settings.draft ? readSettingsBinding(keepBinding, {forms: {runtime: settings.draft}})?.current : undefined;
  const choice = stored === undefined ? undefined : keepOptions.some(option => option.value === stored) ? String(stored) : 'custom';
  if (choice === 'custom') keepOptions.push({value: 'custom', label: `${defaultI18n.number(days!)} ${t('settings.unit.days')}`, disabled: true});
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
  return <FlatList testID="server-history-list" data={state.entries} keyExtractor={entry => entry.id} renderItem={drawEntry}
    contentContainerStyle={{...settingsPageInset(theme, compactHeader)}}
    ListHeaderComponent={<View style={{gap: 12}}>
      <ServerHeader serverName={group.context.serverName} viewerRole="owner" audience="owner" variant="phone"/>
      {!compactHeader && <Text accessibilityRole="header" style={[settingsPageHeading(theme), {color: theme.colors.text}]}>{t('settings.server.playHistory')}</Text>}
      <TextField testID="server-history-search" label={t(table.control.search!)} value={search} onChangeText={setSearch} maxLength={160} returnKeyType="search" onSubmitEditing={() => change({search: search.trim() || undefined})}/>
      <View style={{flexDirection: 'row', gap: 8}}><Button variant="glass" size="compact" onPress={() => change({search: search.trim() || undefined})}>{t(table.control.search!)}</Button><Button variant="glass" size="compact" disabled={state.phase === 'loading'} onPress={() => source.refresh()}>{t('action.tryAgain')}</Button></View>
      <Select testID="server-history-media" rowLabel={t(filter('media')!.label)} value={state.query.media ?? 'all'} options={filter('media')!.options.map(option => ({value: option.value, label: t(option.label)}))} onValueChange={value => change({media: value === 'all' ? undefined : value})}/>
      <Select testID="server-history-person" rowLabel={t(filter('person')!.label)} value={state.query.accountId ?? ''} options={people} onValueChange={value => change({accountId: value || undefined})}/>
      {state.peopleCursor && <Button variant="glass" size="compact" loading={state.peopleLoading} onPress={() => source.morePeople()}>{t('web.people.morePeople')}</Button>}
      {state.peopleError !== undefined && <StateView variant="inline" {...runtime.error(state.peopleError, () => source.retryPeople(), 'server-console')}/>}
      <Select testID="server-history-period" rowLabel={t(filter('range')!.label)} value={state.query.period} options={filter('range')!.options.map(option => ({value: option.value, label: t(option.label)}))} onValueChange={value => {if (value === 'today' || value === '7d' || value === '30d' || value === 'all') change({period: value});}}/>
      <Select testID="server-history-sort" rowLabel={t('web.filters.sortBy')} value={state.query.sort} options={(table.control.columns ?? []).filter(column => column.sort && column.id).map(column => ({value: column.id!, label: t(column.label)}))} onValueChange={sort => change({sort})}/>
      <Select testID="server-history-direction" rowLabel={t('builder.preset.order')} value={state.query.direction} options={[{value: 'desc', label: t('web.filters.direction', {kind: 'other', dir: 'desc'})}, {value: 'asc', label: t('web.filters.direction', {kind: 'other', dir: 'asc'})}]} onValueChange={direction => {if (direction === 'asc' || direction === 'desc') change({direction});}}/>
      {state.total !== undefined && <Text style={[shellType(14, 400, 20), {color: theme.colors.textSecondary}]}>{defaultI18n.number(state.total)} {table.control.noun ? t(table.control.noun[state.total === 1 ? 0 : 1]!) : ''}</Text>}
      {state.error !== undefined && <StateView variant="inline" {...runtime.error(state.error, () => source.retry(), 'server-console')}/>}
    </View>}
    ListEmptyComponent={state.phase === 'ready' ? <Text style={[shellType(14, 400, 20), {color: theme.colors.textSecondary, marginTop: 20}]}>{t(table.control.empty!)}</Text> : state.phase === 'loading' || state.phase === 'idle' ? <StateView title={t('status.loading')}/> : null}
    ListFooterComponent={<View style={{gap: 14, marginTop: 16}}>
      {!!state.nextCursor && <Button variant="glass" loading={state.phase === 'loading'} disabled={state.phase !== 'ready'} onPress={() => source.more()}>{t('action.showMore')}</Button>}
      <NativeAdvancedSection testID="server-history-advanced" summary={days === undefined ? t('status.loading') : days === 0 ? t('next.settings.allDefaults') : `${t(keep.label)}: ${keepOptions.find(option => option.value === choice)?.label ?? days}`}>
        {choice !== undefined && <Select testID="history.keepHistory" rowLabel={t(keep.label)} description={keep.help ? t(keep.help) : undefined} value={choice} options={keepOptions} disabled={formState.saving} onValueChange={value => {
          if (value === 'custom' || formState.saving || !group.current() || !settings.draft) return;
          const patch = settingsBindingPatch(keepBinding, value, {forms: {runtime: settings.draft}});
          forms.forms.runtime.set((draft: RuntimeSettings) => applySettingsBindingPatch(draft, patch));
        }}/>} 
        {settings.loadError !== undefined && <StateView variant="inline" {...runtime.error(settings.loadError, () => {void forms.forms.runtime.load();}, 'server-console')}/>}
      </NativeAdvancedSection>
      {formState.dirty && <View style={{flexDirection: 'row', gap: 8}}><Button variant="glass" disabled={formState.saving} onPress={() => forms.set.discard()}>{t('action.discard')}</Button><Button loading={formState.saving} disabled={Boolean(formState.problem)} onPress={() => {void forms.set.save();}}>{t('action.save')}</Button></View>}
      {formState.saveError !== undefined && <StateView variant="inline" {...runtime.error(formState.saveError, () => {void forms.set.save();}, 'server-console')}/>}
    </View>}/>;
}
