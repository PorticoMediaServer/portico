import React from 'react';
import {Text, View} from 'react-native';
import {DVR_KEEP_PRESETS, DVR_PADDING_PRESETS, DVR_SPACE_PRESETS} from '../../../../../packages/client-core/src/server-admin/owner-dvr-settings';
import {tunerStatus} from '../../../../../packages/client-core/src/server-admin/live-words';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';
import {defaultI18n, type MessageId} from '../../../../../packages/i18n/src';
import {Button, Select, Switch} from '../controls';
import {BottomSheet} from '../overlays';
import {SettingsRow} from '../shell';
import {useTheme} from '../theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {FolderExplorer} from '../../screens/settings-live/owner-libraries/FolderExplorer';
import {PanelBlock, SectionCard} from '../frames';
import {useNewRecordingRule, useOwnerRecording} from '../../pages/shared/settings/useOwnerRecording';

const G = 'next.settingsSpec.server.live.groups';

export function OwnerRecordingSettings({context, runtime}: {context: NativeShellBootstrapContext; runtime: NativeShellRuntime}) {
  const theme = useTheme();
  const {t, rebuilt, folder, tuners, overview, rules, accounts, busy, error, choosing, setChoosing, removing, setRemoving,
    cancelBookings, setCancelBookings, adding, setAdding, load, saveDvr, v, padding, keep, space, choices, permission, toggleRule, removeRule} = useOwnerRecording(context, runtime);
  return <View style={{gap: theme.space.controlGap}} testID="live-recording-owner">
    {error !== undefined && <Text accessibilityRole="alert" style={[theme.type.rowHelp, {color: theme.colors.record}]}>{presentError(error, 'server-console', {operation: 'action'}).body}</Text>}

    <SectionCard label={t('web.dvrSettings.tuners')}>
    {tuners && !tuners.sources.length && <SettingsRow first label={t('web.dvrSettings.noTuners')}/>}
    {tuners?.sources.map(source => {const status = tunerStatus(t, source); return <SettingsRow key={source.sourceId} first label={source.sourceName}
      description={[source.paused ? t('web.liveTv.pausedBadge') : '', ...source.active.map(use => `${use.using} · ${use.channel}`)].filter(Boolean).join('\n') || undefined}
      control={<Text style={[theme.type.rowHelp, {color: status.tone === 'healthy' ? theme.colors.healthy : status.tone === 'warning' ? theme.colors.amber : theme.colors.textSecondary}]}>{status.text}</Text>}/>;})}

    </SectionCard>
    <SectionCard label={t('web.dvrSettings.whoCanRecord')} help={t('web.liveTv.recordingAccountHelp')}>
    {[...accounts].map(([id, rows]) => {const account = rows[0]!; return <View key={id} style={{gap: 4}}>
      <SettingsRow first label={account.accountName} description={t(account.ownerAccount ? 'web.liveTv.ownerMayRecord' : 'web.liveTv.accountMayRecord')}
        control={<Switch accessibilityLabel={account.accountName} checked={account.accountAllowed} disabled={busy || account.ownerAccount}
          onCheckedChange={enabled => permission(account, enabled, false)}/>}/>
      {rows.map(row => <SettingsRow key={row.profileId} label={row.profileName} description={t(row.canRecord ? 'next.liveOwner.canRecord' : 'next.liveOwner.cannotRecord')}
        control={<Switch accessibilityLabel={row.profileName} checked={row.accountAllowed && row.profileAllowed} disabled={busy || !row.accountAllowed}
          onCheckedChange={enabled => permission(row, enabled, true)}/>}/>)}
    </View>;})}

    </SectionCard>
    <SectionCard label={t('web.recordingRules.title')}>
    <PanelBlock><Button testID="live-recording-new-rule" size="compact" variant="glass" disabled={busy} onPress={() => setAdding(true)}>{t('web.recordingRules.newRule')}</Button></PanelBlock>
    {!rules.length && <SettingsRow first label={t('web.recordingRules.noRules')}/>}
    {rules.map(rule => <SettingsRow key={rule.id} first label={rule.title} description={[rule.profileName, rule.channelName, rule.paused ? t('web.recordingRules.pausedRule') : ''].filter(Boolean).join(' · ')}
      control={<View style={{flexDirection: 'row', gap: 6}}>
        <Button size="xs" variant="glass" disabled={busy} onPress={() => toggleRule(rule)}>{t(rule.paused ? 'web.recordingRules.resumeRule' : 'web.recordingRules.pauseRule')}</Button>
        <Button size="xs" variant="glass" disabled={busy} onPress={() => {setCancelBookings(false); setRemoving(rule);}}>{t('web.recordingRules.remove')}</Button>
      </View>}/>)}

    </SectionCard>
    {v && <>
      <SectionCard label={t('web.dvrSettings.defaults')}><PanelBlock>
      <Select label={t(`${G}.6.rows.0.label` as MessageId)} value={padding} disabled={busy} options={choices(`${G}.6.rows.0`, ['standard', 'exact', 'generous', 'sport', 'custom'])}
        onValueChange={value => {if (value in DVR_PADDING_PRESETS) saveDvr(DVR_PADDING_PRESETS[value as keyof typeof DVR_PADDING_PRESETS]);}}/>
      <Select label={t('server.live.tunerConflict')} description={t('server.live.tunerConflictHelp')} value={v.stopViewers ? 'stop-watching' : 'keep-watching'} disabled={busy}
        options={[{value: 'keep-watching', label: t('server.live.tunerConflict.keep')}, {value: 'stop-watching', label: t('server.live.tunerConflict.stop')}]} onValueChange={value => saveDvr({stopViewers: value === 'stop-watching'})}/>
      <SettingsRow first label={t('web.liveTv.recordingFolder')} description={[folder?.folder ?? v.folder, folder?.state === 'unwritable' ? `${t('web.liveTv.notWritable')}${folder.problem ? ` ${folder.problem}` : ''}` : folder?.state === 'not_made' ? t('web.liveTv.recordingFolderNotMade') : ''].filter(Boolean).join('\n')}
        control={<Button size="xs" variant="glass" disabled={busy} onPress={() => setChoosing(true)}>{t('server.live.choose')}</Button>}/>
      <Select label={t(`${G}.6.rows.2.label` as MessageId)} value={keep} disabled={busy} options={choices(`${G}.6.rows.2`, ['all', 'd30', 'd90', 'n5', 'n10', 'custom'])}
        onValueChange={value => {if (value in DVR_KEEP_PRESETS) saveDvr(DVR_KEEP_PRESETS[value as keyof typeof DVR_KEEP_PRESETS]);}}/>
      <Select label={t(`${G}.7.rows.1.label` as MessageId)} value={space} disabled={busy}
        options={['free10', 'up250', 'up500', 'up1000', 'custom'].map(value => ({value, disabled: value === 'custom', label: value === 'custom' ? t(`${G}.7.rows.1.control.options.4.label` as MessageId)
          : value === 'free10' ? t('web.liveTv.leaveFreeAmount', {size: defaultI18n.bytes(10 * 1024 ** 3)}) : t('web.liveTv.capAmount', {size: defaultI18n.bytes(DVR_SPACE_PRESETS[value as 'up250'].capBytes)})}))}
        onValueChange={value => {if (value in DVR_SPACE_PRESETS) saveDvr(DVR_SPACE_PRESETS[value as keyof typeof DVR_SPACE_PRESETS]);}}/>
      {overview && <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{[t('next.live.keepRule', {rule: overview.keepRule}), overview.folderOk ? t('web.dvr.hoursLeft', {count: overview.hoursLeft}) : ''].filter(Boolean).join(' · ')}</Text>}
      </PanelBlock></SectionCard>
      <FolderExplorer open={choosing} onOpenChange={setChoosing} api={rebuilt} title={t('web.liveTv.recordingFolder')} chosen={[]} multiple={false}
        onUse={folders => {const next = folders[0]; if (next) saveDvr({folder: next.path});}} testID="live-recording-folder"/>
    </>}
    <NewRuleSheet context={context} runtime={runtime} open={adding} onOpenChange={setAdding} onSaved={() => {setAdding(false); load();}}/>
    <BottomSheet open={!!removing} onOpenChange={open => {if (!open && !busy) setRemoving(undefined);}} title={removing ? t('web.recordingRules.removeTitle', {name: removing.title}) : undefined} description={t('web.recordingRules.removeBody')}
      footer={<><Button variant="glass" disabled={busy} onPress={() => setRemoving(undefined)}>{t('action.cancel')}</Button>
        <Button variant="danger" loading={busy} onPress={removeRule}>{t('web.recordingRules.remove')}</Button></>}>
      <SettingsRow first label={t('web.recordingRules.cancelBookings')} control={<Switch accessibilityLabel={t('web.recordingRules.cancelBookings')} checked={cancelBookings} disabled={busy} onCheckedChange={setCancelBookings}/>}/>
    </BottomSheet>
  </View>;
}

