import React from 'react';
import {artworkLimitText, type MetadataAnimeTitles, type MetadataFallbackLanguage, type MetadataRefreshPolicy, type MetadataSettings} from '../../../../../../packages/client-core/src/metadata-settings';
import {Button, Select, Switch, TextField} from '../../../kit/controls';
import {PanelBlock, SectionCard, SettingsPageFrame, StateRow} from '../../../kit/frames';
import {BottomSheet} from '../../../kit/overlays';
import {OverlayFootRow} from '../../../kit/overlays/surface';
import {NativeAdvancedSection} from '../../../kit/settings/AdvancedSection';
import {SettingsRow} from '../../../kit/shell';
import {StateView} from '../../../kit/states';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeMetadataController, MetadataPatch} from '../../../screens/settings-live/server/metadata-controller';
import type {StorageForms} from '../../../screens/settings-live/server/storage/forms';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {useMetadataController, useServerMetadata, useTitleDataset, useTitleDatasetForms} from '../../shared/settings/useServerMetadata';

type Props = {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; onLibraries: () => void};
type Frame = Pick<React.ComponentProps<typeof SettingsPageFrame>, 'back' | 'server'>;

export function ServerMetadataPage({ports, runtime, onLibraries, retry, ...frame}: Props & Frame & {retry?: React.ReactNode}) {
  const owned = useMetadataController(ports), {t} = runtime.text;
  return <SettingsPageFrame {...frame} title={t('settings.server.metadata')}>
    {retry}
    {owned ? <MetadataSettingsBody {...owned} ports={ports} runtime={runtime} onLibraries={onLibraries}/>
      : <SectionCard><StateRow kind="loading"/></SectionCard>}
  </SettingsPageFrame>;
}

