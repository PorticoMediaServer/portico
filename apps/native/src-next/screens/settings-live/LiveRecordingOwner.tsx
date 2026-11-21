import React, {useCallback, useEffect, useMemo, useState} from 'react';
import {Text, View} from 'react-native';
import type {DvrFolderStatus, DvrOverview, DvrTuners} from '../../../../../packages/contracts/src/next/api';
import {DVR_KEEP_PRESETS, DVR_PADDING_PRESETS, DVR_SPACE_PRESETS, matchingDvrPreset, readDvrFolder, readDvrTuners, readOwnerDvrSettings, readRecordingStorage, saveOwnerDvrSettings, type OwnerDvrDocument, type OwnerDvrSettings} from '../../../../../packages/client-core/src/server-admin/owner-dvr-settings';
import {readRecordingPermissions, saveRecordingPermission, type DvrPermission} from '../../../../../packages/client-core/src/server-admin/owner-recording-permissions';
import {readOwnerRecordingRules, readRecordingRuleProgrammes, removeOwnerRecordingRule, saveOwnerRecordingRule, type DvrRule, type RecordingRuleProgramme} from '../../../../../packages/client-core/src/server-admin/owner-recording-rules';
import {tunerStatus} from '../../../../../packages/client-core/src/server-admin/live-words';
import {withGuardedRaw} from '../../../../../packages/client-core/src/home-selection/list-destinations';
import type {LibraryContentApi} from '../../../../../packages/client-core/src/library-content';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';
import {defaultI18n, type MessageId} from '../../../../../packages/i18n/src';
import {Button, Select, Switch} from '../../kit/controls';
import {BottomSheet} from '../../kit/overlays';
import {SettingsRow} from '../../kit/shell';
import {shellType} from '../../kit/shell/visual';
import {useTheme} from '../../kit/theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {FolderExplorer} from '../../kit/settings/FolderExplorer';
import {screenScopeEnded} from './lifetime';
import {useSettingsRequestScope} from './use-settings-request-scope';

type Translate = typeof defaultI18n.t;
const G = 'next.settingsSpec.server.live.groups';

/** Settings › Server › Live TV › Recording on the phone, on the rebuilt server: tuners and what they are doing, who may
 * record, recording rules, recording defaults, the recordings folder, and how long recordings are kept and how much space
 * they may use. The same reads and saves web's Recording page uses (client-core owner-dvr-settings, owner-recording-
 * permissions, owner-recording-rules); each choice is saved when it is made, at the revision read. */
