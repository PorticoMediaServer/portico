import React from 'react';
import {Button, Select, Switch, Tabs, TextField} from '../../../kit/controls';
import {SectionCard, SettingsPageFrame} from '../../../kit/frames';
import {Dialog, Menu} from '../../../kit/overlays';
import {OverlayFootRow} from '../../../kit/overlays/surface';
import {NativeAdvancedSection} from '../../../kit/settings/AdvancedSection';
import {StorageActions, StorageNote} from '../../../kit/settings/StorageParts';
import {LiveFieldCard, LiveSourceCard, LiveSourcesEmpty, LiveSourcesHeading, LiveSwitchRow} from '../../../kit/live/LiveSourceParts';
import {ServerHeader} from '../../../kit/shell';
import {Quiet, StateView} from '../../../kit/states';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeLiveOwnerController} from '../../../screens/settings-live/live-owner-controller';
import type {NativeRecordingsController} from '../../../screens/settings-live/recordings-controller';
import {liveRewindRow, liveStabilityRow} from '../../../screens/settings-live/live-defaults-presentation';
import {AddSourceSheet, ChannelMapSheet, DiscoverSheet, UploadSourceSheet} from '../../../kit/live/LiveSourceSheets';
import {SourceSettingsSheet} from '../../../kit/live/SourceSettingsSheet';
import {LiveRecordingTab} from './LiveRecordingTab';
import {LibraryChannelsTab} from './LibraryChannelsTab';
import {useLiveDefaults, useLiveOwner, type LiveOwnerTab} from '../../shared/live/useLiveOwner';

/** Settings › Server › Live TV & Channels (live-owner 1, in the settings kit): Back to Settings, the owner's server, and the
 * shared list's three tabs. Sources: Add source, the sources, then the server-wide defaults and limits; Recording; and the
 * library channels. Every read and write is the server's. */
