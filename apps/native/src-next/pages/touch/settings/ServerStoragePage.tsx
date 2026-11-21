import React from 'react';
import {Button, FormError, Select} from '../../../kit/controls';
import {PanelBlock, SectionCard, SettingsPageFrame, StateRow} from '../../../kit/frames';
import {Banner, BottomSheet} from '../../../kit/overlays';
import {OverlayFootRow} from '../../../kit/overlays/surface';
import {SettingsHelp} from '../../../kit/settings/SettingsParts';
import {StorageLocation, StorageSpaceRow} from '../../../kit/settings/StorageParts';
import {SettingsRow} from '../../../kit/shell';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {StorageBackups} from '../../shared/settings/StorageBackups';
import {useCacheConfirmation, useEmptyCache, useStorageKeeping, useStorageSpace} from '../../shared/settings/useServerStorage';

type Frame = Pick<React.ComponentProps<typeof SettingsPageFrame>, 'back' | 'server'>;
type StorageProps = {context: NativeShellBootstrapContext; runtime: NativeShellRuntime};

export function ServerStoragePage({ports, runtime, onOpenPage, retry, ...frame}: Frame & {
  ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; onOpenPage: (page: 'history' | 'troubleshooting') => void; retry?: React.ReactNode;
}) {
  const {t} = runtime.text;
  return <SettingsPageFrame {...frame} title={t('settings.server.storage')}>
    {retry}
    <StorageSpace context={ports.context} runtime={runtime}/>
    <EmptyCache context={ports.context} runtime={runtime}/>
    <StorageBackups ports={ports} runtime={runtime}
      advancedContent={<StorageKeepRows context={ports.context} t={t} onOpenPage={onOpenPage}/>}/>
  </SettingsPageFrame>;
}

function StorageSpace({context, runtime}: StorageProps) {
  const {t} = runtime.text, model = useStorageSpace(context, runtime);
  if (model.state?.phase === 'error') return <SectionCard><StateRow kind="error" icon="i-alert" title={t('error.unavailable')}
    action={{label: t('action.tryAgain'), onPress: model.retry}}/></SectionCard>;
  if (!model.usage) return <SettingsHelp>{t('next.settings.loading')}</SettingsHelp>;
  return <>
    <SectionCard label={t('next.settingsSpec.server.storage.groups.0.rows.0.label')}
      help={t('next.settingsSpec.server.storage.groups.0.rows.0.help')}>
      <StorageLocation path={model.usage.stateDirectory}/>
    </SectionCard>
    {model.usage.onNetwork === true && <SectionCard>
      <Banner placement="inline" variant="notice" tone="warning" icon="i-warn"
        message={t('next.settingsSpec.server.storage.groups.0.rows.1.label')}/>
      <SettingsHelp>{t('next.settingsSpec.server.storage.groups.0.rows.1.help')}</SettingsHelp>
    </SectionCard>}
    <SectionCard label={t('next.settingsSpec.server.storage.groups.0.rows.2.label')} help={t('web.maintenance.storageLede')}>
      <SettingsRow first testID="server-storage-data-usage" label={t('web.maintenance.totalLabel')} value={model.total}/>
      {model.lines.map(line => <StorageSpaceRow key={line.id} {...line}/>)}
    </SectionCard>
  </>;
}

function EmptyCache({context, runtime}: StorageProps) {
  const group = useEmptyCache(context, runtime);
  return group ? <CacheAction group={group} runtime={runtime}/> : null;
}

function CacheAction({group, runtime}: {group: NonNullable<ReturnType<typeof useEmptyCache>>; runtime: NativeShellRuntime}) {
  const {t} = runtime.text, cache = useCacheConfirmation(group), busy = cache.state.phase === 'busy';
  const label = t('next.settingsSpec.server.storage.groups.0.rows.3.control.label');
  return <>
    <PanelBlock><Button testID="server-storage-empty-cache" size="compact" variant="glass"
      disabled={cache.disabled} onPress={cache.open}>{label}</Button></PanelBlock>
    {cache.state.phase === 'scheduled' && <SettingsHelp>{t('web.maintenance.restartingTitle')}</SettingsHelp>}
    {cache.state.phase === 'uncertain' && <PanelBlock><FormError placement="inline">{t('error.unconfirmedChange')}</FormError></PanelBlock>}
    <BottomSheet open={!!cache.held} onOpenChange={open => {if (!open) cache.close();}} title={label}
      fitContent dismissOnBack={!busy} dismissOnGesture={!busy} dismissOnOutsidePress={!busy}
      description={t('next.settingsSpec.server.storage.groups.0.rows.3.control.confirm')}
      footer={<OverlayFootRow layout="halves">
        <Button variant="glass" disabled={busy} onPress={cache.close}>{t('action.cancel')}</Button>
        <Button loading={busy} disabled={cache.disabled} onPress={() => {void cache.submit();}}>{label}</Button>
      </OverlayFootRow>}>
      <SettingsHelp>{t('next.settingsSpec.server.general.groups.3.rows.1.control.confirm')}</SettingsHelp>
    </BottomSheet>
  </>;
}

export function StorageKeepRows({context, t, onOpenPage}: {
  context: NativeShellBootstrapContext; t: NativeShellRuntime['text']['t']; onOpenPage: (page: 'history' | 'troubleshooting') => void;
}) {
  const {notices, busy, failed, notification, messages, options, change} = useStorageKeeping(context, t);
  const label = t(notification.label);
  return <SectionCard>
    {notices && <SettingsRow first label={label} description={notification.help ? t(notification.help) : undefined}
      control={<Select testID="storage.keepNotifications" accessibilityLabel={label} value={String(notices.days)} disabled={busy}
        options={options} onValueChange={next => {void change(next);}}/>}/>}
    <SettingsRow first={!notices} navigates label={t(messages.label)} onPress={() => onOpenPage('troubleshooting')}/>
    {failed && <PanelBlock><FormError placement="inline">{t('error.unavailable')}</FormError></PanelBlock>}
  </SectionCard>;
}
