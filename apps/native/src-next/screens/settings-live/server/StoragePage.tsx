import React, {useState} from 'react';
import {ScrollView, Text, View} from 'react-native';
import type {RuntimeSettings} from '../../../../../../packages/client-core/src/console';
import type {LogSettings} from '../../../../../../packages/client-core/src/server-administration';
import type {LogsDraft, MaintenanceDocument, MaintenanceSettings} from '../../../../../../packages/client-core/src/server-admin/server-forms';
import {readSettingsBinding, settingsBinding} from '../../../../../../packages/client-core/src/presentation/settings-bindings';
import {settingsSpec} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import {defaultI18n, type MessageId} from '../../../../../../packages/i18n/src';
import {Button} from '../../../kit/controls';
import {ServerHeader} from '../../../kit/shell/ServerIdentity';
import {StateView} from '../../../kit/states';
import {useTheme} from '../../../kit/theme';
import type {NativeSettingsLivePorts} from '../ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {LimitsTab} from './storage/LimitsTab';
import {BackupsTab} from './storage/BackupsTab';
import {NativeStorageSpaceUsage} from './storage/SpaceUsage';
import {NativeEmptyCache} from './storage/EmptyCache';
import {NativeDownloadsUsage} from './storage/DownloadsUsage';
import {useFormState, useStorageForms, useStorageSet, type StorageForms} from './storage/forms';
import {settingsSummary} from './settings-values';
import {storageExtraSummary} from './storage/summary';
import {settingsPageHeading, settingsPageInset} from './page-layout';

const IDS = ['runtime', 'maintenance', 'logs'] as const;
type ViewerRole = 'owner' | 'administrator' | 'member';
/** Storage & backups is one page; Missing titles belongs to Libraries. */
export function NativeServerStorage({ports, runtime, compactHeader = false, onOpenPage}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; compactHeader?: boolean; onOpenPage: (page: 'history' | 'troubleshooting') => void}) {
  const group = useStorageForms(ports, IDS, runtime.reportError);
  return group ? <Storage key={group.mount} ports={ports} runtime={runtime} compactHeader={compactHeader} group={group} onOpenPage={onOpenPage}/> : null;
}
function Storage({ports, runtime, compactHeader, group, onOpenPage}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; compactHeader: boolean; group: StorageForms; onOpenPage: (page: 'history' | 'troubleshooting') => void}) {
  const theme = useTheme(), t = runtime.text.t as typeof defaultI18n.t, context = ports.context, state = useStorageSet(group);
  const runtimeState = useFormState<RuntimeSettings, unknown>(group.forms.runtime);
  const maintenanceState = useFormState<MaintenanceSettings, MaintenanceDocument>(group.forms.maintenance);
  const logsState = useFormState<LogsDraft, LogSettings>(group.forms.logs);
  const [advanced, setAdvanced] = useState(false);
  const viewerRole: ViewerRole = context.account.account.role === 'owner' ? 'owner' : context.account.canManage ? 'administrator' : 'member';
  const sources = {forms: {runtime: runtimeState.draft, maintenance: maintenanceState.draft}};
  const section = settingsSpec.find(section => section.scope === 'server' && section.id === 'storage');
  const specSummary = settingsSummary(section?.advanced?.groups?.flatMap(group => group.rows) ?? [], row => {
    const binding = settingsBinding(row.id); return binding ? readSettingsBinding(binding, sources, row.id) : undefined;
  }, t);
  const extra = storageExtraSummary(runtimeState.draft, maintenanceState.draft, maintenanceState.status, logsState.draft, t);
  const allDefaults = t('next.settings.allDefaults'), loaded = !!runtimeState.draft && !!maintenanceState.draft && !!maintenanceState.status && !!logsState.draft;
  const summary = [specSummary === allDefaults ? '' : specSummary, ...extra].filter(Boolean).join(' · ') || (loaded ? allDefaults : t('status.loading'));
  return <View style={{flex:1}}><ScrollView contentContainerStyle={{...settingsPageInset(theme, compactHeader)}}>
    <ServerHeader serverName={context.serverName} viewerRole={viewerRole} audience={viewerRole === 'administrator' ? 'administrators' : 'owner'} variant="phone" style={{marginBottom:12}}/>
    {!compactHeader && <Text accessibilityRole="header" style={[settingsPageHeading(theme),{color:theme.colors.text}]}>{t('settings.server.storage')}</Text>}
    <View style={{marginTop:14}}><NativeStorageSpaceUsage context={context} runtime={runtime}/></View>
    <NativeEmptyCache context={context} runtime={runtime}/>
    <BackupsTab ports={ports} runtime={runtime} t={t} advancedOpen={advanced} onAdvancedOpenChange={setAdvanced} advancedSummary={summary}
      advancedContent={<>
        <LimitsTab group={group} t={t} onOpenPage={onOpenPage}/>
        <NativeDownloadsUsage ports={ports} runtime={runtime}/>
      </>}>
    </BackupsTab>
    {Object.entries(group.forms).map(([id, form]) => form.getSnapshot().loadError !== undefined && <StateView key={id} variant="inline" {...runtime.error(form.getSnapshot().loadError, () => {void form.load();}, 'server-console')}/>)}
    {state.dirty && <View style={{flexDirection: 'row', gap: 8, marginTop: 16}}>
      <Button variant="glass" disabled={state.saving} onPress={() => {group.set.discard(); }}>{t('action.discard')}</Button>
      <Button loading={state.saving} disabled={Boolean(state.problem)} onPress={() => {void group.set.save();}}>{t('action.save')}</Button>
    </View>}
    {state.problem && <Text accessibilityRole="alert" style={{color: theme.colors.record}}>{t(state.problem as MessageId)}</Text>}
    {state.saveError !== undefined && <StateView variant="inline" {...runtime.error(state.saveError, () => {void group.set.save();}, 'server-console')}/>}
  </ScrollView></View>;
}
