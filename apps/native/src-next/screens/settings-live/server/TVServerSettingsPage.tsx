import React, {useCallback, useEffect, useState} from 'react';
import {Text, View} from 'react-native';
import {routePath} from '../../../../../../packages/contracts/src/routes';
import {parseSettingsConfiguration, type SettingsConfiguration} from '../../../../../../packages/client-core/src/settings-configuration';
import {applySettingsBindingPatch, settingsBinding, settingsBindingPatch} from '../../../../../../packages/client-core/src/presentation/settings-bindings';
import {settingsSpec, type SettingsRow as SpecRow} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import type {ServerFormId} from '../../../../../../packages/client-core/src/server-admin/server-forms';
import type {MessageId} from '../../../../../../packages/i18n/src';
import {Button} from '../../../kit/controls';
import {shellType} from '../../../kit/shell/visual';
import {StateView} from '../../../kit/states';
import {useTheme} from '../../../kit/theme';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../ports';
import {useSettingsRequestScope} from '../use-settings-request-scope';
import {settingsSummary} from './settings-values';
import {useFormState, useStorageForms, useStorageSet, type StorageForms} from './storage/forms';
import {TVChoiceRow, TVEntryRow, TVSwitchRow, type TVChoice} from './TVSettingRows';
import {TVFolderRow} from './TVFolderPicker';
import {parseTranscodeCapacity, type TranscodeCapacity} from '../../../../../../packages/client-core/src/telemetry';
import {tvCustomChoiceValue, tvPageForms, tvServerSettingFact, tvServerSettingGroups, tvServerSettingsSources, type TVChoiceModel, type TVEditablePage, type TVServerSettingsState, type TVSettingGroup, type TVSettingItem} from './tv-server-settings-model';

type FormView = Readonly<{draft?: Readonly<Record<string, unknown>>; loading: boolean; loadError?: unknown}>;

/** Owner-only: the page's spec rows that have a client-core binding, drawn with the TV kit.
 * tv-more 23: one sheet is one decision, so each committed choice is saved at once. */
export function TVServerSettingsPage({page, ports, runtime}: {page: TVEditablePage; ports: NativeSettingsLivePorts; runtime: NativeShellRuntime}) {
  const group = useStorageForms(ports, tvPageForms[page], runtime.reportError);
  return group ? <Page key={group.mount} page={page} group={group} ports={ports} runtime={runtime}/> : <StateView title={runtime.text.t('status.loading')}/>;
}

/** One subscription per form the page owns; the page never reads another page's document. */
function useForms(group: StorageForms, ids: readonly ServerFormId[]): Readonly<Partial<Record<ServerFormId, FormView>>> {
  const fallback = group.forms[ids[0]!];
  const views = {
    runtime: useFormState<Record<string, unknown>, unknown>(group.forms.runtime ?? fallback),
    maintenance: useFormState<Record<string, unknown>, unknown>(group.forms.maintenance ?? fallback),
    alerts: useFormState<Record<string, unknown>, unknown>(group.forms.alerts ?? fallback),
    connectivity: useFormState<Record<string, unknown>, unknown>(group.forms.connectivity ?? fallback),
    certificate: useFormState<Record<string, unknown>, unknown>(group.forms.certificate ?? fallback),
  } as Readonly<Record<string, FormView>>;
  return Object.fromEntries(ids.map(id => [id, views[id]]));
}

