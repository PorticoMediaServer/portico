import React, {useCallback, useEffect, useMemo, useState} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {serverNow} from '../../../../../../packages/client-core/src/server-clock';
import {checkServerUpdates, latestVersionLine, readServerUpdates, type ServerUpdates} from '../../../../../../packages/client-core/src/server-admin/server-updates';
import {OwnerGeneralSettings, readBackgroundPause, setBackgroundPause} from '../../../../../../packages/client-core/src/server-admin/general-settings';
import {withRequestRaw} from '../../../../../../packages/client-core/src/home-selection/list-destinations';
import {restartServer} from '../../../../../../packages/client-core/src/backups';
import type {SettingsConfiguration} from '../../../../../../packages/client-core/src/settings-configuration';
import {settingsSpec, type SettingsRow as SpecRow} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import type {MessageId} from '../../../../../../packages/i18n/src';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {when} from '../../../../../../packages/client-core/src/server-admin/console-words';
import {Button} from '../../../kit/controls/Button';
import {SettingsRow} from '../../../kit/shell';
import {BottomSheet} from '../../../kit/overlays/BottomSheet';
import {ServerHeader} from '../../../kit/shell/ServerIdentity';
import {StateView} from '../../../kit/states';
import {shellType} from '../../../kit/shell/visual';
import {useTheme} from '../../../kit/theme';
import type {NativeSettingsLivePorts} from '../ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {screenScopeEnded} from '../lifetime';
import {useSettingsRequestScope} from '../use-settings-request-scope';
import {NativeAdvancedSection} from './AdvancedSection';
import {NativeSettingControl} from './SettingControl';
import {NativeBroadcastSheet} from './BroadcastSheet';
import {settingsSummary} from './settings-values';
import {settingsPageHeading, settingsPageInset} from './page-layout';
type Translate = typeof defaultI18n.t;
type Group = Readonly<{title?: MessageId; rows: readonly SpecRow[]}>;

/** Settings › Server › General on the phone. Every row reads and saves the rebuilt server's settings
 * documents through the same client-core reader web uses (OwnerGeneralSettings); a draft is saved row by
 * row, each with the revision it was read at. */
