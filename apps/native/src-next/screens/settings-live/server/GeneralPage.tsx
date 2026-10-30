import {useSettingsRequestScope} from '../use-settings-request-scope';
import React, {useCallback, useEffect, useState} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {parseUpdateReport, type UpdateReport} from '../../../../../../packages/client-core/src/administration';
import {pauseBackgroundWork, type AlertThresholds, type MaintenanceDocument, type MaintenanceSettings} from '../../../../../../packages/client-core/src/server-admin/server-forms';
import {restartServer} from '../../../../../../packages/client-core/src/backups';
import {routePath} from '../../../../../../packages/contracts/src/routes';
import {parseSettingsConfiguration, type SettingsConfiguration} from '../../../../../../packages/client-core/src/settings-configuration';
import {applySettingsBindingPatch, readSettingsBinding, settingsBinding, settingsBindingPatch, type SettingsBindingSources} from '../../../../../../packages/client-core/src/presentation/settings-bindings';
import {settingsSpec, settingsRowIndex, type SettingsRow as SpecRow} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import type {RuntimeSettings} from '../../../../../../packages/client-core/src/console';
import type {MessageId} from '../../../../../../packages/i18n/src';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {when} from '../../../../../../packages/client-core/src/server-admin/console-words';
import {Button} from '../../../kit/controls/Button';
import {HeavyWorkControls} from './HeavyWorkControls';
import {NativeAdvancedSection} from './AdvancedSection';
import {NativeSettingControl} from './SettingControl';
import {NativeBroadcastSheet} from './BroadcastSheet';
import {settingsSummary} from './settings-values';
import {SettingsRow} from '../../../kit/shell';
import {TextField} from '../../../kit/controls/TextField';
import {BottomSheet} from '../../../kit/overlays/BottomSheet';
import {ServerHeader} from '../../../kit/shell/ServerIdentity';
import {StateView} from '../../../kit/states';
import {shellType} from '../../../kit/shell/visual';
import {useTheme} from '../../../kit/theme';
import type {NativeSettingsLivePorts} from '../ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {useFormState, useStorageForms, useStorageSet, type StorageForms} from './storage/forms';
import {settingsPageHeading, settingsPageInset} from './page-layout';

type Translate = typeof defaultI18n.t;
type ViewerRole = 'owner' | 'administrator' | 'member';
const IDS = ['runtime', 'maintenance', 'alerts'] as const;