function MetadataSettingsBody({ports, runtime, onLibraries, controller, current}: Props & {controller: NativeMetadataController; current: () => boolean}) {
  const {t} = runtime.text, metadata = useServerMetadata(controller, current, runtime);
  const {settings, state, advanced, disabled, row, groupTitle, advancedTitle, options, save, editing, text, subtitles} = metadata;
  const toggle = (id: string, value: boolean | undefined, patch: (value: boolean) => MetadataPatch, first = false) => {
    if (value === undefined) return null;
    const definition = row(id);
    return <SettingsRow key={id} first={first} label={t(definition.label)} description={definition.help ? t(definition.help) : undefined}
      control={<Switch testID={id} accessibilityLabel={t(definition.label)} checked={value} disabled={disabled} onCheckedChange={next => save(patch(next))}/>}/>;
  };
  const select = (id: string, value: string | undefined, patch: (value: string) => MetadataPatch, first = false, help?: string) => {
    if (value === undefined) return null;
    const definition = row(id);
    return <SettingsRow key={id} first={first} label={t(definition.label)} description={help ?? (definition.help ? t(definition.help) : undefined)}
      control={<Select testID={id} accessibilityLabel={t(definition.label)} value={value} disabled={disabled} options={options(definition, value)} onValueChange={next => {
        if (definition.control.options?.some(option => String(option.value) === next)) save(patch(next));
      }}/>} />;
  };
  const credential = (id: string, configured: boolean, first = false) => {
    const definition = row(id), credential = metadata.credentials(id, configured);
    return <SettingsRow key={id} first={first} label={t(definition.label)}
      description={[definition.help ? t(definition.help) : '', credential.status].filter(Boolean)}
      control={<Button variant="text" size="xs" disabled={disabled} onPress={credential.open}>{t('web.metadataLookup.change')}</Button>}/>;
  };
  if (!settings || !subtitles) return state.error !== undefined
    ? <StateView {...runtime.error(state.error, metadata.reload, 'server-console')}/>
    : <SectionCard><StateRow kind="loading"/></SectionCard>;
  return <>
  {state.error !== undefined && <StateView variant="inline" {...runtime.error(state.error, metadata.reload, 'server-console')}/>}
  <SectionCard label={groupTitle(0)}>
    {toggle('metadata.online', settings.onlineLookups, onlineLookups => ({onlineLookups}), true)}
    {toggle('metadata.adult', settings.includeAdult, includeAdult => ({includeAdult}), settings.onlineLookups === undefined)}
    <SettingsRow navigates label={t(row('metadata.perLibrary').label)} description={row('metadata.perLibrary').help ? t(row('metadata.perLibrary').help!) : undefined} onPress={onLibraries}/>
  </SectionCard>
  {(settings.defaultLanguage !== undefined || settings.defaultRegion !== undefined) && <><SectionCard label={groupTitle(1)}>
    {select('metadata.language', settings.defaultLanguage, defaultLanguage => ({defaultLanguage}), true)}
    {select('metadata.region', settings.defaultRegion, defaultRegion => ({defaultRegion}), settings.defaultLanguage === undefined)}
  </SectionCard></>}
  <SectionCard label={groupTitle(2)}>
    {credential('metadata.tmdb', settings.tmdbCredentialConfigured, true)}
    {settings.tvdbCredentialConfigured !== undefined && credential('metadata.tvdb', settings.tvdbCredentialConfigured)}
    <SettingsRow label={t(row('metadata.noKey').label)} description={row('metadata.noKey').help ? t(row('metadata.noKey').help!) : undefined} value={row('metadata.noKey').control.text ? t(row('metadata.noKey').control.text!) : undefined}/>
  </SectionCard>
  {(settings.artworkPreference !== undefined || settings.artworkLanguage !== undefined) && <><SectionCard label={groupTitle(3)}>
    {select('metadata.artworkPrefer', settings.artworkPreference, value => ({artworkPreference: value as MetadataSettings['artworkPreference']}), true)}
    {select('metadata.artworkLanguage', settings.artworkLanguage, value => ({artworkLanguage: value as MetadataSettings['artworkLanguage']}), settings.artworkPreference === undefined)}
  </SectionCard></>}
  <SectionCard label={groupTitle(4)}>
    {credential('metadata.osKey', settings.openSubtitlesKeyConfigured, true)}
    {credential('metadata.osUser', !!settings.openSubtitlesUsername)}
    {credential('metadata.osPass', settings.openSubtitlesPasswordConfigured)}
    <SettingsRow label={t(row('metadata.osStatus').label)} description={subtitles.downloads} value={subtitles.text}/>
    <SettingsRow label={t(row('metadata.osTest').label)} control={<Button variant="glass" size="xs" disabled={disabled} onPress={metadata.checkSubtitles}>{t(row('metadata.osTest').control.label!)}</Button>}/>
  </SectionCard>
  <TitleDatasetGroup ports={ports} runtime={runtime}/>
  {/* Refresh all is an action; stored Advanced settings require a published field. */}
  {(() => {
    const languages = advanced?.fallbackLanguage !== undefined || advanced?.animeTitles !== undefined;
    const artwork = settings.localArtworkLimitBytes !== undefined || settings.artworkMaxEdge !== undefined || advanced?.castPhotos !== undefined;
    const music = advanced?.acoustidKeyConfigured !== undefined;
    return <NativeAdvancedSection testID="server-metadata-advanced" summary={metadata.summary}>
      <SectionCard label={advancedTitle(0)}>
        {select('metadata.refresh', advanced?.refreshPolicy, value => ({refreshPolicy: value as MetadataRefreshPolicy}), true)}
        <SettingsRow first={advanced?.refreshPolicy === undefined} label={t(row('metadata.refreshAll').label)} description={state.refreshStarted ? t('server.metadata.refreshAllRunning') : row('metadata.refreshAll').help ? t(row('metadata.refreshAll').help!) : undefined}
          control={<Button testID="metadata.refreshAll" variant="glass" size="xs" disabled={disabled} onPress={metadata.openRefresh}>{t(row('metadata.refreshAll').control.label!)}</Button>}/>
      </SectionCard>
      {languages && <><SectionCard label={advancedTitle(1)}>
        {select('metadata.fallbackLanguage', advanced?.fallbackLanguage, value => ({fallbackLanguage: value as MetadataFallbackLanguage}), true)}
        {select('metadata.animeTitles', advanced?.animeTitles, value => ({animeTitles: value as MetadataAnimeTitles}), advanced?.fallbackLanguage === undefined)}
      </SectionCard></>}
      {artwork && <><SectionCard label={advancedTitle(2)}>
        {settings.localArtworkLimitBytes !== undefined && select('metadata.artworkLimit', artworkLimitText(settings.localArtworkLimitBytes), value => ({localArtworkLimitBytes: Number(value) * 2 ** 30}), true,
          t('server.metadata.artworkUsedHelp', {used: artworkLimitText(settings.localArtworkUsedBytes ?? 0)}))}
        {settings.artworkMaxEdge !== undefined && select('metadata.artworkSize', settings.artworkMaxEdge === 1280 ? 'small' : settings.artworkMaxEdge === 2560 ? 'large' : 'standard', value => ({artworkMaxEdge: value === 'small' ? 1280 : value === 'large' ? 2560 : 1920}), settings.localArtworkLimitBytes === undefined)}
      </SectionCard></>}
      {music && <><SectionCard label={advancedTitle(3)}>
        {credential('metadata.acoustid', advanced?.acoustidKeyConfigured === true, true)}
      </SectionCard></>}
    </NativeAdvancedSection>;
  })()}
    <BottomSheet open={!!editing} onOpenChange={open => {if (!open) metadata.closeCredential();}}
      title={editing ? editing.removing ? t('action.remove', {label: t(editing.row.label)}) : t(editing.row.label) : ''}
      description={editing?.removing ? t('server.metadata.removeSavedValue') : undefined}
      keyboardAvoiding={!editing?.removing} fitContent={editing?.removing}
      dismissOnBack={!state.busy} dismissOnGesture={!state.busy} dismissOnOutsidePress={!state.busy}
      footer={<OverlayFootRow layout="halves">
        <Button variant="glass" disabled={state.busy} onPress={metadata.closeCredential}>{t(editing?.removing ? 'server.metadata.keepSavedValue' : 'action.cancel')}</Button>
        <Button testID="metadata-credential-commit" variant={editing?.removing ? 'danger' : 'primary'} disabled={disabled || !metadata.credentialReady}
          loading={state.busy} onPress={() => {void metadata.saveCredential();}}>{t(editing?.removing ? 'action.removeShort' : 'action.save')}</Button>
      </OverlayFootRow>}>
      {!editing?.removing && <PanelBlock><TextField testID="metadata-credential-value" label={editing ? t(editing.row.label) : ''} value={text}
        onChangeText={metadata.setText} editable={!disabled} secureTextEntry={editing?.row.control.secret}
        autoCapitalize="none" autoCorrect={false} autoComplete="off" placeholder={editing?.row.control.placeholder ? t(editing.row.control.placeholder) : undefined}
        description={editing?.row.help ? t(editing.row.help) : undefined}/>
        {editing?.canRemove && <Button testID="metadata-credential-remove" variant="text" size="compact" disabled={disabled} onPress={metadata.openRemoval}>{t('action.removeShort')}</Button>}
      </PanelBlock>}
      {state.error !== undefined && <StateView variant="inline" {...runtime.error(state.error, () => {metadata.closeCredential(); metadata.reload();}, 'server-console')}/>}
    </BottomSheet>
    <BottomSheet open={!!metadata.confirmation} onOpenChange={open => {if (!open) metadata.closeRefresh();}}
      title={t(row('metadata.refreshAll').label)} description={t(row('metadata.refreshAll').control.confirm!)}
      fitContent dismissOnBack={!state.busy} dismissOnGesture={!state.busy} dismissOnOutsidePress={!state.busy}
      footer={<OverlayFootRow layout="halves">
        <Button variant="glass" disabled={state.busy} onPress={metadata.closeRefresh}>{t('action.cancel')}</Button>
        <Button testID="metadata.refreshAll.confirm" disabled={disabled} loading={state.busy}
          onPress={() => {void metadata.refreshAll();}}>{t(row('metadata.refreshAll').control.label!)}</Button>
      </OverlayFootRow>}>
      {state.error !== undefined && <StateView variant="inline" {...runtime.error(state.error, metadata.reload, 'server-console')}/>}
    </BottomSheet>
  </>;
}

