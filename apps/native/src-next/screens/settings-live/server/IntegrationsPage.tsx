import {NativeIntegrationAdvanced} from './integrations/Advanced';
import React, {useCallback, useLayoutEffect, useMemo, useRef, useState} from 'react';
import {ScrollView, Share, Text, View} from 'react-native';
import {assistantAddress} from '../../../../../../packages/client-core/src/server-admin/api-words';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button} from '../../../kit/controls/Button';
import {Tabs} from '../../../kit/controls/Tabs';
import {ServerHeader} from '../../../kit/shell/ServerIdentity';
import {alpha, shellType} from '../../../kit/shell/visual';
import {useTheme} from '../../../kit/theme';
import type {NativeSettingsLivePorts} from '../ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {copyText} from '../../../platform/clipboard';
import {NativeWebhooks} from './integrations/Webhooks';
import {createNativeApiKeys, NativeApiKeys} from './integrations/ApiKeys';
import {settingsPageHeading, settingsPageInset} from './page-layout';

type ViewerRole = 'owner' | 'administrator' | 'member';
type Translate = typeof defaultI18n.t;

/** Settings › Server › Integrations on the phone (owner-server.html frames 38–39). */
export function NativeServerIntegrations({ports, runtime, compactHeader = false}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; compactHeader?: boolean}) {
  const theme = useTheme(), t = runtime.text.t as Translate, context = ports.context;
  const viewerRole: ViewerRole = context.account.account.role === 'owner' ? 'owner' : context.account.canManage ? 'administrator' : 'member';
  const [tab, setTab] = useState('keys');
  const [keys, setKeys] = useState<ReturnType<typeof createNativeApiKeys>>(), [keyCreateRequest, setKeyCreateRequest] = useState(0);
  const keyRequest = useRef(0), keySequence = useRef(0), operationId = ports.requestId, reportError = runtime.reportError;
  useLayoutEffect(() => {const next = createNativeApiKeys(context, operationId, reportError);setKeys(next);return () => {keyRequest.current = 0;next.dispose();};}, [context, operationId, reportError]);
  const consumeKeyCreate = useCallback((request: number) => {if (!keys || keys.context !== context || !keys.current() || keyRequest.current !== request) return false;keyRequest.current = 0;setKeyCreateRequest(0);return true;}, [keys, context]);
  const openCreateKey = () => {if (!keys || keys.context !== context || !keys.current()) return;const request = ++keySequence.current;keyRequest.current = request;setKeyCreateRequest(request);setTab('keys');};
  return <View style={{flex: 1}}>
    <ScrollView contentContainerStyle={{...settingsPageInset(theme, compactHeader)}}>
      <ServerHeader serverName={context.serverName} viewerRole={viewerRole} audience={viewerRole === 'administrator' ? 'administrators' : 'owner'} variant="phone" style={{marginBottom: 12}}/>
      {!compactHeader && <Text accessibilityRole="header" style={[settingsPageHeading(theme), {color: theme.colors.text}]}>{t('settings.server.integrations')}</Text>}
      <Tabs testID="server-integrations-tabs" style={{marginTop: 12, marginHorizontal: -20}} value={tab} onValueChange={setTab} accessibilityLabel={t('settings.server.integrations')}
        items={[{value: 'keys', label: t('web.access.apiKeys')}, {value: 'webhooks', label: t('api.webhooks.title')}, {value: 'assistants', label: t('api.mcp.title')}]}/>
      {tab === 'keys' && keys?.context === context && keys.current() && <NativeApiKeys group={keys} runtime={runtime} createRequest={keyCreateRequest} consumeCreateRequest={consumeKeyCreate}/>}
      {tab === 'webhooks' && <NativeWebhooks ports={ports} runtime={runtime}/>}
      {tab === 'assistants' && <AssistantsTab ports={ports} t={t} openCreateKey={openCreateKey} isCurrent={() => keys?.context === context && keys.current()}/>}
      <NativeIntegrationAdvanced ports={ports} runtime={runtime}/>
    </ScrollView>
  </View>;
}

function AssistantsTab({ports, t, openCreateKey, isCurrent}: {ports: NativeSettingsLivePorts; t: Translate; openCreateKey: () => void; isCurrent: () => boolean}) {
  const theme = useTheme(), context = ports.context;
  const address = assistantAddress(context.api.baseUrl), [copied, setCopied] = useState(false), [failed, setFailed] = useState(false);
  const scope = useMemo(() => ({context, address, active: true}), [context, address]);
  useLayoutEffect(() => {scope.active = true;setCopied(false);setFailed(false);return () => {scope.active = false;};}, [scope]);
  const current = () => scope.active && scope.context === context && scope.address === address && context.current() && isCurrent();
  const copy = async () => {if (!current()) return;try {await copyText(address);if (current()) {setCopied(true);setFailed(false);}} catch {if (current()) setFailed(true);}};
  const share = async () => {if (!current()) return;try {await Share.share({title:t('api.mcp.title'),message:address});} catch {if (current()) setFailed(true);}};
  return <View style={{marginTop: 14}} testID="server-integrations-assistants">
    <Text style={[shellType(17, 800), {color: theme.colors.text}]}>{t('api.mcp.title')}</Text>
    <Text style={[shellType(13, 400, 18), {color: theme.colors.textSecondary, marginTop: 4}]}>{t('api.mcp.lede')}</Text>
    <Text style={[shellType(14, 700), {color: theme.colors.text, marginTop: 16}]}>{t('api.mcp.address')}</Text>
    <Text style={[shellType(12.5, 400, 17), {color: theme.colors.textTertiary, marginTop: 2}]}>{t('api.mcp.addressHelp')}</Text>
    <View style={{marginTop: 8, borderRadius: 12, padding: 12, backgroundColor: alpha(theme.colors.ink, .55), borderWidth: 1, borderColor: theme.colors.lineSoft}}>
      <Text selectable style={[shellType(13, 500, 18), {color: theme.colors.text, fontFamily: 'monospace'}]}>{address}</Text>
    </View>
    <View style={{flexDirection: 'row', gap: 8, marginTop: 10}}><Button size="compact" variant="glass" icon="i-copy" onPress={() => {void copy();}}>{t('web.twoStep.copy')}</Button><Button size="compact" variant="glass" icon="i-link" onPress={() => {void share();}}>{t('api.mcp.shareAddress')}</Button></View>{copied && <Text style={[shellType(13,500),{color:theme.colors.accentStrong}]}>{t('api.credential.copied')}</Text>}{failed && <Text accessibilityRole="alert" style={[shellType(13,500),{color:theme.colors.record}]}>{t('error.unavailable')}</Text>}
    <Text style={[shellType(12.5, 400, 17), {color: theme.colors.textSecondary, marginTop: 14}]}>{t('api.mcp.keyHelp')}</Text><Button size="compact" variant="glass" onPress={() => {if (current()) openCreateKey();}}>{t('web.access.createKey')}</Button>
  </View>;
}