/** Settings › Server › General on the phone (slots server-general group 1–2 and the drawn background/restart rows). */
export function NativeServerGeneral({ports, runtime, compactHeader = false}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; compactHeader?: boolean}) {
  const group = useStorageForms(ports, IDS, runtime.reportError);
  return group ? <General key={group.mount} group={group} ports={ports} runtime={runtime} compactHeader={compactHeader}/> : null;
}
function General({ports, runtime, compactHeader = false, group}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; compactHeader?: boolean; group: StorageForms}) {
  const theme = useTheme(), t = runtime.text.t as Translate, context = ports.context;
  const {api, lifetime} = useSettingsRequestScope(context);
  const {forms, set} = group, state = useStorageSet(group);
  const runtimeState = useFormState<RuntimeSettings, unknown>(forms.runtime);
  const maintenanceState = useFormState<MaintenanceSettings, MaintenanceDocument>(forms.maintenance);
  const [updates, setUpdates] = useState<UpdateReport>(), [updateError, setUpdateError] = useState<unknown>(), [checking, setChecking] = useState(false);
  const [busy, setBusy] = useState(false), [problem, setProblem] = useState<string>(), [restarting, setRestarting] = useState(false);
  const alertsState = useFormState<AlertThresholds, {revision: number}>(forms.alerts);
  const [messageOpen, setMessageOpen] = useState(false), [advanced, setAdvanced] = useState(false), [customUpdate, setCustomUpdate] = useState(false), [customHeavyDraft, setCustomHeavyDraft] = useState<MaintenanceSettings>();
  const [configuration, setConfiguration] = useState<SettingsConfiguration>(), [configurationError, setConfigurationError] = useState<unknown>();
  const loadConfiguration = useCallback(() => {
    if (!lifetime.current()) return;
    void api.request(routePath('get_settings_configuration')).then(parseSettingsConfiguration).then(value => {if (lifetime.current()) {setConfiguration(value); setConfigurationError(undefined);}}, error => {if (lifetime.current()) setConfigurationError(error);});
  }, [api, lifetime]);
  useEffect(loadConfiguration, [loadConfiguration]);
  useEffect(() => setCustomUpdate(false), [runtimeState.saved]);
  const section = settingsSpec.find(value => value.scope === 'server' && value.id === 'general');
  const sources: SettingsBindingSources = {configuration, forms: {runtime: runtimeState.draft, maintenance: maintenanceState.draft, alerts: alertsState.draft}};
  const fact = (row: SpecRow) => {
    const binding = settingsBinding(row.id);
    if (!binding) return undefined;
    const policy = configuration?.fields.find(field => field.id === (row.id === 'general.updateChecks' ? 'general.updateFeed' : row.id));
    if (policy?.configOnly || row.id === 'general.updateChecks' && typeof policy?.default !== 'string') return undefined;
    const current = row.id === 'general.updateChecks' && configuration && runtimeState.draft
      ? readSettingsBinding(binding, {...sources, configuration: {...configuration, fields: configuration.fields.map(field => field.id === 'general.updateFeed' ? {...field, current: runtimeState.draft!['updates.feedUrl'], effective: runtimeState.draft!['updates.feedUrl']} : field)}}, row.id)
      : readSettingsBinding(binding, {forms: sources.forms}, row.id);
    if (!current) return undefined;
    return {...current, default: configuration?.fields.find(field => field.id === row.id)?.default ?? current.default};
  };
  const change = (row: SpecRow, value: unknown) => {
    if (!lifetime.current() || state.saving) return;
    const binding = settingsBinding(row.id); if (!binding || !['runtime', 'maintenance', 'alerts'].includes(binding.form)) return;
    const patch = settingsBindingPatch(binding, value, sources);
    forms[patch.form as keyof typeof forms].set(current => applySettingsBindingPatch(current, patch));
    if (row.id === 'general.updateChecks') setCustomUpdate(false);
  };
  const renderRow = (row: SpecRow) => {
    const value = fact(row); if (!value) return null;
    const custom = row.id === 'general.updateChecks' && customUpdate;
    return <NativeSettingControl key={row.id} row={row} value={custom ? 'custom' : value.current} t={t} disabled={state.saving || row.id === 'general.updateFeed' && !(customUpdate || fact(settingsRowIndex.get('general.updateChecks')!.row)?.current === 'custom')}
      onCustom={row.id === 'general.updateChecks' ? () => {setCustomUpdate(true); setAdvanced(true);} : undefined} onChange={next => change(row, next)}/>;
  };
  const viewerRole: ViewerRole = context.account.account.role === 'owner' ? 'owner' : context.account.canManage ? 'administrator' : 'member';
  const loadUpdates = useCallback(() => {
    if (!lifetime.current()) return;
    setChecking(true); setUpdateError(undefined);
    void api.request<unknown>('/v1/admin/updates').then(raw => {if (lifetime.current()) setUpdates(parseUpdateReport(raw));})
      .catch(error => {if (lifetime.current()) setUpdateError(error);}).finally(() => {if (lifetime.current()) setChecking(false);});
  }, [api, lifetime]);
  useEffect(() => {loadUpdates();}, [loadUpdates]);
  const now = Date.now(), pausedUntil = maintenanceState.status?.pausedUntilUnixMillis ?? 0, paused = pausedUntil > now;
  const pause = () => {
    setBusy(true); setProblem(undefined);
    void pauseBackgroundWork({api}, !paused, ports.requestId()).then(() => forms.maintenance.load(), error => setProblem(presentError(error, 'server-console').body)).finally(() => setBusy(false));
  };
  const restart = () => {
    setBusy(true); setProblem(undefined);
    void restartServer(api).then(() => setRestarting(false), error => {setBusy(false); setProblem(presentError(error, 'server-console').body);});
  };
  if (runtimeState.loadError !== undefined && !runtimeState.draft) return <StateView {...runtime.error(runtimeState.loadError, () => {void forms.runtime.load();}, 'server-console')}/>;
  const draft = runtimeState.draft;
  return <View style={{flex: 1}}>
    <ScrollView contentContainerStyle={{...settingsPageInset(theme, compactHeader)}}>
      <ServerHeader serverName={context.serverName} viewerRole={viewerRole} audience={viewerRole === 'administrator' ? 'administrators' : 'owner'} variant="phone" style={{marginBottom: 12}}/>
      {!compactHeader && <Text accessibilityRole="header" style={[settingsPageHeading(theme), {color: theme.colors.text}]}>{t('settings.server.general')}</Text>}
      {draft ? <View style={{marginTop: 16}}>
        <Text style={[shellType(17, 800), {color: theme.colors.text}]}>{t('web.general.serverName')}</Text>
        <TextField testID="server-general-name" value={draft.name} onChangeText={value => forms.runtime.set(current => ({...current, name: value}))} maxLength={100} containerStyle={{marginTop: 10}}/>
      </View> : null}
      <Text style={[shellType(17, 800), {color: theme.colors.text, marginTop: 22}]}>{t('settings.server.updates')}</Text>
      {renderRow(settingsRowIndex.get('general.updateChecks')!.row)}
      {configurationError !== undefined && <StateView variant="inline" {...runtime.error(configurationError, loadConfiguration, 'server-console')}/>}
      {updateError !== undefined && !updates ? <StateView title={presentError(updateError, 'server-console').title} body={presentError(updateError, 'server-console').body} tone="error" primaryAction={{id: 'retry', label: t('action.tryAgain'), onPress: loadUpdates}}/> : null}
      {updates ? <View style={{marginTop: 8, borderRadius: 14, overflow: 'hidden', backgroundColor: theme.colors.raised}}>
        <View style={{paddingHorizontal: 14, paddingVertical: 11}}>
          <View style={{flexDirection: 'row', justifyContent: 'space-between', gap: 10}}>
            <Text style={[shellType(15, 600), {color: theme.colors.text}]}>{t('web.general.serverProduct')}</Text>
            <Text style={[shellType(14, 600), {color: updates.updateAvailable ? theme.colors.amber : theme.colors.textSecondary}]}>{updates.updateAvailable && updates.latest ? updates.latest.version : updates.current.version}</Text>
          </View>
          <View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: 10, rowGap: 3}}>{[updates.message, when(Date.parse(updates.checkedAt))].filter(Boolean).map((part, index) => <Text key={index} style={[shellType(12.5, 400, 17), {color: theme.colors.textTertiary, marginTop: 2}]}>{part}</Text>)}</View>
        </View>
      </View> : null}
      <View style={{flexDirection: 'row', marginTop: 10}}><Button size="compact" variant="glass" icon="i-refresh" loading={checking} disabled={fact(settingsRowIndex.get('general.updateChecks')!.row)?.current === 'never'} onPress={loadUpdates}>{t('server.updates.check')}</Button></View>
      {maintenanceState.draft ? <>
        <HeavyWorkControls form={forms.maintenance} state={maintenanceState} t={t} placement="main" open={advanced} onOpenChange={setAdvanced} customDraft={customHeavyDraft} onCustomDraftChange={setCustomHeavyDraft}/>
        {renderRow(settingsRowIndex.get('general.backgroundSpeed')!.row)}
        <View style={{flexDirection: 'row', marginTop: 12}}>
          <Button size="compact" variant="glass" loading={busy} onPress={pause}>{t(paused ? 'settings.server.resumeBackground' : 'settings.server.pauseBackground')}</Button>
        </View>
      </> : null}
      <Text style={[shellType(17, 800), {color: theme.colors.text, marginTop: 22}]}>{t('server.messageEveryone')}</Text>
      <View style={{flexDirection: 'row', marginTop: 10}}><Button size="compact" variant="glass" icon="i-mail" onPress={() => setMessageOpen(true)}>{t('server.messageEveryone')}</Button></View>
      <Text style={[shellType(17, 800), {color: theme.colors.text, marginTop: 22}]}>{t('web.maintenance.serverRestart')}</Text>
      <Text style={[shellType(12.5, 400, 17), {color: theme.colors.textTertiary, marginTop: 2}]}>{t('server.restart.body')}</Text>
      <View style={{flexDirection: 'row', marginTop: 10}}><Button size="compact" variant="danger" disabled={busy} onPress={() => setRestarting(true)}>{t('web.maintenance.serverRestart')}</Button></View>
      <NativeAdvancedSection testID="server-general-advanced" open={advanced} onOpenChange={setAdvanced}
        summary={runtimeState.loading || maintenanceState.loading || alertsState.loading ? t('status.loading') : settingsSummary(section?.advanced?.groups?.flatMap(group => group.rows) ?? [], fact, t)}>
        {section?.advanced?.groups?.map((group, index) => {
          if (group.rows.some(row => row.id === 'general.heavyDays')) return null;
          const rows = group.rows.some(row => row.id === 'general.heavyStart')
            ? <HeavyWorkControls form={forms.maintenance} state={maintenanceState} t={t} placement="advanced" customDraft={customHeavyDraft} onCustomDraftChange={setCustomHeavyDraft}/>
            : group.rows.map(row => row.id === 'general.configFile' ? configuration && <SettingsRow key={row.id} first label={t(row.label)} description={row.help ? t(row.help) : undefined} control={<Text selectable style={{color: theme.colors.textSecondary, fontFamily: 'monospace', flexShrink: 1}}>{configuration.path}</Text>}/>
              : row.id === 'general.serverId' ? <SettingsRow key={row.id} label={t(row.label)} control={<Text selectable style={{color: theme.colors.textSecondary, fontFamily: 'monospace', flexShrink: 1}}>{context.authority().serverId}</Text>}/>
                : renderRow(row)).filter(Boolean);
          if (Array.isArray(rows) && !rows.length) return null;
          return <View key={index} style={{gap: 8}}>{group.title && <Text style={[shellType(16, 700), {color: theme.colors.textSecondary}]}>{t(group.title)}</Text>}<View style={{borderRadius: 14, backgroundColor: theme.colors.raised, padding: 12, gap: 10}}>{rows}</View></View>;
        })}
        {alertsState.loadError !== undefined && <StateView variant="inline" {...runtime.error(alertsState.loadError, () => {void forms.alerts.load();}, 'server-console')}/>}
      </NativeAdvancedSection>
      {problem ? <Text accessibilityRole="alert" style={[shellType(13, 600), {color: theme.colors.record, marginTop: 10}]}>{problem}</Text> : null}
      {state.dirty ? <View style={{flexDirection: 'row', gap: 8, marginTop: 16}}>
        <Button variant="glass" disabled={state.saving} onPress={() => {set.discard(); setCustomUpdate(false); setCustomHeavyDraft(undefined);}}>{t('action.discard')}</Button>
        <Button loading={state.saving} disabled={Boolean(state.problem)} onPress={() => {void set.save();}}>{t('action.save')}</Button>
      </View> : null}
      {state.saveError !== undefined && <StateView variant="inline" {...runtime.error(state.saveError, () => {void set.save();}, 'server-console')}/>}
      {state.problem ? <Text accessibilityRole="alert" style={[shellType(13, 600), {color: theme.colors.record, marginTop: 8}]}>{t(state.problem as MessageId)}</Text> : null}
    </ScrollView>
    <NativeBroadcastSheet ports={ports} runtime={runtime} open={messageOpen} onOpenChange={setMessageOpen}/>
    <BottomSheet open={restarting} onOpenChange={open => {if (!open && !busy) setRestarting(false);}} testID="server-general-restart"
      title={t('web.maintenance.serverRestart')} description={t('server.restart.body')}
      footer={<View style={{flexDirection: 'row', gap: 8}}><Button variant="glass" disabled={busy} onPress={() => setRestarting(false)}>{t('action.cancel')}</Button>
        <Button variant="danger" loading={busy} onPress={restart}>{t('web.maintenance.serverRestart')}</Button></View>}><View/></BottomSheet>
  </View>;
}