export function NewRuleSheet({context, runtime, open, onOpenChange, onSaved}: {context: NativeShellBootstrapContext; runtime: NativeShellRuntime; open: boolean; onOpenChange: (open: boolean) => void; onSaved: () => void}) {
  const theme = useTheme();
  const {t, owners, profileId, setProfileId, day, setDay, programmes, cursor, programme, setProgramme, newOnly, setNewOnly,
    busy, error, guide, save, days} = useNewRecordingRule({context, runtime, open, onSaved});
  return <BottomSheet open={open} onOpenChange={next => {if (!next && !busy) onOpenChange(false);}} title={t('web.recordingRules.newTitle')} scrollable testID="live-new-rule"
    footer={<><Button variant="glass" disabled={busy} onPress={() => onOpenChange(false)}>{t('action.cancel')}</Button>
      <Button testID="live-new-rule-save" loading={busy} disabled={busy || !profileId || !programme} onPress={() => {void save();}}>{t('action.save')}</Button></>}>
    <PanelBlock inset={false}>
      {error !== undefined && <Text accessibilityRole="alert" style={[theme.type.rowHelp, {color: theme.colors.record}]}>{presentError(error, 'server-console', {operation: 'action'}).body}</Text>}
      <Select label={t('web.recordingRules.recordFor')} value={profileId} disabled={busy}
        options={[{value: '', label: t('web.recordingRules.chooseProfile')}, ...owners.map(row => ({value: row.profileId, label: `${row.accountName} · ${row.profileName}`}))]} onValueChange={setProfileId}/>
      <Select label={t('web.recordingRules.guideDate')} value={day} disabled={busy} options={days} onValueChange={setDay}/>
      <Select label={t('web.recordingRules.seriesFromGuide')} value={programme} disabled={busy || !profileId}
        options={[{value: '', label: t('web.recordingRules.chooseProgram')}, ...programmes.map(row => ({value: row.id, label: `${row.title ?? ''} · ${row.channelName} · ${defaultI18n.formatDate(row.startsAt, {hour: 'numeric', minute: '2-digit'})}`}))]} onValueChange={setProgramme}/>
      {cursor && <Button size="compact" variant="glass" disabled={busy} onPress={() => guide(cursor)}>{t('web.live.empty.moreTitle')}</Button>}
      <SettingsRow first label={t('guide.newOnly')} control={<Switch accessibilityLabel={t('guide.newOnly')} checked={newOnly} disabled={busy} onCheckedChange={setNewOnly}/>}/>
    </PanelBlock>
  </BottomSheet>;
}
