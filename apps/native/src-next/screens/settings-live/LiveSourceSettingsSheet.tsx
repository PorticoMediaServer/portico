import React, {useEffect, useRef, useState} from 'react';
import {Text, View} from 'react-native';
import type {LiveSourceConfig, LiveLogoResult} from '../../../../../packages/contracts/src/next/api';
import type {SourceSettingsDocument} from '../../../../../packages/client-core/src/server-admin/live-source-settings';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';
import type {MessageId} from '../../../../../packages/i18n/src';
import {Button, Select, Switch, TextField} from '../../kit/controls';
import {BottomSheet} from '../../kit/overlays';
import {SettingsRow} from '../../kit/shell';
import {shellType} from '../../kit/shell/visual';
import {useTheme} from '../../kit/theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeLiveOwnerController} from './live-owner-controller';
import {liveDocumentPicker} from './live-document-picker';
import {sourceSettingsDraft as draftOf, sourceSettingsChange, type LiveSource, type SourceSettingsDraft as Draft} from './live-owner-model';

const K = 'next.settingsSpec.server.live.groups.2.rows', N = 'next.settingsSpec.server.live.groups.3.rows.0';

/** A Live TV source's own settings on the rebuilt server (GET/PATCH /v1/admin/live/sources/{id} with its revision), the
 * same rows web's source dialog draws: name, on/off, who may watch, where it comes from, tuners, guide, how often it is
 * read again, which channels, numbering, and channel logos. Only what changed is sent. */