export function NativeServerGeneral({ports, runtime, compactHeader = false}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; compactHeader?: boolean}) {
  const theme = useTheme(), t = runtime.text.t as Translate, context = ports.context;
  const {api, lifetime} = useSettingsRequestScope(context);
  const settings = useMemo(() => new OwnerGeneralSettings(withRequestRaw(api, context.api.requestRaw?.bind(context.api), () => {if (!lifetime.current()) throw screenScopeEnded();})), [api, context, lifetime]);
  const [configuration, setConfiguration] = useState<SettingsConfiguration>(), [loadError, setLoadError] = useState<unknown>();
  const [drafts, setDrafts] = useState<ReadonlyMap<string, unknown>>(new Map()), [saving, setSaving] = useState(false), [saveError, setSaveError] = useState<unknown>();
  const [updates, setUpdates] = useState<ServerUpdates>(), [updateError, setUpdateError] = useState<unknown>(), [checking, setChecking] = useState(false);
  const [pausedUntil, setPausedUntil] = useState<string>(), [busy, setBusy] = useState(false), [problem, setProblem] = useState<string>();
  const [messageOpen, setMessageOpen] = useState(false), [restarting, setRestarting] = useState(false), [advanced, setAdvanced] = useState(false);
  const section = settingsSpec.find(value => value.scope === 'server' && value.id === 'general');
  const main: readonly Group[] = section?.groups ?? [], extra: readonly Group[] = section?.advanced?.groups ?? [];
  const reread = useCallback(async () => {const next = await settings.readConfiguration(); if (lifetime.current()) setConfiguration(next);}, [settings, lifetime]);
  const load = useCallback(() => {
    void reread().then(() => {if (lifetime.current()) setLoadError(undefined);}, error => {if (lifetime.current()) setLoadError(error);});
    void readBackgroundPause(api).then(value => {if (lifetime.current()) setPausedUntil(value);}, () => {});
  }, [reread, api, lifetime]);
  useEffect(load, [load]);
  const loadUpdates = useCallback((check = false) => {
    if (!lifetime.current()) return;
    setChecking(true); setUpdateError(undefined);
    void (check ? checkServerUpdates : readServerUpdates)(api).then(next => {if (lifetime.current()) setUpdates(next);})
      .catch(error => {if (lifetime.current()) setUpdateError(error);}).finally(() => {if (lifetime.current()) setChecking(false);});
  }, [api, lifetime]);
  useEffect(() => {loadUpdates();}, [loadUpdates]);

  const value = (id: string) => drafts.has(id) ? drafts.get(id) : settings.read(id)?.current;
  const enabled = (row: SpecRow) => {
    const rule = row.enableWhen; if (!rule) return true;
    const current = value(rule.id) as never;
    return (!rule.in || rule.in.includes(current)) && (!rule.notIn || !rule.notIn.includes(current));
  };
  const change = (row: SpecRow, next: unknown) => {if (!saving) {setSaveError(undefined); setDrafts(current => new Map(current).set(row.id, next));}};
  // Spec order, so Heavy work's own choice is stored before its window, and each save reads the
  // documents again: the next row is saved against the revision this one made.
  const save = async () => {
    if (saving || !lifetime.current()) return;
    setSaving(true); setSaveError(undefined);
    try {
      for (const row of [...main, ...extra].flatMap(group => group.rows)) {
        if (!drafts.has(row.id)) continue;
        await settings.save(row.id, drafts.get(row.id));
        await reread();
        if (lifetime.current()) setDrafts(current => {const next = new Map(current); next.delete(row.id); return next;});
      }
    } catch (error) {
      if (lifetime.current()) setSaveError(error);
      await reread().catch(() => {});
    } finally {if (lifetime.current()) setSaving(false);}
  };
  const paused = !!pausedUntil && Date.parse(pausedUntil) > serverNow();
  const pause = () => {
    setBusy(true); setProblem(undefined);
    void setBackgroundPause(api, !paused).then(next => {if (lifetime.current()) setPausedUntil(next);}, error => {if (lifetime.current()) setProblem(presentError(error, 'server-console').body);}).finally(() => {if (lifetime.current()) setBusy(false);});
  };
  const restart = () => {
    setBusy(true); setProblem(undefined);
    void restartServer(api).then(() => {if (lifetime.current()) {setRestarting(false); setBusy(false);}}, error => {if (lifetime.current()) {setBusy(false); setProblem(presentError(error, 'server-console').body);}});
  };

  const action = (label: string, onPress: () => void, options: {variant?: 'glass' | 'danger'; icon?: 'i-refresh' | 'i-mail'; loading?: boolean; disabled?: boolean; testID: string}) =>
    <View key={options.testID} style={{flexDirection: 'row', marginTop: 4}}><Button testID={options.testID} size="compact" variant={options.variant ?? 'glass'} icon={options.icon} loading={options.loading} disabled={options.disabled} onPress={onPress}>{label}</Button></View>;
  const renderRow = (row: SpecRow): React.ReactNode => {
    switch (row.id) {
      case 'general.version': return updates ? <SettingsRow key={row.id} first label={t(row.label)} value={updates.version}
        description={[latestVersionLine(updates, t), updates.checkedAt ? when(Date.parse(updates.checkedAt)) : ''].filter(Boolean).join(' · ')}/>
        : updateError !== undefined ? <StateView key={row.id} variant="inline" title={presentError(updateError, 'server-console').title} body={presentError(updateError, 'server-console').body} tone="error" primaryAction={{id: 'retry', label: t('action.tryAgain'), onPress: () => loadUpdates()}}/> : null;
      case 'general.checkNow': return action(t('server.updates.check'), () => loadUpdates(true), {icon: 'i-refresh', loading: checking, disabled: !enabled(row), testID: 'server-general-check'});
      case 'general.pauseBackground': return action(t(paused ? 'settings.server.resumeBackground' : 'settings.server.pauseBackground'), pause, {loading: busy, testID: 'server-general-pause'});
      case 'general.message': return action(t('server.messageEveryone'), () => setMessageOpen(true), {icon: 'i-mail', testID: 'server-general-message'});
      case 'general.restart': return <View key={row.id}>
        <Text style={[shellType(12.5, 400, 17), {color: theme.colors.textTertiary}]}>{t('server.restart.body')}</Text>
        {action(t('web.maintenance.serverRestart'), () => setRestarting(true), {variant: 'danger', disabled: busy, testID: 'server-general-restart-open'})}
      </View>;
      case 'general.configFile': return configuration?.path ? <SettingsRow key={row.id} first label={t(row.label)} control={<Text selectable style={{color: theme.colors.textSecondary, fontFamily: 'monospace', flexShrink: 1}}>{configuration.path}</Text>}/> : null;
      case 'general.serverId': return <SettingsRow key={row.id} first label={t(row.label)} control={<Text selectable style={{color: theme.colors.textSecondary, fontFamily: 'monospace', flexShrink: 1}}>{context.authority().serverId}</Text>}/>;
    }
    if (!configuration || !settings.has(row.id) || settings.read(row.id) === undefined) return null;
    if (row.enableWhen?.hide && !enabled(row)) return null;
    return <NativeSettingControl key={row.id} row={row} value={value(row.id)} t={t} disabled={saving || !enabled(row)} onChange={next => change(row, next)}
      onCustom={row.control.options?.some(option => option.value === 'custom') ? () => {change(row, 'custom'); setAdvanced(true);} : undefined}/>;
  };
  const renderGroup = (group: Group, index: number) => {
    const rows = group.rows.map(renderRow).filter(Boolean);
    if (!rows.length) return null;
    return <View key={index} style={{gap: 8, marginTop: 22}}>
      {group.title && <Text accessibilityRole="header" style={[shellType(17, 800), {color: theme.colors.text}]}>{t(group.title)}</Text>}
      <View style={{gap: 10}}>{rows}</View>
    </View>;
  };
  const viewerRole = context.account.account.role === 'owner' ? 'owner' : context.account.canManage ? 'administrator' : 'member';
  if (loadError !== undefined && !configuration) return <StateView {...runtime.error(loadError, load, 'server-console')}/>;
  return <View style={{flex: 1}}>
    <ScrollView contentContainerStyle={{...settingsPageInset(theme, compactHeader)}}>
      <ServerHeader serverName={context.serverName} viewerRole={viewerRole} audience={viewerRole === 'administrator' ? 'administrators' : 'owner'} variant="phone" style={{marginBottom: 12}}/>
      {!compactHeader && <Text accessibilityRole="header" style={[settingsPageHeading(theme), {color: theme.colors.text}]}>{t('settings.server.general')}</Text>}
      {configuration ? main.map(renderGroup) : <StateView title={t('status.loading')}/>}
      {configuration && extra.length > 0 && <NativeAdvancedSection testID="server-general-advanced" open={advanced} onOpenChange={setAdvanced}
        summary={settingsSummary(extra.flatMap(group => group.rows), row => {const fact = settings.read(row.id); return fact && {...fact, current: value(row.id)};}, t)}>
        {extra.map(renderGroup)}
      </NativeAdvancedSection>}
      {problem ? <Text accessibilityRole="alert" style={[shellType(13, 600), {color: theme.colors.record, marginTop: 10}]}>{problem}</Text> : null}
      {drafts.size > 0 ? <View style={{flexDirection: 'row', gap: 8, marginTop: 16}}>
        <Button testID="server-general-discard" variant="glass" disabled={saving} onPress={() => {setDrafts(new Map()); setSaveError(undefined);}}>{t('action.discard')}</Button>
        <Button testID="server-general-save" loading={saving} onPress={() => {void save();}}>{t('action.save')}</Button>
      </View> : null}
      {saveError !== undefined && <StateView variant="inline" {...runtime.error(saveError, () => {void save();}, 'server-console')}/>}
    </ScrollView>
    <NativeBroadcastSheet ports={ports} runtime={runtime} open={messageOpen} onOpenChange={setMessageOpen}/>
    <BottomSheet open={restarting} onOpenChange={open => {if (!open && !busy) setRestarting(false);}} testID="server-general-restart"
      title={t('web.maintenance.serverRestart')} description={t('server.restart.body')}
      footer={<View style={{flexDirection: 'row', gap: 8}}><Button variant="glass" disabled={busy} onPress={() => setRestarting(false)}>{t('action.cancel')}</Button>
        <Button variant="danger" loading={busy} onPress={restart}>{t('web.maintenance.serverRestart')}</Button></View>}><View/></BottomSheet>
  </View>;
}
