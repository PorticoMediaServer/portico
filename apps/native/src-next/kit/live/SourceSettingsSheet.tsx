import React from 'react';
import {Text} from 'react-native';
import {Button, Select, Switch, TextField} from '../controls';
import {BottomSheet} from '../overlays';
import {SettingsRow} from '../shell';
import {StorageNote, StorageSheetBody} from '../settings/StorageParts';
import {useTheme} from '../theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeLiveOwnerController} from '../../screens/settings-live/live-owner-controller';
import type {LiveSource} from '../../screens/settings-live/live-owner-model';
import {useSourceSettings} from '../../pages/shared/live/useSourceSettings';

/** A source's settings: name, on, who can watch, where it comes from, tuners, guide, how often it is read again, which
 * channels, numbering and logos; Save sends what changed. */
export function SourceSettingsSheet({controller, runtime, source, sources, open, onOpenChange, onSaved}: {controller: NativeLiveOwnerController; runtime: NativeShellRuntime; source?: LiveSource; sources: readonly LiveSource[]; open: boolean; onOpenChange: (open: boolean) => void; onSaved: () => void}) {
  const theme = useTheme(), view = useSourceSettings({controller, runtime, source, sources, open, onSaved}), {t, document, draft, cfg, busy, guide, include} = view;
  return <BottomSheet open={open && !!source} onOpenChange={next => {if (!next && !busy) onOpenChange(false);}} title={source?.name} scrollable keyboardAvoiding testID="live-source-settings"
    footer={<><Button variant="glass" disabled={busy} onPress={() => onOpenChange(false)}>{t('action.close')}</Button>
      <Button testID="live-source-settings-save" loading={busy} disabled={!view.canSave} onPress={view.save}>{t('action.save')}</Button></>}>
    <StorageSheetBody>
      {!document && view.error === undefined && <StorageNote>{t('web.librarySettings.loading')}</StorageNote>}
      {view.error !== undefined && <StorageNote alert>{view.error}</StorageNote>}
      {view.saved && <Text style={[theme.type.rowHelp, {color: theme.colors.healthy}]}>{t('server.live.settingsSaved')}</Text>}
      {draft && document && cfg && guide && include && <>
        <TextField testID="live-source-name" label={t('profile.name')} value={draft.name} maxLength={120} editable={!busy} onChangeText={name => view.edit({name})}/>
        <SettingsRow first label={view.onLabel} control={<Switch accessibilityLabel={view.onLabel} checked={!draft.paused} disabled={busy} onCheckedChange={enabled => view.edit({paused: !enabled})}/>}/>
        <Select label={view.audience.label} value={draft.audience} disabled={busy} options={view.audience.options} onValueChange={audience => view.edit({audience})}/>
        {document.source.kind === 'playlist' && <>
          <TextField label={t('web.liveTv.playlistUrl')} value={cfg.playlistUrl ?? ''} editable={!busy} autoCapitalize="none" autoCorrect={false} onChangeText={playlistUrl => view.edit({playlist: undefined, config: {...cfg, playlistUrl, uploaded: false}})}/>
          {view.canPick && <Button size="compact" variant="glass" disabled={busy} onPress={() => view.pick('playlist')}>{t('web.liveTv.playlistFile')}</Button>}
        </>}
        {document.source.kind === 'xtream' && <>
          <TextField label={t('web.liveTv.serverAddress')} value={cfg.serverUrl ?? ''} editable={!busy} autoCapitalize="none" autoCorrect={false} onChangeText={serverUrl => view.config({serverUrl})}/>
          <TextField label={t('web.liveSource.replaceUsername')} value={draft.username} editable={!busy} autoCapitalize="none" autoCorrect={false} secureTextEntry onChangeText={username => view.edit({username})}/>
          <TextField label={t('web.liveSource.replacePassword')} value={draft.password} editable={!busy} autoCapitalize="none" autoCorrect={false} secureTextEntry onChangeText={password => view.edit({password})}/>
        </>}
        {document.source.kind === 'hdhomerun' && <TextField label={t('web.liveTv.tunerAddress')} value={cfg.tunerUrl ?? ''} editable={!busy} autoCapitalize="none" autoCorrect={false} onChangeText={tunerUrl => view.config({tunerUrl})}/>}
        {!view.guideOnly && (document.source.kind === 'hdhomerun' ? <StorageNote>{t('server.live.tunerCount', {count: draft.tunerCount})}</StorageNote>
          : <TextField label={t('web.dvrSettings.tuners')} keyboardType="number-pad" value={String(draft.tunerCount)} editable={!busy} onChangeText={view.tunerCount}/>)}
        {!view.guideOnly && <Select label={guide.label} value={guide.value} disabled={busy} options={guide.options} onValueChange={guide.choose}/>}
        {guide.ownAddress && <>
          <TextField label={t('web.liveTv.guideUrl')} description={t('server.live.guideUrlHelp')} value={cfg.guideUrl ?? ''} editable={!busy} autoCapitalize="none" autoCorrect={false} onChangeText={guideUrl => view.edit({guide: undefined, config: {...cfg, guideUrl, guideUploaded: false}})}/>
          {view.canPick && <Button size="compact" variant="glass" disabled={busy} onPress={() => view.pick('guide')}>{t('web.liveTv.guideFile')}</Button>}
        </>}
        <TextField label={view.refresh.label} description={t('web.liveSource.refreshHours')} keyboardType="number-pad" value={String(cfg.refreshHours || 6)} editable={!busy} onChangeText={view.refresh.set}/>
        {!view.guideOnly && <>
          <Select label={include.label} value={include.value} disabled={busy} options={include.options} onValueChange={include.choose}/>
          {include.choices.map(choice => <SettingsRow key={choice.key} first label={choice.name} control={<Switch accessibilityLabel={choice.name} disabled={busy} checked={choice.on} onCheckedChange={choice.flip}/>}/>)}
          <Select label={t('web.liveSource.channelNumbers')} value={cfg.numbering || 'source'} disabled={busy} options={[{value: 'source', label: t('web.liveSource.asSent')}, {value: 'sequential', label: t('web.liveSource.inOrder')}]} onValueChange={view.numbering}/>
          {cfg.numbering === 'sequential' && <TextField label={t('web.liveSource.startAt')} keyboardType="number-pad" value={String(cfg.firstNumber || 1)} editable={!busy} onChangeText={view.firstNumber}/>}
          <SettingsRow first label={t('web.liveSource.replaceLogos')} control={<Switch accessibilityLabel={t('web.liveSource.replaceLogos')} checked={view.replace} disabled={busy || !document.imagesEnabled} onCheckedChange={view.setReplace}/>}/>
          <Button size="compact" variant="glass" disabled={busy || view.changed || !document.imagesEnabled} onPress={view.importLogos}>{t('web.liveSource.importLogos')}</Button>
          {!document.imagesEnabled && <StorageNote>{t('web.liveSource.imagesDisabled')}</StorageNote>}
          {view.logos !== undefined && <StorageNote>{view.logos}</StorageNote>}
        </>}
      </>}
    </StorageSheetBody>
  </BottomSheet>;
}
