import React from 'react';
import type {SettingsRow as SpecRow} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import {Button, FormError, Select, Switch, TextField} from '../../../kit/controls';
import {PanelBlock, SectionCard, SettingsPageFrame, StateRow} from '../../../kit/frames';
import {Banner, BottomSheet} from '../../../kit/overlays';
import {OverlayFootRow} from '../../../kit/overlays/surface';
import {SettingsRow} from '../../../kit/shell';
import {DetailsPanel, StateView} from '../../../kit/states';
import {NativeAdvancedSection} from '../../../kit/settings/AdvancedSection';
import {NativeSettingControl} from '../../../kit/settings/SettingControl';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {useServerRemoteAccess} from './useServerRemoteAccess';

const K = 'next.settingsSpec.server.remote.groups.2.rows.0.children.0.children.0';
const B = 'next.settingsSpec.server.remote.groups.2.rows.1.children.0';
const C = 'next.settingsSpec.server.remote.groups.1.rows.0';
const OWN_FIELDS = new Set(['remote.externalAddress', 'remote.certFile', 'remote.certKey', 'remote.certStatus', 'remote.identityCode']);
const LIST_ROWS = new Set(['remote.homeNets', 'remote.customAddresses', 'remote.trustedProxies']);
type Frame = Pick<React.ComponentProps<typeof SettingsPageFrame>, 'back' | 'server'>;
type Group = ReturnType<typeof useServerRemoteAccess>['main'][number];

