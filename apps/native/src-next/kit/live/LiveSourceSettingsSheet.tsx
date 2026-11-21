import React from 'react';
import {Text, View} from 'react-native';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';
import type {MessageId} from '../../../../../packages/i18n/src';
import {Button, Select, Switch, TextField} from '../controls';
import {BottomSheet} from '../overlays';
import {SettingsRow} from '../shell';
import {useTheme} from '../theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeLiveOwnerController} from '../../screens/settings-live/live-owner-controller';
import {liveDocumentPicker} from '../../screens/settings-live/live-document-picker';
import type {LiveSource} from '../../screens/settings-live/live-owner-model';
import {useLiveSourceSettings} from '../../pages/shared/settings/useLiveSourceSettings';
import {PanelBlock} from '../frames';

const K = 'next.settingsSpec.server.live.groups.2.rows', N = 'next.settingsSpec.server.live.groups.3.rows.0';

export function SourceSettingsSheet({controller, runtime, source, sources, open, onOpenChange, onSaved}: {controller: NativeLiveOwnerController; runtime: NativeShellRuntime; source?: LiveSource; sources: readonly LiveSource[]; open: boolean; onOpenChange: (open: boolean) => void; onSaved: () => void}) {
  const theme = useTheme();
  const {t, document, error, saved, busy, changed, pair, draft, cfg, guideOnly, guides, mode, choices, logos, replace, setReplace, setIncluded, edit, config, pick, importLogos, save, toggle} = useLiveSourceSettings({controller, runtime, source, sources, open, onOpenChange, onSaved});
  return <BottomSheet open={open && !!source} onOpenChange={next => {if (!next && !busy) onOpenChange(false);}} title={source?.name} scrollable keyboardAvoiding testID="live-source-settings"
    footer={<View style={{flexDirection: 'row', gap: 8}}><Button variant="glass" disabled={busy} onPress={() => onOpenChange(false)}>{t('action.close')}</Button>
      <Button testID="live-source-settings-save" loading={busy} disabled={!changed || busy || !pair || !draft?.name.trim()} onPress={() => {void save();}}>{t('action.save')}</Button></View>}>
    <PanelBlock inset={false}>
      {!document && !error && <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{t('web.librarySettings.loading')}</Text>}
      {error !== undefined && <Text accessibilityRole="alert" style={[theme.type.rowHelp, {color: theme.colors.record}]}>{presentError(error, 'server-console', {operation: 'action'}).body}</Text>}
      {saved && <Text accessibilityRole="text" style={[theme.type.rowHelp, {color: theme.colors.healthy}]}>{t('server.live.settingsSaved')}</Text>}
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
        {!guideOnly && (document.source.kind === 'hdhomerun' ? <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{t('server.live.tunerCount', {count: draft.tunerCount})}</Text>
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
          {!document.imagesEnabled && <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{t('web.liveSource.imagesDisabled')}</Text>}
          {logos && <Text accessibilityRole="text" style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{t('web.liveSource.logoResult', {imported: logos.imported, skipped: logos.skipped, requested: logos.requested})}</Text>}
        </>}
      </>}
    </PanelBlock>
  </BottomSheet>;
}