function Page({page, group, ports, runtime}: {page: TVEditablePage; group: StorageForms; ports: NativeSettingsLivePorts; runtime: NativeShellRuntime}) {
  const theme = useTheme(), t = runtime.text.t;
  const {forms, set} = group, saving = useStorageSet(group), ids = tvPageForms[page], views = useForms(group, ids);
  const {api, lifetime} = useSettingsRequestScope(ports.context);
  const [configuration, setConfiguration] = useState<SettingsConfiguration>(), [configurationError, setConfigurationError] = useState<unknown>();
  const [advanced, setAdvanced] = useState(false), [custom, setCustom] = useState<ReadonlySet<string>>(new Set());
  const loadConfiguration = useCallback(() => {
    if (!lifetime.current()) return;
    void api.request(routePath('get_settings_configuration')).then(parseSettingsConfiguration).then(value => {if (lifetime.current()) {setConfiguration(value); setConfigurationError(undefined);}}, error => {if (lifetime.current()) setConfigurationError(error);});
  }, [api, lifetime]);
  useEffect(loadConfiguration, [loadConfiguration]);
  // Capacity supplies memory admission and the adapters this host found.
  // The location preference and disk folder remain independent runtime settings.
  const [capacity, setCapacity] = useState<TranscodeCapacity>();
  useEffect(() => {
    if (page !== 'streaming' || !lifetime.current()) return;
    const read = new AbortController();
    void api.request(routePath('get_admin_transcode_capacity'), 'GET', undefined, read.signal).then(parseTranscodeCapacity)
      .then(value => {if (!read.signal.aborted && lifetime.current()) setCapacity(value);}, () => {/* memory remains unavailable until measured */});
    return () => read.abort();
  }, [api, lifetime, page]);
  const section = settingsSpec.find(value => value.scope === 'server' && value.id === page);
  const drafts = Object.fromEntries(ids.map(id => [id, views[id]?.draft]));
  const certificate = drafts.certificate;
  const linked = page === 'remote' && !!ports.context.account.account.hostedAccountId && certificate?.claimed === true;
  const state: TVServerSettingsState = {page, configuration, drafts, custom, secure: linked ? certificate?.enabled === true : undefined, capacity};
  const busy = saving.saving;
  const commit = () => {void set.save().catch(() => {/* kept in saveError and drawn below */});};
  const change = (row: SpecRow, value: unknown) => {
    if (!lifetime.current() || busy) return;
    setCustom(previous => {if (!previous.has(row.id)) return previous; const next = new Set(previous); next.delete(row.id); return next;});
    if (row.id === 'remote.secure') {forms.certificate.set(current => ({...current, enabled: value === true})); commit(); return;}
    const binding = settingsBinding(row.id);
    if (!binding) return;
    let patch;
    try {patch = settingsBindingPatch(binding, value, tvServerSettingsSources(state));} catch (error) {runtime.reportError(error); return;}
    if (!ids.includes(patch.form as ServerFormId)) return;
    forms[patch.form as ServerFormId].set((current: Readonly<Record<string, unknown>>) => applySettingsBindingPatch(current, patch));
    commit();
  };
  const chooseCustom = (row: SpecRow) => {
    if (!lifetime.current() || busy) return;
    const stored = tvCustomChoiceValue(row, drafts);
    if (stored !== undefined) change(row, stored);
    setCustom(previous => new Set(previous).add(row.id)); setAdvanced(true);
  };
  const choices = (options: readonly TVChoiceModel[]): TVChoice[] => options.map(option => ({value: option.value, label: option.plain || option.label === option.value ? option.label : t(option.label as MessageId), description: option.hint ? t(option.hint as MessageId) : undefined, disabled: option.disabled}));
  const renderItem = (item: TVSettingItem): React.ReactNode => {
    const {row} = item, label = t(row.label), description = row.help ? t(row.help) : undefined;
    const copy = {testID: item.id, label, description, dependent: item.dependent};
    switch (item.kind) {
      case 'switch': return <TVSwitchRow key={item.id} {...copy} checked={item.checked} onChange={next => change(row, next)}/>;
      case 'choice': return <TVChoiceRow key={item.id} {...copy} value={item.value} options={choices(item.options)} onChange={next => {
        if (next === 'custom') {chooseCustom(row); return;}
        const option = row.control.options?.find(option => String(option.value) === next);
        if (option && !option.locked) change(row, option.value);
        else if (row.id === 'streaming.hardware' && item.options.some(choice => choice.value === next && !choice.disabled)) change(row, next);
      }}/>;
      case 'number': return <TVEntryRow key={item.id} {...copy} kind="number" value={item.value} control={row.control} t={t} onSave={next => change(row, next)}/>;
      case 'text': return <TVEntryRow key={item.id} {...copy} kind="text" value={item.value} maxLength={row.id === 'general.name' ? 100 : undefined} t={t} onSave={next => change(row, next)}/>;
      case 'secret': return <TVEntryRow key={item.id} {...copy} kind="secret" configured={item.configured} t={t} onSave={next => {if (next) change(row, next);}}/>;
      case 'folder': return <TVFolderRow key={item.id} {...copy} value={item.value} api={api} disabled={busy} t={t} onChoose={next => change(row, next)}/>;
      case 'matrix': return <View key={item.id}>
        <Text style={[shellType(20, 700), {color: theme.colors.textTertiary, marginLeft: 24, marginTop: 14, marginBottom: 6}]}>{label}</Text>
        {item.items.map(entry => <TVChoiceRow key={entry.id} testID={`${item.id}.${entry.id}`} label={t(entry.label)} value={entry.value} options={choices(item.columns)} dependent
          onChange={next => change(row, {...item.values, [entry.id]: next})}/>)}
      </View>;
    }
  };
  const renderGroups = (groups: readonly TVSettingGroup[]) => groups.map((value, index) => <View key={index} style={{marginBottom: 30}}>
    {value.title && <Text style={[shellType(20, 700), {color: theme.colors.textTertiary, marginLeft: 24, marginBottom: 10}]}>{t(value.title)}</Text>}
    <View style={{backgroundColor: theme.colors.raised, borderRadius: 18}}>{value.items.map(renderItem)}</View>
  </View>);
  const loadError = ids.map(id => views[id]).find(view => view?.loadError !== undefined && view.draft === undefined)?.loadError;
  if (loadError !== undefined) return <StateView testID={`tv-server-${page}-settings-error`} {...runtime.error(loadError, () => {for (const id of ids) void forms[id].load();})}/>;
  if (configurationError !== undefined) return <StateView testID={`tv-server-${page}-configuration-error`} {...runtime.error(configurationError, loadConfiguration)}/>;
  if (!configuration || ids.some(id => views[id]?.loading)) return <StateView title={t('status.loading')}/>;
  const main = tvServerSettingGroups(state, section?.groups), extra = tvServerSettingGroups(state, section?.advanced?.groups);
  return <View testID={`tv-server-${page}-settings`}>
    {renderGroups(main)}
    {extra.length > 0 && <View style={{flexDirection: 'row', alignItems: 'center', gap: 20, marginBottom: 24}}>
      <Button testID={`tv-server-${page}-advanced`} variant="glass" size="small" trailingIcon={advanced ? 'i-up' : 'i-down'} onPress={() => setAdvanced(value => !value)}>{t('settings.section.advanced')}</Button>
      {!advanced && <Text numberOfLines={1} style={[shellType(20), {flex: 1, color: theme.colors.textTertiary}]}>{settingsSummary(extra.flatMap(value => value.items.map(item => item.row)), row => tvServerSettingFact(state, row), t)}</Text>}
    </View>}
    {advanced && renderGroups(extra)}
    {saving.problem && <Text accessibilityRole="alert" style={[shellType(22, 600), {color: theme.colors.record, marginBottom: 16}]}>{t(saving.problem as MessageId)}</Text>}
    {saving.saveError !== undefined && <StateView variant="inline" testID={`tv-server-${page}-save-error`} {...runtime.error(saving.saveError, commit)}/>}
    {saving.dirty && !busy && (saving.saveError !== undefined || saving.problem) && <View style={{flexDirection: 'row'}}>
      <Button testID={`tv-server-${page}-discard`} variant="glass" onPress={() => {set.discard(); setCustom(new Set());}}>{t('action.discard')}</Button>
    </View>}
  </View>;
}