export function SourceSettingsSheet({controller, runtime, source, sources, open, onOpenChange, onSaved}: {controller: NativeLiveOwnerController; runtime: NativeShellRuntime; source?: LiveSource; sources: readonly LiveSource[]; open: boolean; onOpenChange: (open: boolean) => void; onSaved: () => void}) {
  const theme = useTheme(), t = runtime.text.t;
  const [document, setDocument] = useState<SourceSettingsDocument>(), [draft, setDraft] = useState<Draft>(), [error, setError] = useState<unknown>(), [busy, setBusy] = useState(false);
  const [included, setIncluded] = useState<string>(), [choices, setChoices] = useState<readonly {id: string; name: string}[]>([]), [replace, setReplace] = useState(false), [logos, setLogos] = useState<LiveLogoResult>(), [saved, setSaved] = useState(false);
  const opening = useRef<object | undefined>(undefined);
  const id = source?.id;
  useEffect(() => {
    if (!open || !id) return;
    const ticket = {}; opening.current = ticket;
    setDocument(undefined); setDraft(undefined); setError(undefined); setIncluded(undefined); setChoices([]); setLogos(undefined); setSaved(false); setReplace(false);
    void controller.sourceSettings(id).then(value => {if (opening.current === ticket) {setDocument(value); setDraft(draftOf(value));}}, failure => {if (opening.current === ticket) setError(failure);});
    return () => {opening.current = undefined;};
  }, [open, id, controller]);
  const cfg = draft?.config, guideOnly = document?.source.kind === 'guide';
  const mode = included ?? (cfg?.channels?.length ? 'picked' : cfg?.groups?.length ? 'groups' : 'all');
  useEffect(() => {
    if (!id || !open || mode === 'all') {setChoices([]); return;}
    let live = true;
    void controller.sourceChoices(id, mode === 'groups' ? 'groups' : 'channels', '').then(page => {if (live) setChoices(page.items);}, () => {if (live) setChoices([]);});
    return () => {live = false;};
  }, [id, open, mode, controller]);
  const edit = (patch: Partial<Draft>) => {setSaved(false); setDraft(current => current && {...current, ...patch});};
  const config = (patch: Partial<LiveSourceConfig>) => {if (cfg) edit({config: {...cfg, ...patch}});};
  const changed = !!draft && !!document && (JSON.stringify([draft.name, draft.audience, draft.paused, draft.tunerCount, draft.config]) !== JSON.stringify([document.source.name, document.source.audience, document.source.paused, document.source.tunerCount, document.source.config]) || !!draft.username || !!draft.password || !!draft.playlist || !!draft.guide);
  const pair = !(draft?.username || draft?.password) || !!draft?.username && !!draft?.password;
  const save = async () => {
    if (!id || !draft || !document || busy) return;
    const body = sourceSettingsChange(document, draft);
    setBusy(true); setError(undefined);
    try {const next = await controller.saveSourceSettings(id, body, document.etag); const value = {...next, imagesEnabled: document.imagesEnabled}; setDocument(value); setDraft(draftOf(value)); setSaved(true); onSaved();}
    catch (failure) {setError(failure);}
    finally {setBusy(false);}
  };
  const pick = async (field: 'playlist' | 'guide') => {
    if (!liveDocumentPicker || busy) return;
    const picked = await liveDocumentPicker.pick(field, new AbortController().signal).catch(failure => {setError(failure); return undefined;});
    if (picked) edit(field === 'playlist' ? {playlist: picked.data, config: {...cfg!, uploaded: true, playlistUrl: ''}} : {guide: picked.data, config: {...cfg!, guideUploaded: true, guideUrl: ''}});
  };
  const importLogos = async () => {
    if (!id || busy) return;
    setBusy(true); setError(undefined);
    try {setLogos(await controller.importSourceLogos(id, replace));} catch (failure) {setError(failure);} finally {setBusy(false);}
  };
  const guides = sources.filter(value => value.id !== id);
  const toggle = (list: readonly string[] | undefined, value: string) => list?.includes(value) ? list.filter(item => item !== value) : [...(list ?? []), value];
  return <BottomSheet open={open && !!source} onOpenChange={next => {if (!next && !busy) onOpenChange(false);}} title={source?.name} scrollable keyboardAvoiding testID="live-source-settings"
    footer={<View style={{flexDirection: 'row', gap: 8}}><Button variant="glass" disabled={busy} onPress={() => onOpenChange(false)}>{t('action.close')}</Button>
      <Button testID="live-source-settings-save" loading={busy} disabled={!changed || busy || !pair || !draft?.name.trim()} onPress={() => {void save();}}>{t('action.save')}</Button></View>}>
    <View style={{gap: 12, paddingBottom: 8}}>
      {!document && !error && <Text style={[shellType(13), {color: theme.colors.textSecondary}]}>{t('web.librarySettings.loading')}</Text>}
      {error !== undefined && <Text accessibilityRole="alert" style={[shellType(13, 600), {color: theme.colors.record}]}>{presentError(error, 'server-console', {operation: 'action'}).body}</Text>}
      {saved && <Text accessibilityRole="text" style={[shellType(13), {color: theme.colors.healthy}]}>{t('server.live.settingsSaved')}</Text>}
      {draft && document && cfg && <>
        <TextField testID="live-source-name" label={t('profile.name')} value={draft.name} maxLength={120} editable={!busy} onChangeText={name => edit({name})}/>
        <SettingsRow first label={t(`${K}.0.label` as MessageId)} control={<Switch accessibilityLabel={t(`${K}.0.label` as MessageId)} checked={!draft.paused} disabled={busy} onCheckedChange={enabled => edit({paused: !enabled})}/>}/>
        <Select label={t(`${K}.1.label` as MessageId)} value={draft.audience} disabled={busy} options={['everyone', 'owner'].map((value, index) => ({value, label: t(`${K}.1.control.options.${index}.label` as MessageId)}))} onValueChange={audience => edit({audience})}/>
        {document.source.kind === 'playlist' && <>
          <TextField label={t('web.liveTv.playlistUrl')} value={cfg.playlistUrl ?? ''} editable={!busy} autoCapitalize="none" autoCorrect={false} onChangeText={playlistUrl => edit({playlist: undefined, config: {...cfg, playlistUrl, uploaded: false}})}/>
          {liveDocumentPicker && <Button size="compact" variant="glass" disabled={busy} onPress={() => {void pick('playlist');}}>{t('web.liveTv.playlistFile')}</Button>}
        </>}
        {document.source.kind === 'xtream' && <>
          <TextField label={t('web.liveTv.serverAddress')} value={cfg.serverUrl ?? ''} editable={!busy} autoCapitalize="none" autoCorrect={false} onChangeText={serverUrl => config({serverUrl})}/>
          <TextField label={t('web.liveSource.replaceUsername')} value={draft.username} editable={!busy} autoCapitalize="none" autoCorrect={false} secureTextEntry onChangeText={username => edit({username})}/>
          <TextField label={t('web.liveSource.replacePassword')} value={draft.password} editable={!busy} autoCapitalize="none" autoCorrect={false} secureTextEntry onChangeText={password => edit({password})}/>
        </>}
        {document.source.kind === 'hdhomerun' && <TextField label={t('web.liveTv.tunerAddress')} value={cfg.tunerUrl ?? ''} editable={!busy} autoCapitalize="none" autoCorrect={false} onChangeText={tunerUrl => config({tunerUrl})}/>}
        {!guideOnly && (document.source.kind === 'hdhomerun' ? <Text style={[shellType(13), {color: theme.colors.textSecondary}]}>{t('server.live.tunerCount', {count: draft.tunerCount})}</Text>
          : <TextField label={t('web.dvrSettings.tuners')} keyboardType="number-pad" value={String(draft.tunerCount)} editable={!busy} onChangeText={value => {const n = Number(value); if (Number.isInteger(n) && n >= 1 && n <= 256) edit({tunerCount: n});}}/>)}
        {!guideOnly && <Select label={t(`${K}.2.label` as MessageId)} value={cfg.guideSource || (cfg.guideUrl || cfg.guideUploaded ? 'url' : 'own')} disabled={busy}
          options={[{value: 'own', label: t('player.quality.automatic')}, ...guides.map(value => ({value: value.id, label: value.name})), {value: 'url', label: t(`${K}.2.control.options.2.label` as MessageId)}]}
          onValueChange={value => config(value === 'own' ? {guideSource: '', guideUrl: ''} : value === 'url' ? {guideSource: ''} : {guideSource: value, guideUrl: ''})}/>}
        {(guideOnly || !cfg.guideSource && (cfg.guideUrl !== undefined && cfg.guideUrl !== '' || cfg.guideUploaded)) && <>
          <TextField label={t('web.liveTv.guideUrl')} description={t('server.live.guideUrlHelp')} value={cfg.guideUrl ?? ''} editable={!busy} autoCapitalize="none" autoCorrect={false} onChangeText={guideUrl => edit({guide: undefined, config: {...cfg, guideUrl, guideUploaded: false}})}/>
          {liveDocumentPicker && <Button size="compact" variant="glass" disabled={busy} onPress={() => {void pick('guide');}}>{t('web.liveTv.guideFile')}</Button>}
        </>}
        <TextField label={t(`${K}.3.label` as MessageId)} description={t('web.liveSource.refreshHours')} keyboardType="number-pad" value={String(cfg.refreshHours || 6)} editable={!busy} onChangeText={value => {const n = Number(value); if (Number.isInteger(n) && n >= 1 && n <= 168) config({refreshHours: n});}}/>
        {!guideOnly && <>
          <Select label={t(`${N}.label` as MessageId)} value={mode} disabled={busy} options={['all', 'groups', 'picked'].map((value, index) => ({value, label: t(`${N}.control.options.${index}.label` as MessageId)}))}
            onValueChange={value => {setIncluded(value); config({groups: value === 'groups' ? cfg.groups : [], channels: value === 'picked' ? cfg.channels : []});}}/>
          {mode !== 'all' && choices.map(choice => <SettingsRow key={choice.id} first label={choice.name}
            control={<Switch accessibilityLabel={choice.name} disabled={busy} checked={(mode === 'groups' ? cfg.groups : cfg.channels)?.includes(choice.id) === true}
              onCheckedChange={() => config(mode === 'groups' ? {groups: toggle(cfg.groups, choice.id)} : {channels: toggle(cfg.channels, choice.id)})}/>}/>)}
          <Select label={t('web.liveSource.channelNumbers')} value={cfg.numbering || 'source'} disabled={busy} options={[{value: 'source', label: t('web.liveSource.asSent')}, {value: 'sequential', label: t('web.liveSource.inOrder')}]}
            onValueChange={numbering => config({numbering, ...(numbering === 'sequential' ? {firstNumber: cfg.firstNumber || 1} : {})})}/>
          {cfg.numbering === 'sequential' && <TextField label={t('web.liveSource.startAt')} keyboardType="number-pad" value={String(cfg.firstNumber || 1)} editable={!busy} onChangeText={value => {const n = Number(value); if (Number.isInteger(n) && n >= 1 && n <= 99999) config({firstNumber: n});}}/>}
          <SettingsRow first label={t('web.liveSource.replaceLogos')} control={<Switch accessibilityLabel={t('web.liveSource.replaceLogos')} checked={replace} disabled={busy || !document.imagesEnabled} onCheckedChange={setReplace}/>}/>
          <Button size="compact" variant="glass" disabled={busy || changed || !document.imagesEnabled} onPress={() => {void importLogos();}}>{t('web.liveSource.importLogos')}</Button>
          {!document.imagesEnabled && <Text style={[shellType(13), {color: theme.colors.textSecondary}]}>{t('web.liveSource.imagesDisabled')}</Text>}
          {logos && <Text accessibilityRole="text" style={[shellType(13), {color: theme.colors.textSecondary}]}>{t('web.liveSource.logoResult', {imported: logos.imported, skipped: logos.skipped, requested: logos.requested})}</Text>}
        </>}
      </>}
    </View>
  </BottomSheet>;
}