export function ServerRemoteAccessPage({ports, runtime, retry, ...frame}: Frame & {
  ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; retry?: React.ReactNode;
}) {
  const remote = useServerRemoteAccess({ports, runtime});
  const {t, when, source, state, status, ownDraft, blocked} = remote;
  const ownAddress = ownDraft && <>
    <SettingsRow first label={t(`${C}.label`)} control={<Switch testID="remote.ownCertificate" accessibilityLabel={t(`${C}.label`)}
      checked={ownDraft.enabled} disabled={blocked} onCheckedChange={enabled => remote.setOwn({...ownDraft, enabled})}/>}/>
    {ownDraft.enabled && <PanelBlock>
      <TextField testID="remote.externalAddress" label={t(`${C}.children.0.label`)} description={t('next.remoteOwner.domainHelp')}
        value={ownDraft.domain} editable={!blocked} autoCapitalize="none" autoCorrect={false} onChangeText={domain => remote.setOwn({...ownDraft, domain})}/>
      <TextField testID="remote.certFile" label={t(`${C}.children.1.label`)} description={t('next.remoteOwner.fileHelp')}
        value={ownDraft.certificateFile} editable={!blocked} autoCapitalize="none" autoCorrect={false} onChangeText={certificateFile => remote.setOwn({...ownDraft, certificateFile})}/>
      <TextField testID="remote.certKey" label={t(`${C}.children.2.label`)} description={t('next.remoteOwner.fileHelp')}
        value={ownDraft.keyFile} editable={!blocked} autoCapitalize="none" autoCorrect={false} onChangeText={keyFile => remote.setOwn({...ownDraft, keyFile})}/>
    </PanelBlock>}
    {status && <DetailsPanel compact audience="owner" facts={[
      {id: 'state', label: t('next.remoteStatus.ownCertificate'), value: status.certificate.problem ?? t(status.certificate.state === 'ok' ? 'next.remoteOwner.serving' : 'next.remoteStatus.none')},
      ...(status.certificate.names ?? []).map(name => ({id: name, label: name})),
      ...(status.certificate.expiresAt ? [{id: 'expires', label: t('next.remoteStatus.expires', {when: when(status.certificate.expiresAt)})}] : []),
      {id: 'identity', label: t(`${C}.children.4.label`), value: status.identityCode},
    ]}/>}
    <Button testID="remote-own-address-save" size="compact" variant="glass" align="stretch" loading={state.busy}
      disabled={blocked || !remote.ownChanged} onPress={remote.saveOwn}>{t('next.remoteStatus.saveOwnAddress')}</Button>
  </>;

  const control = (row: SpecRow): React.ReactNode => {
    switch (row.id) {
      case 'remote.ownCertificate': return ownAddress;
      case 'remote.thisDevice': return status && <SettingsRow first label={t(row.label)}
        value={t(status.thisDevice.atHome ? 'next.remoteStatus.deviceHome' : 'next.remoteStatus.deviceAway', {address: status.thisDevice.address})}/>;
      case 'remote.listen': return status?.listener && <SettingsRow first label={t(row.label)} value={`${status.listener.address}:${status.listener.port}`}/>;
      case 'remote.addresses': return status && <SettingsRow first label={t(row.label)} description={status.addresses.map(address => `${address.address}:${address.port}`).join('\n')}/>;
      case 'remote.blocked': return <SectionCard label={t(row.label)}>{state.blocks.length ? state.blocks.map(block =>
        <SettingsRow key={block.id} first label={block.network} description={t('next.remoteOwner.blockedUntil', {when: when(block.blockedUntil)})}
          control={<Button size="xs" variant="glass" disabled={blocked} onPress={() => {void source.unblock(block);}}>{t(`${B}.control.itemAction.label`)}</Button>}/>)
        : <StateRow kind="empty" icon="i-lock" title={t(`${B}.control.empty`)}/>}</SectionCard>;
      case 'remote.refused': return <SectionCard label={t(row.label)}>{status?.refused.length ? status.refused.map(item => <React.Fragment key={item.address}>
        <SettingsRow first label={item.address} description={t(item.reason === 'http' ? 'next.remoteOwner.refusedHttp' : 'next.remoteOwner.refusedAddress')}/>
        <OverlayFootRow layout="halves">
          {item.fixes.includes('allow') && <Button size="xs" variant="glass" disabled={blocked} onPress={() => {void source.allow(item.address);}}>
            {t(`${K}.control.itemAction.label`)}</Button>}
          {item.fixes.includes('standard') && <Button size="xs" variant="glass" disabled={blocked} onPress={() => remote.change('remote.security', 'standard')}>
            {t(`${K}.control.standardAction.label`)}</Button>}
        </OverlayFootRow>
      </React.Fragment>) : <StateRow kind="empty" icon="i-lock" title={t(`${K}.control.empty`)}/>}</SectionCard>;
    }
    if (OWN_FIELDS.has(row.id) || !source.has(row.id) || source.read(row.id) === undefined || row.enableWhen?.hide && !remote.enabled(row)) return null;
    const current = remote.value(row.id), disabled = blocked || !remote.enabled(row);
    if (LIST_ROWS.has(row.id)) return <PanelBlock>
      <TextField testID={row.id} label={t(row.label)} description={row.help ? t(row.help) : undefined}
        multiline editable={!disabled} autoCapitalize="none" autoCorrect={false} value={Array.isArray(current) ? current.join('\n') : ''}
        onChangeText={value => remote.change(row.id, value.split('\n').map(line => line.trim()).filter(Boolean))}/>
    </PanelBlock>;
    if (row.id === 'remote.secureConnections' && status?.remoteSecurity.active === 'enhanced') return <SettingsRow first label={t(row.label)} value={t('next.remoteOwner.required')}/>;
    if (row.id === 'remote.interface') {
      const interfaces = state.interfaces;
      const options = [{value: 'all', label: t('next.remoteOwner.automatic')}, ...(interfaces?.interfaces ?? []).map(item => ({value: item.name, label: item.name, description: item.addresses.join(', ')}))];
      if (typeof current === 'string' && !options.some(option => option.value === current)) options.push({value: current, label: current});
      return <SettingsRow first label={t(row.label)} description={interfaces?.note ?? (row.help ? t(row.help) : undefined)}
        control={<Select testID={row.id} accessibilityLabel={t(row.label)} value={String(current)} disabled={disabled}
          options={options} onValueChange={next => remote.change(row.id, next)}/>}/>;
    }
    const options = row.id === 'remote.access' ? row.control.options?.filter(option => option.value !== 'private') : row.control.options;
    const shown = {...row, control: {...row.control, ...(row.control.type === 'segmented' ? {type: 'select' as const} : {}), ...(options ? {options} : {})}};
    return <NativeSettingControl row={shown} value={current} t={t} disabled={disabled} onChange={next => remote.change(row.id, next)}/>;
  };
  const renderRow = (row: SpecRow): React.ReactNode => {
    const self = control(row), children = (row.children ?? []).map(renderRow).filter(Boolean);
    return self || children.length ? <React.Fragment key={row.id}>{self}{children}</React.Fragment> : null;
  };
  const renderGroup = (group: Group, index: number) => {
    const rows = group.rows.map(renderRow).filter(Boolean);
    return rows.length ? <SectionCard key={index} label={group.title ? t(group.title) : undefined}>{rows}</SectionCard> : null;
  };

  const error = !remote.loaded && remote.loadError !== undefined ? runtime.error(remote.loadError, remote.load, 'server-console') : undefined;
  const failure = error && {icon: error.icon ?? 'i-alert' as const, tone: error.tone,
    title: typeof error.title === 'string' ? error.title : t('action.tryAgain'), body: typeof error.body === 'string' ? error.body : undefined,
    primaryAction: error.primaryAction, secondaryAction: error.secondaryAction, help: error.help, testID: error.testID};
  return <>
    <SettingsPageFrame {...frame} title={t('settings.server.remote')} testID="server-remote-access" state={failure}>
      {retry}
      {remote.loaded ? <>
        <SectionCard>
          <Banner testID="remote-access-status" placement="inline" variant="notice" tone={remote.outsideTone}
            icon={remote.outsideTone === 'success' ? 'i-check' : remote.outsideTone === 'warning' ? 'i-warn' : 'i-info'} message={remote.outsideTitle}/>
          {remote.outsideFacts.map((label, index) => <SettingsRow key={index} first={index === 0} label={label}/>)}
          <Button testID="remote-access-check" size="compact" variant="glass" align="stretch" loading={state.checking}
            disabled={!status || blocked} onPress={() => {void source.check();}}>{t('next.settingsSpec.server.general.groups.1.rows.2.control.label')}</Button>
        </SectionCard>
        {remote.notes.length > 0 && <SectionCard>{remote.notes.map((message, index) => <Banner key={index}
          testID="remote-access-warning" placement="inline" variant="notice" tone="warning" icon="i-warn" message={message}/>)}</SectionCard>}
        {remote.main.map(renderGroup)}
        {remote.extra.length > 0 && <NativeAdvancedSection testID="remote-access-advanced" open={remote.advanced} onOpenChange={remote.setAdvanced}>
          {remote.extra.map(renderGroup)}
        </NativeAdvancedSection>}
        {remote.actionError && <PanelBlock><FormError placement="inline">{remote.actionError}</FormError></PanelBlock>}
        {remote.hasDrafts && <SectionCard><OverlayFootRow layout="halves">
          <Button testID="remote-access-discard" variant="glass" disabled={remote.saving} onPress={remote.discard}>{t('action.discard')}</Button>
          <Button testID="remote-access-save" loading={remote.saving} disabled={!!state.confirmation} onPress={remote.save}>{t('action.save')}</Button>
        </OverlayFootRow></SectionCard>}
        {remote.saveError !== undefined && <StateView variant="inline" {...runtime.error(remote.saveError, remote.save, 'server-console')}/>}
      </> : <StateView title={t('status.loading')}/>}
    </SettingsPageFrame>
    <BottomSheet open={!!state.confirmation} onOpenChange={open => {if (!open && !state.busy) remote.cancelConfirmation();}}
      testID="remote-access-confirm" title={t('next.remoteOwner.confirm')} description={state.confirmation?.message} fitContent
      dismissOnBack={!state.busy} dismissOnGesture={!state.busy} dismissOnOutsidePress={!state.busy}
      footer={<>
        <Button variant="glass" disabled={state.busy} onPress={remote.cancelConfirmation}>{t('action.cancel')}</Button>
        <Button variant="danger" loading={state.busy} onPress={() => {void remote.confirm();}}>{t('next.remoteOwner.saveAnyway')}</Button>
      </>}>
      {state.confirmation && state.error !== undefined && <StateView variant="inline" {...runtime.error(state.error, remote.confirm, 'server-console')}/>}
    </BottomSheet>
  </>;
}