export function NativeRecordingOwner({context, runtime}: {context: NativeShellBootstrapContext; runtime: NativeShellRuntime}) {
  const theme = useTheme(), t = runtime.text.t as Translate;
  const {api, lifetime} = useSettingsRequestScope(context);
  const rebuilt = useMemo(() => withGuardedRaw(api, context.api, () => {if (!lifetime.current()) throw screenScopeEnded();}) as unknown as LibraryContentApi, [api, context, lifetime]);
  const [dvr, setDvr] = useState<OwnerDvrDocument>(), [overview, setOverview] = useState<DvrOverview>(), [folder, setFolder] = useState<DvrFolderStatus>(), [tuners, setTuners] = useState<DvrTuners>();
  const [permissions, setPermissions] = useState<readonly DvrPermission[]>([]), [rules, setRules] = useState<readonly DvrRule[]>([]);
  const [busy, setBusy] = useState(false), [error, setError] = useState<unknown>(), [choosing, setChoosing] = useState(false), [removing, setRemoving] = useState<DvrRule>(), [cancelBookings, setCancelBookings] = useState(false), [adding, setAdding] = useState(false);
  const load = useCallback(() => {
    const keep = <T,>(set: (value: T) => void) => (value: T) => {if (lifetime.current()) set(value);};
    const fail = (failure: unknown) => {if (lifetime.current()) setError(failure);};
    void readOwnerDvrSettings(rebuilt).then(keep(setDvr), fail);
    void readRecordingStorage(rebuilt).then(keep(setOverview), fail);
    void readDvrFolder(rebuilt).then(keep(setFolder), fail);
    void readDvrTuners(rebuilt).then(keep(setTuners), fail);
    void readRecordingPermissions(rebuilt).then(keep(setPermissions), fail);
    void readOwnerRecordingRules(rebuilt).then(keep(setRules), fail);
  }, [rebuilt, lifetime]);
  useEffect(load, [load]);
  const run = async (work: () => Promise<unknown>) => {
    if (busy || !lifetime.current()) return;
    setBusy(true); setError(undefined);
    try {await work();} catch (failure) {if (lifetime.current()) setError(failure);} finally {if (lifetime.current()) {setBusy(false); load();}}
  };
  const saveDvr = (change: Partial<OwnerDvrSettings>) => {if (dvr) void run(async () => setDvr(await saveOwnerDvrSettings(rebuilt, dvr, change)));};
  const choices = (row: string, values: readonly string[]) => values.map((value, index) => ({value, label: t(`${row}.control.options.${index}.label` as MessageId), disabled: value === 'custom'}));
  const v = dvr?.value;
  const padding = v ? matchingDvrPreset(DVR_PADDING_PRESETS, v) : 'custom', keep = v ? matchingDvrPreset(DVR_KEEP_PRESETS, v) : 'custom', space = v ? matchingDvrPreset(DVR_SPACE_PRESETS, v) : 'custom';
  const heading = (label: string) => <Text accessibilityRole="header" style={[shellType(17, 800), {color: theme.colors.text, marginTop: 18}]}>{label}</Text>;
  const accounts = new Map<string, DvrPermission[]>();
  for (const row of permissions) accounts.set(row.accountId, [...(accounts.get(row.accountId) ?? []), row]);
  return <View style={{gap: 10}} testID="live-recording-owner">
    {error !== undefined && <Text accessibilityRole="alert" style={[shellType(13, 600), {color: theme.colors.record}]}>{presentError(error, 'server-console', {operation: 'action'}).body}</Text>}

    {heading(t('web.dvrSettings.tuners'))}
    {tuners && !tuners.sources.length && <Text style={[shellType(13), {color: theme.colors.textSecondary}]}>{t('web.dvrSettings.noTuners')}</Text>}
    {tuners?.sources.map(source => {const status = tunerStatus(t, source); return <SettingsRow key={source.sourceId} first label={source.sourceName}
      description={[source.paused ? t('web.liveTv.pausedBadge') : '', ...source.active.map(use => `${use.using} · ${use.channel}`)].filter(Boolean).join('\n') || undefined}
      control={<Text style={[shellType(13, 600), {color: status.tone === 'healthy' ? theme.colors.healthy : status.tone === 'warning' ? theme.colors.amber : theme.colors.textSecondary}]}>{status.text}</Text>}/>;})}

    {heading(t('web.dvrSettings.whoCanRecord'))}
    <Text style={[shellType(13), {color: theme.colors.textSecondary}]}>{t('web.liveTv.recordingAccountHelp')}</Text>
    {[...accounts].map(([id, rows]) => {const account = rows[0]!; return <View key={id} style={{gap: 4}}>
      <SettingsRow first label={account.accountName} description={t(account.ownerAccount ? 'web.liveTv.ownerMayRecord' : 'web.liveTv.accountMayRecord')}
        control={<Switch accessibilityLabel={account.accountName} checked={account.accountAllowed} disabled={busy || account.ownerAccount}
          onCheckedChange={enabled => {void run(async () => setPermissions(await saveRecordingPermission(rebuilt, {accountId: account.accountId, enabled, revision: account.accountRevision})));}}/>}/>
      {rows.map(row => <SettingsRow key={row.profileId} label={row.profileName} description={t(row.canRecord ? 'next.liveOwner.canRecord' : 'next.liveOwner.cannotRecord')}
        control={<Switch accessibilityLabel={row.profileName} checked={row.accountAllowed && row.profileAllowed} disabled={busy || !row.accountAllowed}
          onCheckedChange={enabled => {void run(async () => setPermissions(await saveRecordingPermission(rebuilt, {accountId: row.accountId, profileId: row.profileId, enabled, revision: row.revision})));}}/>}/>)}
    </View>;})}

    {heading(t('web.recordingRules.title'))}
    <View style={{flexDirection: 'row'}}><Button testID="live-recording-new-rule" size="compact" variant="glass" disabled={busy} onPress={() => setAdding(true)}>{t('web.recordingRules.newRule')}</Button></View>
    {!rules.length && <Text style={[shellType(13), {color: theme.colors.textSecondary}]}>{t('web.recordingRules.noRules')}</Text>}
    {rules.map(rule => <SettingsRow key={rule.id} first label={rule.title} description={[rule.profileName, rule.channelName, rule.paused ? t('web.recordingRules.pausedRule') : ''].filter(Boolean).join(' · ')}
      control={<View style={{flexDirection: 'row', gap: 6}}>
        <Button size="xs" variant="glass" disabled={busy} onPress={() => {void run(() => saveOwnerRecordingRule(rebuilt, {paused: !rule.paused}, rule.id));}}>{t(rule.paused ? 'web.recordingRules.resumeRule' : 'web.recordingRules.pauseRule')}</Button>
        <Button size="xs" variant="glass" disabled={busy} onPress={() => {setCancelBookings(false); setRemoving(rule);}}>{t('web.recordingRules.remove')}</Button>
      </View>}/>)}

    {v && <>
      {heading(t('web.dvrSettings.defaults'))}
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
      {overview && <Text style={[shellType(13), {color: theme.colors.textSecondary}]}>{[t('next.live.keepRule', {rule: overview.keepRule}), overview.folderOk ? t('web.dvr.hoursLeft', {count: overview.hoursLeft}) : ''].filter(Boolean).join(' · ')}</Text>}
      <FolderExplorer open={choosing} onOpenChange={setChoosing} api={rebuilt} title={t('web.liveTv.recordingFolder')} chosen={[]} multiple={false}
        onUse={folders => {const next = folders[0]; if (next) saveDvr({folder: next.path});}} testID="live-recording-folder"/>
    </>}
    <NewRuleSheet context={context} runtime={runtime} open={adding} onOpenChange={setAdding} onSaved={() => {setAdding(false); load();}}/>
    <BottomSheet open={!!removing} onOpenChange={open => {if (!open && !busy) setRemoving(undefined);}} title={removing ? t('web.recordingRules.removeTitle', {name: removing.title}) : undefined} description={t('web.recordingRules.removeBody')}
      footer={<View style={{flexDirection: 'row', gap: 8}}><Button variant="glass" disabled={busy} onPress={() => setRemoving(undefined)}>{t('action.cancel')}</Button>
        <Button variant="danger" loading={busy} onPress={() => {const rule = removing; if (rule) void run(async () => {await removeOwnerRecordingRule(rebuilt, rule.id, cancelBookings); setRemoving(undefined);});}}>{t('web.recordingRules.remove')}</Button></View>}>
      <SettingsRow first label={t('web.recordingRules.cancelBookings')} control={<Switch accessibilityLabel={t('web.recordingRules.cancelBookings')} checked={cancelBookings} disabled={busy} onCheckedChange={setCancelBookings}/>}/>
    </BottomSheet>
  </View>;
}