export function TitleDatasetGroup({ports, runtime}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime}) {
  const group = useTitleDatasetForms(ports, runtime);
  return group ? <Dataset key={group.mount} group={group} runtime={runtime}/> : <SectionCard><StateRow kind="loading"/></SectionCard>;
}

function Dataset({group, runtime}: {group: StorageForms; runtime: NativeShellRuntime}) {
  const {t} = runtime.text, model = useTitleDataset(group), {row, title, state, save} = model;
  if (!row) return null;
  const label = t(row.label);
  return <SectionCard label={title ? t(title) : undefined}>
    {state.draft ? <SettingsRow first label={label} description={row.help ? t(row.help) : undefined}
      control={<Switch testID={row.id} accessibilityLabel={label} checked={model.on} disabled={save.saving} onCheckedChange={model.change}/>}/>
      : state.loading ? <StateRow kind="loading"/>
      : state.unavailable && <StateRow kind="error" icon="i-warn" title={label} reason={t('error.unavailable')}
        action={{label: t('action.tryAgain'), onPress: model.reload}}/>}
    {state.loadError !== undefined && <StateView variant="inline" {...runtime.error(state.loadError, model.reload, 'server-console')}/>}
    {state.saveError !== undefined && <StateView variant="inline" {...runtime.error(state.saveError, model.retry, 'server-console')}/>}
  </SectionCard>;
}
