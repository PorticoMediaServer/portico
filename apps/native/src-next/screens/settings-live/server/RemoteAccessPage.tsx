import React, {useEffect, useRef, useState} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {remoteWarningLines, type RemoteAccess, type RemoteConfiguration} from '../../../../../../packages/client-core/src/remote-access';
import type {ConnectivityStatus} from '../../../../../../packages/client-core/src/server-administration';
import {secureConnectionView} from '../../../../../../packages/client-core/src/server-admin/secure-connection';
import {ownCertificateLine, remoteStatusLine} from '../../../../../../packages/client-core/src/server-admin/status-words';
import type {CertificateDraft, ConnectivityDraft} from '../../../../../../packages/client-core/src/server-admin/server-forms';
import {certificateProblemMessage, retryCertificate, type CertificateApi, type CertificateStatus} from '../../../../../../packages/client-core/src/certificates';
import {routePath} from '../../../../../../packages/contracts/src/routes';
import {useSettingsRequestScope} from '../use-settings-request-scope';
import {settingsSpec} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import {readSettingsBinding, settingsBinding} from '../../../../../../packages/client-core/src/presentation/settings-bindings';
import {settingsSummary} from './settings-values';
import {NativeAdvancedSection} from './AdvancedSection';
import {Button, Select, Switch, TextField} from '../../../kit/controls';
import {SettingsRow} from '../../../kit/shell';
import {ServerHeader} from '../../../kit/shell/ServerIdentity';
import {shellType} from '../../../kit/shell/visual';
import {StateView} from '../../../kit/states';
import {useTheme} from '../../../kit/theme';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../ports';
import {useFormState, useStorageForms, useStorageSet, type StorageForms} from './storage/forms';
import {settingsPageHeading, settingsPageInset} from './page-layout';

const IDS = ['remote', 'connectivity', 'certificate'] as const;

/** Settings › Server › Remote access on the phone and tablet. Every status and field
 * comes from the owner's real remote/connectivity/certificate forms; the server's own
 * remote status words decide the health line, and a missing read is never shown as off. */
export function NativeRemoteAccessPage({ports, runtime, compactHeader = false}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; compactHeader?: boolean}) {
  const group = useStorageForms(ports, IDS, runtime.reportError);
  return group ? <RemoteAccessPage key={group.mount} group={group} ports={ports} runtime={runtime} compactHeader={compactHeader}/> : null;
}