export function LiveOwnerPage({controller, recordings: _recordings, ports, runtime, onClose, initialAction, initialChannelId}: {initialChannelId?: string; initialAction?: 'add' | 'discover';
  controller: NativeLiveOwnerController; recordings: NativeRecordingsController; ports: Pick<NativeSettingsLivePorts, 'context' | 'requestId'> & Partial<NativeSettingsLivePorts>;
  runtime: NativeShellRuntime; onClose: () => void}) {
  const view = useLiveOwner({controller, ports, runtime, initialAction, initialChannelId}), {t, state, shownRemove} = view;
  return <>
    <SettingsPageFrame title={t('settings.server.live')} testID="live-owner" back={{label: t('next.shell.settings'), onPress: onClose, testID: 'live-owner-back'}}
      server={<ServerHeader variant="phone" serverName={ports.context.serverName} viewerRole={view.role} audience="owner" testID="live-owner-server"/>}
      tabs={<Tabs testID="live-owner-tabs" value={view.tab} onValueChange={value => view.setTab(value as LiveOwnerTab)} items={[{value: 'sources', label: t('settings.server.liveSources')},
        {value: 'recording', label: t('settings.server.recording')}, {value: 'channels', label: t('settings.server.libraryChannels')}]}/>}>
      {view.tab === 'sources' ? <>
        <SectionCard plain help={t('web.liveTv.sourcesLede')}>
          <StorageActions onPage><Button ref={view.addButton} testID="live-owner-add" icon="i-plus" onPress={() => view.setAddMenu(true)}>{t('web.liveTv.addSource')}</Button></StorageActions>
        </SectionCard>
        <SectionCard plain>
          <LiveSourcesHeading title={t('web.liveTv.sources')} checked={view.checked}/>
          {state.loading && !state.list && <Quiet><StateView title={t('web.librarySettings.loading')}/></Quiet>}
          {state.error !== undefined && <StateView testID="live-owner-error" {...runtime.error(state.error, () => {void controller.load();})}/>}
          {state.list && !view.sources.length && <LiveSourcesEmpty testID="live-owner-empty" title={t('web.live.empty.noSourcesTitle')} body={t('web.liveTv.noSourcesHelp')}
            action={<Button testID="live-owner-empty-add" icon="i-plus" onPress={() => view.setAddMenu(true)}>{t('web.liveTv.addSource')}</Button>}/>}
          {state.notice && <StorageNote onPage>{state.notice}</StorageNote>}
          {view.rows.map(row => <LiveSourceCard key={row.key} testID={`live-owner-source-${row.key}`} name={row.name} facts={row.facts} tuners={row.tuners} updated={row.updated}
            status={row.status} skipped={row.skipped} onPress={row.open} menuLabel={t('server.row.actions', {name: row.name})} onMenu={row.openMenu} menuRef={row.opener}/>)}
        </SectionCard>
        <LiveDefaults controller={controller} runtime={runtime}/>
      </> : view.tab === 'channels' ? <LibraryChannelsTab ports={ports} runtime={runtime} initialChannelId={initialChannelId}/>
        : <LiveRecordingTab context={ports.context} runtime={runtime}/>}
    </SettingsPageFrame>
    <Menu open={view.menu !== undefined} onOpenChange={value => {if (!value) view.setMenu(undefined);}} title={view.menu?.name} anchorRef={view.menuOpener} presentation="sheet" items={view.menuItems} testID="live-owner-menu"/>
    <Menu open={view.addMenu} onOpenChange={view.setAddMenu} title={t('web.liveTv.addSource')} anchorRef={view.addButton} presentation="sheet" testID="live-owner-add-menu" items={view.addItems}/>
    <AddSourceSheet controller={controller} runtime={runtime} open={view.adding} tuner={view.tuner} onOpenChange={view.setAdding} onSaved={() => {view.setAdding(false); void controller.load(true);}}/>
    <UploadSourceSheet controller={controller} runtime={runtime} open={view.uploading} onOpenChange={view.setUploading} onSaved={() => {view.setUploading(false); void controller.load(true);}}/>
    <DiscoverSheet controller={controller} runtime={runtime} open={view.discovering} onOpenChange={view.setDiscovering} onUse={address => {view.setTuner(address); view.setAdding(true);}}/>
    <SourceSettingsSheet controller={controller} runtime={runtime} source={view.configure} sources={view.sources} open={view.configure !== undefined} onOpenChange={value => {if (!value) view.setConfigure(undefined);}} onSaved={() => void controller.load(true)}/>
    <ChannelMapSheet controller={controller} runtime={runtime} source={view.mapping} open={view.mapping !== undefined} onOpenChange={value => {if (!value) view.setMapping(undefined);}}/>
    <Dialog open={shownRemove !== undefined} onOpenChange={value => {if (!value && !view.removeFlight.current && shownRemove) view.closeRemove(shownRemove);}} title={t('web.liveTv.removeTitle', {name: shownRemove?.source.name ?? ''})}
      description={t('web.liveTv.removeBody')} testID="live-owner-remove" initialFocusRef={view.confirmCancel}
      footer={<OverlayFootRow layout="halves"><Button ref={view.confirmCancel} variant="glass" disabled={view.removing} onPress={() => {if (shownRemove) view.closeRemove(shownRemove);}}>{t('action.cancel')}</Button>
        <Button variant="danger" disabled={view.removing} loading={view.removing} onPress={() => {if (shownRemove) view.removeSource(shownRemove);}}>{t('web.liveTv.removeSource')}</Button></OverlayFootRow>}>
      {null}
    </Dialog>
  </>;
}

