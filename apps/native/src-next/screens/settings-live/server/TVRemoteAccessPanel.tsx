import React, {useEffect, useState} from 'react';
import {Text, View} from 'react-native';
import {routePath} from '../../../../../../packages/contracts/src/routes';
import {parseRemoteAccess, type RemoteAccess} from '../../../../../../packages/client-core/src/remote-access';
import {parseConnectivityStatus, type ConnectivityStatus} from '../../../../../../packages/client-core/src/server-administration';
import {parseCertificateStatus, type CertificateStatus} from '../../../../../../packages/client-core/src/certificates';
import {certificateClaimed, secureConnectionView} from '../../../../../../packages/client-core/src/server-admin/secure-connection';
import {remoteStatusLine} from '../../../../../../packages/client-core/src/server-admin/status-words';
import type {MessageId} from '../../../../../../packages/i18n/src';
import {Focusable} from '../../../kit/controls';
import {shellType} from '../../../kit/shell/visual';
import {StateView} from '../../../kit/states';
import {useTheme} from '../../../kit/theme';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeScreenLifetime} from '../lifetime';
import type {NativeSettingsLivePorts} from '../ports';
import {createTVServerReader} from './tv-server-reader';

type Snapshot = {remote: RemoteAccess; connectivity: ConnectivityStatus; certificate?: CertificateStatus};
/** TV38 exposes only selected public settings. Certificate material, mapping internals,
 * trusted proxies and credentials never enter a row or the accessibility label. */
export function TVRemoteAccessPanel({ports, runtime, lifetime, statusOnly = false}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; lifetime: NativeScreenLifetime; statusOnly?: boolean}) {
  const theme = useTheme(), t = runtime.text.t;
  const [state, setState] = useState<Snapshot>(), [error, setError] = useState<unknown>(), [revision, setRevision] = useState(0);
  useEffect(() => {
    const abort = new AbortController();
    const api = createTVServerReader(ports.context, lifetime);
    setState(undefined); setError(undefined);
    const certificate = api.request<unknown>(routePath('get_networking_certificate'), 'GET', undefined, abort.signal).then(parseCertificateStatus).catch(error => {
      if (error && typeof error === 'object' && 'status' in error && error.status === 404) return undefined;
      throw error;
    });
    void Promise.all([
      api.request<unknown>(routePath('get_networking_remote'), 'GET', undefined, abort.signal).then(parseRemoteAccess),
      api.request<unknown>(routePath('get_connectivity_status'), 'GET', undefined, abort.signal).then(parseConnectivityStatus),
      certificate,
    ]).then(([remote, connectivity, certificate]) => {if (!abort.signal.aborted && lifetime.current()) setState({remote, connectivity, certificate});}, error => {if (!abort.signal.aborted && lifetime.current()) setError(error);});
    const retire = () => {abort.abort(); setState(undefined); setError(undefined);};
    lifetime.signal.addEventListener('abort', retire, {once: true});
    return () => {abort.abort(); lifetime.signal.removeEventListener('abort', retire);};
  }, [ports, lifetime, revision]);
  if (error) return <StateView {...runtime.error(error, () => setRevision(value => value + 1))}/>;
  if (!state) return <StateView title={t('status.loading')}/>;
  const secure = secureConnectionView({certificate: state.certificate, own: state.connectivity.tls.customCertificate});
  const remote = remoteStatusLine(t, {unavailable: false, ...state});
  const row = (id: string, label: MessageId, value: string, help?: string, healthy?: boolean) => <Focusable key={id} focusId={id} focusKind="row" accessibilityRole="text" accessibilityLabel={[t(label), help, value].filter(Boolean).join('. ')} style={{paddingHorizontal: 24, paddingVertical: 22, marginBottom: 3, backgroundColor: theme.colors.raised, borderRadius: 12, flexDirection: 'row', gap: 28, alignItems: 'center'}}>
    <View style={{flex: 1, gap: 6}}><Text style={[shellType(26, 600), {color: theme.colors.text}]}>{t(label)}</Text>{help && <Text style={[shellType(20, 400, 28), {color: theme.colors.textSecondary}]}>{help}</Text>}</View>
    {healthy !== undefined && <View style={{width: 12, height: 12, borderRadius: 6, backgroundColor: healthy ? theme.colors.healthy : theme.colors.amber}}/>}
    <Text style={[shellType(22, 600), {maxWidth: '45%', color: theme.colors.textSecondary}]}>{value}</Text>
  </Focusable>;
  return <View style={statusOnly ? {marginBottom: 30} : undefined}>
    {row('remote', 'settings.server.remote', t(state.remote.config.enabled ? 'web.hostedSecurity.on' : 'web.hostedSecurity.off'), remote.text, remote.tone === 'healthy')}
    {row('secure', 'settings.server.secure.title', t(secure.badge), t(secure.body, secure.address ? {address: secure.address} : undefined), secure.tone === 'healthy')}
    {/* An owner changes these rows below, in the page's own settings. */}
    {!statusOnly && row('policy', 'web.connectivity.whoMaySignIn', t(`settings.server.signIn.${state.connectivity.policy.remoteSignInPolicy}`))}
    {!statusOnly && row('dlna', 'settings.server.dlna', t(state.connectivity.policy.dlnaEnabled ? 'web.hostedSecurity.on' : 'web.hostedSecurity.off'), t('settings.server.dlnaHelp'), state.connectivity.policy.dlnaEnabled)}
    {state.certificate && row('account', 'account.portico', t(certificateClaimed(state.certificate) ? 'web.settings.throughAccount' : 'settings.server.secure.notSetUp'))}
  </View>;
}