function RemoteAccessPage({group, ports, runtime, compactHeader}: {group: StorageForms; ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; compactHeader: boolean}) {
  const theme = useTheme(), t = runtime.text.t, context = ports.context;
  const {forms, set} = group, state = useStorageSet(group);
  const remoteState = useFormState<RemoteConfiguration, RemoteAccess>(forms.remote);
  const connectivityState = useFormState<ConnectivityDraft, ConnectivityStatus>(forms.connectivity);
  const certificateState = useFormState<CertificateDraft, CertificateStatus>(forms.certificate);
  const {api, lifetime} = useSettingsRequestScope(context);
  const retryFlight = useRef<AbortController | undefined>(undefined);
  const [retrying, setRetrying] = useState(false), [retryError, setRetryError] = useState<unknown>();
  useEffect(() => () => {retryFlight.current?.abort(); retryFlight.current = undefined;}, [api]);
  const retry = async () => {
    if (!lifetime.current() || retryFlight.current || state.saving || state.dirty || !context.account.account.hostedAccountId || !certificateState.draft?.claimed || !certificateState.draft.enabled) return;
    const request = new AbortController(); retryFlight.current = request; setRetrying(true); setRetryError(undefined);
    const certificateApi: CertificateApi = {requestCertificate: (action, body, signal) => api.request(routePath(action === 'status' ? 'get_networking_certificate' : action === 'config' ? 'post_networking_certificate_config' : 'post_networking_certificate_retry'), action === 'status' ? 'GET' : 'POST', body, signal)};
    try {await retryCertificate(certificateApi, request.signal); if (lifetime.current() && !request.signal.aborted) await forms.certificate.load();}
    catch (error) {if (lifetime.current() && !request.signal.aborted) setRetryError(error);}
    finally {if (retryFlight.current === request) {retryFlight.current = undefined; if (lifetime.current()) setRetrying(false);}}
  };
  const remote = remoteState.draft, connectivity = connectivityState.draft;
  const certificate = certificateState.status;
  const failed = connectivityState.loadError;
  if (failed !== undefined && !connectivity) return <StateView {...runtime.error(failed, () => {void forms.remote.load(); void forms.connectivity.load(); void forms.certificate.load();}, 'server-console')}/>;
  if (!connectivity) {
    if (connectivityState.unavailable) return <StateView title={t('settings.server.remote.notSetUp')}/>;
    return <StateView title={t('status.loading')}/>;
  }
  const remoteLine = remoteStatusLine(t, {unavailable: remoteState.status === undefined, remote: remoteState.status, connectivity: connectivityState.status, certificate});
  const secure = secureConnectionView({certificate});
  const problemMessage = certificateProblemMessage(certificate), certificateProblem = problemMessage !== undefined;
  const own = ownCertificateLine(t, connectivityState.status?.tls.customCertificate, value => new Date(value).toLocaleString());
  const services = !!context.account.account.hostedAccountId && !!certificateState.draft?.claimed;
  const secureBody = certificateState.loadError !== undefined || certificateState.loading ? undefined
    : problemMessage ? t(problemMessage) : certificate ? t(secure.body, secure.address ? {address: secure.address} : undefined)
      : certificateState.unavailable ? t('web.connectivity.customCertNotInUse') : undefined;
  const role = context.account.account.role === 'owner' ? 'owner' : 'administrator';
  const signInOptions = [
    {value: 'allow', label: t('settings.server.signIn.allow')},
    {value: 'owner-only', label: t('settings.server.signIn.owner-only')},
    {value: 'off', label: t('settings.server.signIn.off')},
  ];
  const lines = (value: readonly string[]) => value.join('\n');
  const parseLines = (value: string) => value.split('\n').map(line => line.trim()).filter(Boolean);
  return <View style={{flex: 1}}>
    <ScrollView contentContainerStyle={{...settingsPageInset(theme, compactHeader)}}>
      <ServerHeader serverName={context.serverName} viewerRole={role} audience={role === 'administrator' ? 'administrators' : 'owner'} variant="phone" style={{marginBottom: 12}}/>
      {!compactHeader && <Text accessibilityRole="header" style={[settingsPageHeading(theme), {color: theme.colors.text}]}>{t('settings.server.remote')}</Text>}
      <View testID="remote-access-status" style={{borderRadius: 14, backgroundColor: theme.colors.raised, padding: 16, marginTop: 14, flexDirection: 'row', gap: 12}}>
        <View style={{width: 9, height: 9, borderRadius: 5, marginTop: 5, backgroundColor: remoteLine.tone === 'healthy' ? theme.colors.healthy : remoteLine.tone === 'warning' ? theme.colors.amber : theme.colors.textTertiary}}/>
        <View style={{flex: 1}}>
          <Text style={[shellType(15, 700), {color: theme.colors.text}]}>{remoteLine.text || t('settings.server.remote.notSetUp')}</Text>
          {secureBody ? <Text style={[shellType(12.5, 400, 17), {color: theme.colors.textSecondary, marginTop: 4}]}>{secureBody}</Text> : null}
          {remoteState.status ? remoteWarningLines(remoteState.status, t).map((line, index) => <Text key={index} testID="remote-access-warning" style={[shellType(12.5, 400, 17), {color: theme.colors.amber, marginTop: 4}]}>{line}</Text>) : null}
        </View>
      </View>

      {connectivity ? <>
        {services && certificateState.draft && <>
          <Text style={[shellType(13, 700), {color: theme.colors.textTertiary, marginTop: 22, marginBottom: 8}]}>{t('next.settingsSpec.server.remote.groups.0.title')}</Text>
          <View style={{borderRadius: 14, overflow: 'hidden', backgroundColor: theme.colors.raised}}>
            <SettingsRow first label={t('next.settingsSpec.server.remote.groups.0.rows.0.label')} control={<Switch testID="remote-services-enabled" accessibilityLabel={t('next.settingsSpec.server.remote.groups.0.rows.0.label')} checked={certificateState.draft.enabled} disabled={state.saving || retrying} onCheckedChange={enabled => forms.certificate.set(current => ({...current, enabled}))}/>}/>
            <SettingsRow label={t('next.settingsSpec.server.remote.groups.0.rows.0.children.0.label')} control={<Text style={[shellType(13), {color: certificateProblem || secure.tone === 'warning' ? theme.colors.amber : secure.tone === 'healthy' ? theme.colors.healthy : theme.colors.textSecondary, flexShrink: 1}]}>{secureBody ?? t('status.loading')}</Text>}/>
            {certificate?.routeUrl && <SettingsRow label={t('next.settingsSpec.server.remote.groups.0.rows.0.children.1.label')} control={<Text selectable style={{color: theme.colors.textSecondary, fontFamily: 'monospace', flexShrink: 1}}>{certificate.routeUrl}</Text>}/>}
            {certificate?.notAfter && <SettingsRow label={t('next.settingsSpec.server.remote.groups.0.rows.0.children.2.label')} value={new Date(certificate.notAfter).toLocaleString()}/>}
            {certificateProblem && <Button testID="remote-services-retry" variant="glass" size="compact" disabled={state.saving || state.dirty || !certificateState.draft.enabled} loading={retrying} onPress={() => {void retry();}}>{t('action.tryAgain')}</Button>}
          </View>
        </>}
        {certificateState.loadError !== undefined && !!context.account.account.hostedAccountId && <StateView variant="inline" {...runtime.error(certificateState.loadError, () => {void forms.certificate.load();}, 'server-console')}/>}
        {retryError !== undefined && <StateView variant="inline" {...runtime.error(retryError, () => {void retry();}, 'server-console')}/>}
        <Text style={[shellType(13, 700), {color: theme.colors.textTertiary, marginTop: 22, marginBottom: 8}]}>{t('next.settingsSpec.server.remote.groups.1.title')}</Text>
        <View style={{borderRadius: 14, backgroundColor: theme.colors.raised, padding: 14, gap: 14}}>
        <View style={{flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', gap: 12}}>
          <Text style={[shellType(15, 600), {color: theme.colors.text, flex: 1}]}>{t('next.settingsSpec.server.remote.groups.1.rows.0.label')}</Text>
          <Switch checked={connectivity.ownAddressEnabled === true} disabled={state.saving} onCheckedChange={value => forms.connectivity.set(current => ({...current, ownAddressEnabled: value}))}/>
        </View>
        <TextField label={t('next.settingsSpec.server.remote.groups.1.rows.0.children.0.label')} description={t('next.settingsSpec.server.remote.groups.1.rows.0.children.0.help')} value={connectivity.customCertificateDomain} editable={!state.saving && connectivity.ownAddressEnabled === true}
          onChangeText={value => forms.connectivity.set(current => ({...current, customCertificateDomain: value}))}/>
        <TextField label={t('next.settingsSpec.server.remote.groups.1.rows.0.children.1.label')} description={t('next.settingsSpec.server.remote.groups.1.rows.0.children.1.help')} value={connectivity.customCertificatePath} editable={!state.saving && connectivity.ownAddressEnabled === true}
          onChangeText={value => forms.connectivity.set(current => ({...current, customCertificatePath: value}))}/>
        <TextField label={t('next.settingsSpec.server.remote.groups.1.rows.0.children.2.label')} description={t('next.settingsSpec.server.remote.groups.1.rows.0.children.2.help')} value={connectivity.customCertificateKeyPath} editable={!state.saving && connectivity.ownAddressEnabled === true}
          onChangeText={value => forms.connectivity.set(current => ({...current, customCertificateKeyPath: value}))}/>
        {connectivityState.status?.identityCode ? <TextField testID="remote-access-identity-code" label={t('next.settingsSpec.server.remote.groups.1.rows.0.children.4.label')} description={t('next.settingsSpec.server.remote.groups.1.rows.0.children.4.help')} value={connectivityState.status.identityCode} editable={false} style={{fontFamily: 'monospace'}} containerStyle={{opacity: connectivity.ownAddressEnabled === true ? 1 : .5}}/> : null}
          {own && <Text testID="remote-own-address-status" style={[shellType(13), {color: own.tone === 'healthy' ? theme.colors.healthy : theme.colors.amber}]}>{own.text}</Text>}
        </View>
        <Text style={[shellType(13, 700), {color: theme.colors.textTertiary, marginTop: 22, marginBottom: 8}]}>{t('web.connectivity.outside')}</Text>
        <View style={{borderRadius: 14, overflow: 'hidden', backgroundColor: theme.colors.raised}}>
          <SettingsRow testID="remote-access-policy" label={t('web.connectivity.whoMaySignIn')}
            control={<Select testID="remote-access-policy-select" value={connectivity.remoteSignInPolicy} options={signInOptions} onValueChange={value => forms.connectivity.set(current => ({...current, remoteSignInPolicy: value as ConnectivityDraft['remoteSignInPolicy']}))}/>}/>
          <SettingsRow testID="remote-access-secure-connections" label={t('next.settingsSpec.server.remote.groups.0.rows.1.label')} description={t('next.settingsSpec.server.remote.groups.0.rows.1.help')}
            control={<Select testID="remote-access-secure-connections-select" value={connectivity.secureConnections} options={[
              {value: 'preferred', label: t('next.settingsSpec.server.remote.groups.0.rows.1.control.options.0.label')},
              {value: 'required', label: t('next.settingsSpec.server.remote.groups.0.rows.1.control.options.1.label')},
            ]} onValueChange={value => {if (value === 'preferred' || value === 'required') forms.connectivity.set(current => ({...current, secureConnections: value}));}}/>}/>
        </View>
        <Text style={[shellType(13, 700), {color: theme.colors.textTertiary, marginTop: 22, marginBottom: 8}]}>{t('settings.server.homeNetwork')}</Text>
        <View style={{borderRadius: 14, overflow: 'hidden', backgroundColor: theme.colors.raised}}>
          <SettingsRow testID="remote-access-discovery" first label={t('web.connectivity.discovery')}
            control={<Switch testID="remote-access-discovery-switch" checked={connectivity.lanDiscoveryEnabled} onCheckedChange={value => forms.connectivity.set(current => ({...current, lanDiscoveryEnabled: value}))}/>}/>
          <SettingsRow testID="remote-access-dlna" label={t('settings.server.dlna')} description={t('settings.server.dlnaHelp')}
            control={<Switch testID="remote-access-dlna-switch" checked={connectivity.dlnaEnabled} onCheckedChange={value => forms.connectivity.set(current => ({...current, dlnaEnabled: value}))}/>}/>
        </View>
        <NativeAdvancedSection testID="remote-access-advanced" summary={(() => {
          const rows = settingsSpec.find(section => section.scope === 'server' && section.id === 'remote')?.advanced?.groups?.flatMap(group => group.rows) ?? [];
          const values: Record<string, unknown> = remote ? {'remote.mapping': remote.mapping ? 'auto' : 'off', 'remote.gateway': remote.gateway, 'remote.publicPort': remote.publicPort, 'remote.ipv6': remote.ipv6Open ?? false, 'remote.mappingProtocols': {pcp: remote.pcp ? 'try' : 'skip', natpmp: remote.natpmp ? 'try' : 'skip', upnp: remote.upnp ? 'try' : 'skip'}} : {};
          const bound = settingsSummary(rows, row => {if (Object.hasOwn(values, row.id)) return {current: values[row.id]};const binding = settingsBinding(row.id);return binding ? readSettingsBinding(binding, {forms: {connectivity}}, row.id) : undefined;}, t);
          return [bound === t('next.settings.allDefaults') ? '' : bound, connectivity.accessUrls.length ? t('web.connectivity.extraAddresses') : '', connectivity.trustedProxies.length ? t('web.connectivity.trustedProxies') : '', connectivity.lanNetworks.length ? t('web.connectivity.homeNetworks') : '', connectivity.advertiseUrl ? t('web.connectivity.advertiseUrl') : ''].filter(Boolean).join(' · ') || t('next.settings.allDefaults');
        })()}>
          {remote && <>
          <SettingsRow testID="remote-access-enabled" first label={t('web.network.remoteTitle')} description={t('web.network.remoteHelp')}
            control={<Switch testID="remote-access-enabled-switch" checked={remote.enabled} onCheckedChange={value => forms.remote.set(current => ({...current, enabled: value}))}/>}/>
        <TextField label={t('web.network.publicPort')} value={String(remote.publicPort)} keyboardType="number-pad"
          onChangeText={value => {const next = Number(value); if (Number.isInteger(next) && next >= 1 && next <= 65535) forms.remote.set(current => ({...current, publicPort: next}));}}/>
        <TextField label={t('web.network.gateway')} description={t('web.network.gatewayHelp')} value={remote.gateway}
          onChangeText={value => forms.remote.set(current => ({...current, gateway: value}))}/>
          {remote.ipv6Open !== undefined ? <SettingsRow testID="remote-access-ipv6" label={t('web.network.ipv6')} description={t('web.network.ipv6Help')}
            control={<Switch testID="remote-access-ipv6-switch" checked={remote.ipv6Open === true} onCheckedChange={value => forms.remote.set(current => ({...current, ipv6Open: value}))}/>}/> : null}
        <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 12}}>
          <View style={{flexDirection: 'row', alignItems: 'center', gap: 8}}><Switch checked={remote.mapping} onCheckedChange={value => forms.remote.set(current => ({...current, mapping: value}))}/><Text style={[shellType(14), {color: theme.colors.text}]}>{t('web.network.mappingTitle')}</Text></View>
          <View style={{flexDirection: 'row', alignItems: 'center', gap: 8}}><Switch checked={remote.upnp} onCheckedChange={value => forms.remote.set(current => ({...current, upnp: value}))}/><Text style={[shellType(14), {color: theme.colors.text}]}>{t('settings.server.mapping.upnp')}</Text></View>
        </View>
          </>}
        <TextField label={t('web.connectivity.advertiseUrl')} description={t('settings.server.advertiseUrlHelp')} value={connectivity.advertiseUrl}
          onChangeText={value => forms.connectivity.set(current => ({...current, advertiseUrl: value}))}/>
        <TextField label={t('web.connectivity.extraAddresses')} description={t('settings.server.extraAddressesHelp')} value={lines(connectivity.accessUrls)} multiline
          onChangeText={value => forms.connectivity.set(current => ({...current, accessUrls: parseLines(value)}))}/>
        <TextField label={t('web.connectivity.trustedProxies')} description={t('settings.server.trustedProxiesHelp')} value={lines(connectivity.trustedProxies)} multiline
          onChangeText={value => forms.connectivity.set(current => ({...current, trustedProxies: parseLines(value)}))}/>
        <TextField label={t('web.connectivity.homeNetworks')} description={t('settings.server.homeNetworksHelp')} value={lines(connectivity.lanNetworks)} multiline
          onChangeText={value => forms.connectivity.set(current => ({...current, lanNetworks: parseLines(value)}))}/>
        <TextField testID="remote-access-connection-limit" label={t('web.connectivity.connectionLimit')} description={t('settings.server.connectionLimitHelp')} value={connectivity.connectionLimit ? String(connectivity.connectionLimit) : ''} placeholder={t('settings.server.automatic')} keyboardType="number-pad"
          onChangeText={value => {if (!value.trim()) {forms.connectivity.set(current => ({...current, connectionLimit: 0})); return;} const next = Number(value); if (Number.isInteger(next) && next >= 1 && next <= 65536) forms.connectivity.set(current => ({...current, connectionLimit: next}));}}/>
          {remoteState.loadError !== undefined && <StateView variant="inline" {...runtime.error(remoteState.loadError, () => {void forms.remote.load();}, 'server-console')}/>}
        </NativeAdvancedSection>
        {state.dirty ? <View style={{flexDirection: 'row', gap: 8, marginTop: 16}}>
          <Button variant="glass" disabled={state.saving} onPress={() => set.discard()}>{t('action.discard')}</Button>
          <Button loading={state.saving} disabled={Boolean(state.problem)} onPress={() => {void set.save();}}>{t('action.save')}</Button>
        </View> : null}
        {state.saveError !== undefined && <StateView variant="inline" {...runtime.error(state.saveError, () => {void set.save();}, 'server-console')}/>}
        {state.problem ? <Text accessibilityRole="alert" style={[shellType(13, 600), {color: theme.colors.record, marginTop: 8}]}>{t(state.problem as Parameters<typeof t>[0])}</Text> : null}
      </> : <StateView title={t('status.loading')}/>}
    </ScrollView>

  </View>;
}
