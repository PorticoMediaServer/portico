import React from 'react';
import {Button, Switch, Tabs} from '../../../kit/controls';
import {PanelBlock, SectionCard, SettingsPageFrame, StateRow} from '../../../kit/frames';
import {OverlayFootRow} from '../../../kit/overlays/surface';
import {IntegrationNotice, IntegrationValue} from '../../../kit/settings/IntegrationParts';
import {SettingsHelp} from '../../../kit/settings/SettingsParts';
import {SettingsRow} from '../../../kit/shell';
import {StateView} from '../../../kit/states';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {ServerApiKeys} from '../../shared/settings/ServerApiKeys';
import {ServerWebhooks} from '../../shared/settings/ServerWebhooks';
import {useAssistantAddress, useIntegrationCompatibility, useServerIntegrations} from '../../shared/settings/useServerIntegrations';

type Ports = {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime};
type Frame = Pick<React.ComponentProps<typeof SettingsPageFrame>, 'back' | 'server'>;

export function ServerIntegrationsPage({ports, runtime, retry, ...frame}: Frame & Ports & {retry?: React.ReactNode}) {
  const integrations = useServerIntegrations({ports, runtime}), {t} = runtime.text;
  const title = t('settings.server.integrations');
  return <SettingsPageFrame {...frame} title={title}>
    {retry}
    <Tabs testID="server-integrations-tabs" value={integrations.tab} onValueChange={integrations.setTab} accessibilityLabel={title}
      items={[{value: 'keys', label: t('web.access.apiKeys')}, {value: 'webhooks', label: t('api.webhooks.title')}, {value: 'assistants', label: t('api.mcp.title')}]}/>
    {integrations.tab === 'keys' && (integrations.keys
      ? <ServerApiKeys group={integrations.keys} runtime={runtime} createRequest={integrations.keyCreateRequest} consumeCreateRequest={integrations.consumeKeyCreate}/>
      : <SectionCard><StateRow kind="loading"/></SectionCard>)}
    {integrations.tab === 'webhooks' && <ServerWebhooks ports={ports} runtime={runtime}/>}
    {integrations.tab === 'assistants' && <AssistantsPanel ports={ports} runtime={runtime}
      openCreateKey={integrations.openCreateKey} isCurrent={integrations.current}/>}
    <IntegrationCompatibility ports={ports} runtime={runtime}/>
  </SettingsPageFrame>;
}

function AssistantsPanel({ports, runtime, openCreateKey, isCurrent}: Ports & {openCreateKey: () => void; isCurrent: () => boolean}) {
  const assistant = useAssistantAddress({ports, runtime, isCurrent}), {t} = runtime.text;
  return <>
    <SectionCard label={t('api.mcp.title')} help={t('api.mcp.lede')}>
      <SettingsRow first label={t('api.mcp.address')} description={t('api.mcp.addressHelp')}/>
    </SectionCard>
    <PanelBlock><IntegrationValue value={assistant.address}/></PanelBlock>
    <PanelBlock><OverlayFootRow layout="halves">
      <Button size="compact" variant="glass" icon="i-copy" onPress={() => {void assistant.copy();}}>{t('web.twoStep.copy')}</Button>
      <Button size="compact" variant="glass" icon="i-link" accessibilityLabel={t('api.mcp.shareAddress')}
        onPress={() => {void assistant.share();}}>{t('next.overlays.share')}</Button>
    </OverlayFootRow></PanelBlock>
    {assistant.copied && <IntegrationNotice text={t('api.credential.copied')}/>}
    {assistant.failed && <IntegrationNotice error text={t('error.unavailable')}/>}
    <SettingsHelp>{t('api.mcp.keyHelp')}</SettingsHelp>
    <PanelBlock><Button size="compact" variant="glass" onPress={() => {if (assistant.current()) openCreateKey();}}>{t('web.access.createKey')}</Button></PanelBlock>
  </>;
}

export function IntegrationCompatibility({ports, runtime}: Ports) {
  const {state, retry, setCompatible} = useIntegrationCompatibility(ports), {t} = runtime.text;
  const on = state.value?.compatibleRoutes, label = t('integrations.compatibility.title');
  return <SectionCard help={t('integrations.compatibility.help')}>
    {on !== undefined && <SettingsRow first label={label} control={<Switch accessibilityLabel={label}
      checked={on} disabled={!!state.saving} onCheckedChange={setCompatible}/>}/>}
    {state.phase === 'loading' && on === undefined && <StateRow kind="loading"/>}
    {state.error !== undefined && <StateView variant="inline" {...runtime.error(state.error, retry)}/>}
  </SectionCard>;
}
