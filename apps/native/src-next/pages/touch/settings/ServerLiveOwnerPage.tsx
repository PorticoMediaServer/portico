import React from 'react';
import {Button, Tabs} from '../../../kit/controls';
import {SettingsPageFrame} from '../../../kit/frames';
import {Dialog, Menu} from '../../../kit/overlays';
import {OverlayFootRow} from '../../../kit/overlays/surface';
import {LiveDefaultsSection} from '../../../kit/settings/LiveDefaultsSection';
import {LiveSourcesSection} from '../../../kit/settings/LiveSourcesSection';
import {ServerHeader} from '../../../kit/shell';
import {AddSourceSheet, DiscoverSheet} from '../../../screens/settings-live/LiveOwnerSheets';
import {ChannelMapSheet} from '../../../screens/settings-live/LiveOwnerSettingsSheets';
import {UploadSourceSheet} from '../../../screens/settings-live/LiveOwnerUploadSheet';
import {NativeRecordingOwner} from '../../../screens/settings-live/LiveRecordingOwner';
import {SourceSettingsSheet} from '../../../screens/settings-live/LiveSourceSettingsSheet';
import {liveDocumentPicker} from '../../../screens/settings-live/live-document-picker';
import {NativeLibraryChannelsPanel} from '../../../screens/settings-live/server/LibraryChannelsPanel';
import {useLiveOwnerPage} from '../../shared/settings/useLiveOwnerPage';

type Props = Parameters<typeof useLiveOwnerPage>[0] & {
  onClose: () => void; backLabel?: string; retry?: React.ReactNode;
};

export function ServerLiveOwnerPage({onClose, backLabel, retry, ...props}: Props) {
  const {controller, ports, runtime, initialChannelId} = props;
  const model = useLiveOwnerPage(props);
  const {t, tab, setTab, role, sources, addMenu, setAddMenu, adding, setAdding, discovering, setDiscovering,
    uploading, setUploading, tuner, setTuner, addButton, menu, setMenu, configure, setConfigure, mapping, setMapping,
    shownRemove, closeRemove, removing, removeSource, menuItems, menuOpener, confirmCancel} = model;
  const tabs = [
    {value: 'sources', label: t('settings.server.liveSources')},
    {value: 'recording', label: t('settings.server.recording')},
    {value: 'channels', label: t('settings.server.libraryChannels')},
  ];
  return <>
    <SettingsPageFrame title={t('settings.server.live')} testID="live-owner"
      back={{label: backLabel ?? t('next.shell.settings'), onPress: onClose, testID: 'live-owner-back'}}
      server={<ServerHeader variant="phone" serverName={ports.context.serverName} viewerRole={role} audience="owner" testID="live-owner-server"/>}
      tabs={<Tabs testID="live-owner-tabs" value={tab} onValueChange={setTab} items={tabs} accessibilityLabel={t('settings.server.live')}/>}>
      {retry}
      {tab === 'sources' && <>
        <LiveSourcesSection model={model} controller={controller} runtime={runtime}/>
        <LiveDefaultsSection controller={controller} runtime={runtime}/>
      </>}
      {tab === 'channels' && <NativeLibraryChannelsPanel ports={ports} runtime={runtime} initialChannelId={initialChannelId}/>}
      {tab === 'recording' && <NativeRecordingOwner context={ports.context} runtime={runtime}/>}
    </SettingsPageFrame>
    <Menu open={menu !== undefined} onOpenChange={value => {if (!value) setMenu(undefined);}}
      title={menu?.name} anchorRef={menuOpener} presentation="sheet" items={menuItems} testID="live-owner-menu"/>
    <Menu open={addMenu} onOpenChange={setAddMenu} title={t('web.liveTv.addSource')} anchorRef={addButton} presentation="sheet" testID="live-owner-add-menu" items={[
      {id: 'remote', icon: 'i-live', label: t('web.liveTv.remoteSource'), description: t('web.liveTv.remoteSourceMeta'), onSelect: () => {setTuner(undefined); setAdding(true);}},
      {id: 'discover', icon: 'i-search', label: t('web.liveTv.discover'), description: t('web.liveTv.discoverMeta'), onSelect: () => setDiscovering(true)},
      ...(liveDocumentPicker ? [{id: 'upload', icon: 'i-upload' as const, label: t('web.liveTv.upload'), description: t('web.liveTv.uploadMeta'), onSelect: () => setUploading(true)}] : []),
    ]}/>
    <AddSourceSheet controller={controller} runtime={runtime} open={adding} tuner={tuner} onOpenChange={setAdding}
      onSaved={() => {setAdding(false); void controller.load(true);}}/>
    <UploadSourceSheet controller={controller} runtime={runtime} open={uploading} onOpenChange={setUploading}
      onSaved={() => {setUploading(false); void controller.load(true);}}/>
    <DiscoverSheet controller={controller} runtime={runtime} open={discovering} onOpenChange={setDiscovering}
      onUse={address => {setTuner(address); setAdding(true);}}/>
    <SourceSettingsSheet controller={controller} runtime={runtime} source={configure} sources={sources} open={configure !== undefined}
      onOpenChange={value => {if (!value) setConfigure(undefined);}} onSaved={() => void controller.load(true)}/>
    <ChannelMapSheet controller={controller} runtime={runtime} source={mapping} open={mapping !== undefined}
      onOpenChange={value => {if (!value) setMapping(undefined);}}/>
    <Dialog open={shownRemove !== undefined} onOpenChange={value => {if (!value && shownRemove) closeRemove(shownRemove);}}
      title={t('web.liveTv.removeTitle', {name: shownRemove?.source.name ?? ''})} description={t('web.liveTv.removeBody')}
      testID="live-owner-remove" initialFocusRef={confirmCancel} children={null} footer={<OverlayFootRow layout="end">
        <Button ref={confirmCancel} variant="glass" disabled={removing} onPress={() => {if (shownRemove) closeRemove(shownRemove);}}>{t('action.cancel')}</Button>
        <Button variant="danger" disabled={removing} loading={removing} onPress={() => {if (shownRemove) void removeSource(shownRemove);}}>{t('web.liveTv.removeSource')}</Button>
      </OverlayFootRow>}/>
  </>;
}