/** A new recording rule on the rebuilt server: the owner chooses a profile that may record and a programme from the
 * guide; the server makes the series (or title) rule for it (`POST /v1/dvr/rules`, client-core saveOwnerRecordingRule). */
export function NewRuleSheet({context, runtime, open, onOpenChange, onSaved}: {context: NativeShellBootstrapContext; runtime: NativeShellRuntime; open: boolean; onOpenChange: (open: boolean) => void; onSaved: () => void}) {
  const theme = useTheme(), t = runtime.text.t as Translate;
  const {api, lifetime} = useSettingsRequestScope(context);
  const [owners, setOwners] = useState<readonly DvrPermission[]>([]), [profileId, setProfileId] = useState(''), [day, setDay] = useState(() => new Date().toISOString().slice(0, 10));
  const [programmes, setProgrammes] = useState<readonly RecordingRuleProgramme[]>([]), [cursor, setCursor] = useState<string>(), [programme, setProgramme] = useState(''), [newOnly, setNewOnly] = useState(false);
  const [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  useEffect(() => {
    if (!open) return;
    setProfileId(''); setProgramme(''); setNewOnly(false); setError(undefined);
    void readRecordingPermissions(api).then(rows => {if (lifetime.current()) setOwners(rows.filter(row => row.canRecord));}, failure => {if (lifetime.current()) setError(failure);});
  }, [open, api, lifetime]);
  const guide = useCallback((more?: string) => {
    void readRecordingRuleProgrammes(api, day, '', more).then(page => {if (lifetime.current()) {setProgrammes(current => more ? [...current, ...page.items] : page.items); setCursor(page.nextCursor);}}, failure => {if (lifetime.current()) setError(failure);});
  }, [api, day, lifetime]);
  useEffect(() => {if (open) {setProgramme(''); guide();}}, [open, guide]);
  const save = async () => {
    if (busy || !profileId || !programme) return;
    setBusy(true); setError(undefined);
    try {await saveOwnerRecordingRule(api, {profileId, programme, newOnly}); if (lifetime.current()) onSaved();}
    catch (failure) {if (lifetime.current()) setError(failure);}
    finally {if (lifetime.current()) setBusy(false);}
  };
  const days = Array.from({length: 7}, (_, index) => {const date = new Date(Date.now() + index * 86_400_000); return {value: date.toISOString().slice(0, 10), label: defaultI18n.formatDate(date.toISOString(), {weekday: 'short', day: 'numeric', month: 'short'})};});
  return <BottomSheet open={open} onOpenChange={next => {if (!next && !busy) onOpenChange(false);}} title={t('web.recordingRules.newTitle')} scrollable testID="live-new-rule"
    footer={<View style={{flexDirection: 'row', gap: 8}}><Button variant="glass" disabled={busy} onPress={() => onOpenChange(false)}>{t('action.cancel')}</Button>
      <Button testID="live-new-rule-save" loading={busy} disabled={busy || !profileId || !programme} onPress={() => {void save();}}>{t('action.save')}</Button></View>}>
    <View style={{gap: 12, paddingBottom: 8}}>
      {error !== undefined && <Text accessibilityRole="alert" style={[shellType(13, 600), {color: theme.colors.record}]}>{presentError(error, 'server-console', {operation: 'action'}).body}</Text>}
      <Select label={t('web.recordingRules.recordFor')} value={profileId} disabled={busy}
        options={[{value: '', label: t('web.recordingRules.chooseProfile')}, ...owners.map(row => ({value: row.profileId, label: `${row.accountName} · ${row.profileName}`}))]} onValueChange={setProfileId}/>
      <Select label={t('web.recordingRules.guideDate')} value={day} disabled={busy} options={days} onValueChange={setDay}/>
      <Select label={t('web.recordingRules.seriesFromGuide')} value={programme} disabled={busy || !profileId}
        options={[{value: '', label: t('web.recordingRules.chooseProgram')}, ...programmes.map(row => ({value: row.id, label: `${row.title ?? ''} · ${row.channelName} · ${defaultI18n.formatDate(row.startsAt, {hour: 'numeric', minute: '2-digit'})}`}))]} onValueChange={setProgramme}/>
      {cursor && <Button size="compact" variant="glass" disabled={busy} onPress={() => guide(cursor)}>{t('web.live.empty.moreTitle')}</Button>}
      <SettingsRow first label={t('guide.newOnly')} control={<Switch accessibilityLabel={t('guide.newOnly')} checked={newOnly} disabled={busy} onCheckedChange={setNewOnly}/>}/>
    </View>
  </BottomSheet>;
}