function LiveDefaults({controller, runtime}: {controller: NativeLiveOwnerController; runtime: NativeShellRuntime}) {
  const t = runtime.text.t, view = useLiveDefaults(controller), {draft, document} = view;
  return <>
    <SectionCard plain label={t('web.dvrSettings.liveDefaults')}>
      {view.error !== undefined && <StateView testID="live-owner-defaults-error" {...runtime.error(view.error, view.clearError)}/>}
      {!document && view.error === undefined && <StorageNote onPage>{t('web.librarySettings.loading')}</StorageNote>}
      {draft && document && <LiveFieldCard>
        <Select testID="live-owner-defaults-stability" label={t(liveStabilityRow.label)} value={view.stability!}
          options={(liveStabilityRow.control.options ?? []).map(option => ({value: String(option.value), label: t(option.label), description: option.hint ? t(option.hint) : undefined}))}
          onValueChange={view.chooseStability}/>
        {draft.timeshiftMinutes !== undefined && <Select testID="live-owner-defaults-rewind" label={t(liveRewindRow.label)} description={liveRewindRow.help ? t(liveRewindRow.help) : undefined} value={String(draft.timeshiftMinutes)}
          options={(liveRewindRow.control.options ?? []).map(option => ({value: String(option.value), label: t(option.label)}))}
          onValueChange={value => {const minutes = Number(value); if (minutes === 10 || minutes === 30 || minutes === 60 || minutes === 120) view.patch({timeshiftMinutes: minutes});}}/>}
        <Select testID="live-owner-defaults-days" label={t('web.dvrSettings.guideDays')} description={t('web.dvrSettings.guideDaysHelp')} value={String(draft.guideDays)}
          options={view.guideDayChoices.map(days => ({value: String(days), label: t('server.export.days', {count: days})}))} onValueChange={value => view.patch({guideDays: Number(value)})}/>
        <LiveSwitchRow label={t('web.dvrSettings.logoImport')} control={<Switch testID="live-owner-defaults-logos" accessibilityLabel={t('web.dvrSettings.logoImport')} checked={draft.logoImport} onCheckedChange={logoImport => view.patch({logoImport})}/>}/>
      </LiveFieldCard>}
    </SectionCard>
    {draft && document && <>
      <NativeAdvancedSection testID="live-owner-defaults-advanced" open={view.advanced} onOpenChange={view.setAdvanced}>
        <SectionCard plain><LiveFieldCard>
          <TextField testID="live-owner-defaults-buffer" label={t('web.dvrSettings.buffer')} description={t('web.dvrSettings.bufferHelp')} keyboardType="number-pad" value={String(draft.streamBufferMs / 1000)} onChangeText={value => view.patch({streamBufferMs: view.number(value) * 1000})}/>
          <TextField testID="live-owner-defaults-retry" label={t('web.dvrSettings.retryWindow')} description={t('web.dvrSettings.retryWindowHelp')} keyboardType="number-pad" value={String(draft.retryWindowMs / 1000)} onChangeText={value => view.patch({retryWindowMs: view.number(value) * 1000})}/>
          <TextField testID="live-owner-defaults-agent" label={t('web.dvrSettings.userAgent')} description={t('web.dvrSettings.userAgentHelp')} value={draft.userAgent} autoCapitalize="none" autoCorrect={false} onChangeText={userAgent => view.patch({userAgent})}/>
          <LiveSwitchRow label={t('web.dvrSettings.discovery')} help={t('web.dvrSettings.discoveryHelp')} control={<Switch testID="live-owner-defaults-discovery" accessibilityLabel={t('web.dvrSettings.discovery')}
            checked={draft.discoveryEnabled} onCheckedChange={discoveryEnabled => view.patch({discoveryEnabled})}/>}/>
        </LiveFieldCard></SectionCard>
        <SectionCard plain label={t('server.live.limits')}><LiveFieldCard>
          <TextField testID="live-owner-defaults-channels" label={t('server.live.limitChannels')} keyboardType="number-pad" value={String(draft.importLimits.channels)} onChangeText={value => view.patch({importLimits: {...draft.importLimits, channels: view.number(value, 1)}})}/>
          <TextField testID="live-owner-defaults-programmes" label={t('server.live.limitProgrammes')} keyboardType="number-pad" value={String(draft.importLimits.programmes)} onChangeText={value => view.patch({importLimits: {...draft.importLimits, programmes: view.number(value, 1)}})}/>
          <TextField testID="live-owner-defaults-size" label={`${t('server.live.limitSize')} (${t('settings.unit.mb')})`} keyboardType="number-pad" value={String(Math.round(draft.importLimits.bytes / 1048576))}
            onChangeText={value => view.patch({importLimits: {...draft.importLimits, bytes: view.number(value, 1) * 1048576}})}/>
        </LiveFieldCard></SectionCard>
      </NativeAdvancedSection>
      <SectionCard plain><StorageActions onPage>
        <Button testID="live-owner-defaults-save" loading={view.busy} disabled={!view.dirty} onPress={view.save}>{t('action.save')}</Button>
        {view.saved && <StorageNote>{t('server.live.saved')}</StorageNote>}
      </StorageActions></SectionCard>
    </>}
  </>;
}
