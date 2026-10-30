import {NativeModules} from 'react-native';
import {HttpLocalApi, type HostedSession, type LocalSession, type DeviceAuthorization} from '../../../../packages/client-core/src';
import {SetupCodeService, type SetupCodeStorage, type SetupCodeJournal} from '../../../../packages/client-core/src/setup-code';
import {parseLocalSession} from '../../../../packages/client-core/src/local-session';
import {parseAccountSession} from '../../../../packages/client-core/src/hosted-security';
import {installationIdentity} from '../../../../packages/client-core/src/installation';
import {routePath, routeBody} from '../../../../packages/contracts/src/routes';
import {routePath as hostedPath, routeBody as hostedBody} from '../../../../packages/contracts/src/hosted/routes';
import type {ServerSignIn} from '../../../../packages/client-core/src/sign-in';
import {NativeQueueStore} from './queue-storage';
import {nativeNetworkFetch} from './server-connections';
import {hostedUrl} from './account-authority';

const invalid = () => Object.assign(new Error('native_tv_consent_invalid'), {code: 'invalid_consent'});
function identity() {
  const value = installationIdentity(); if (!value) throw invalid();
  return {deviceName: value.name, platform: value.platform, appVersion: value.appVersion};
}
function storage<T>(authority: string): SetupCodeStorage<T> {
  return {change: update => new NativeQueueStore().change('accounts.tv:' + authority, raw => {
    const record = update(raw == null ? undefined : raw as SetupCodeJournal<T>); return {record, value: record};
  })};
}
const requestId = async () => {
  const value: unknown = await NativeModules.PorticoRuntime.newRequestId();
  // Apple NSUUID and the Vega bridge emit uppercase UUIDs; the durable
  // activation journal requires their canonical lowercase v4 representation.
  const canonical = typeof value === 'string' ? value.toLowerCase() : '';
  if (!/^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/.test(canonical)) throw invalid();
  return canonical;
};

/** Server-local Quick Connect is same-account sign-in consent. It has no pending/trust gate. */
export function createNativeLocalTVSignIn(server: ServerSignIn, api: HttpLocalApi) {
  const installed = installationIdentity(), pin = api.getRouteConnection()?.pin;
  if (!installed || server.signIn !== 'local' || pin?.serverId !== server.id) throw invalid();
  return new SetupCodeService<LocalSession>('local:' + server.id, {
    create: (id, signal) => api.request<DeviceAuthorization>(routePath('post_quick_connect'), 'POST', routeBody('post_quick_connect', {requestId: id, ...identity()}), signal),
    poll: async (entry, signal) => {
      const session = parseLocalSession(await api.request(routePath('post_quick_connect_token'), 'POST', routeBody('post_quick_connect_token', {deviceCode: entry.authorization.deviceCode}), signal), {authority: 'local', serverId: server.id});
      if (session.installationId !== installed.installationId || installationIdentity() !== installed) throw invalid(); return session;
    },
    cancel: (entry, signal) => api.request<void>(routePath('post_quick_connect_cancel'), 'POST', routeBody('post_quick_connect_cancel', {deviceCode: entry.authorization.deviceCode}), signal),
  }, storage('local:' + server.id), requestId);
}

/** Portico TV consent keeps native private-key decryption and the canonical durable
 * create/poll/cancel journal. Only an explicit start contacts the account authority. */
export function createNativePorticoTVSignIn() {
  const http = new HttpLocalApi(hostedUrl, '', nativeNetworkFetch), authority = hostedUrl + '#tv-x25519-v1';
  return new SetupCodeService<HostedSession>(authority, {
    create: async (id, signal) => {
      const publicKey: unknown = await NativeModules.PorticoNetwork.tvPublicKey(id);
      if (signal.aborted || typeof publicKey !== 'string' || !/^[A-Za-z0-9_-]{43}$/.test(publicKey)) throw invalid();
      return http.request<DeviceAuthorization>(hostedPath('device_authorization_create'), 'POST', hostedBody('device_authorization_create',
        {requestId: id, ...identity(), protocol: 'tv-x25519-v1', publicKey}), signal);
    },
    poll: async (entry, signal) => {
      const envelope = await http.request<Record<string, unknown>>(hostedPath('tv_authorization_token'), 'POST', hostedBody('tv_authorization_token', {deviceCode: entry.authorization.deviceCode}), signal);
      const key: unknown = await NativeModules.PorticoNetwork.tvPublicKey(entry.requestId);
      if (signal.aborted || envelope.issuer !== hostedUrl || envelope.requestId !== entry.requestId || envelope.recipientKey !== key) throw invalid();
      const raw: unknown = await NativeModules.PorticoNetwork.openTVGrant(entry.requestId, envelope);
      if (signal.aborted || typeof raw !== 'string' || raw.length > 262144) throw invalid();
      const session = parseAccountSession(JSON.parse(raw), 'native');
      if ('mfaRequired' in session) throw invalid(); return session;
    },
    cancel: (entry, signal) => http.request<void>(hostedPath('device_authorization_cancel'), 'POST', hostedBody('device_authorization_cancel', {deviceCode: entry.authorization.deviceCode}), signal),
    forgotten: entry => {NativeModules.PorticoNetwork.forgetTVKey(entry.requestId);},
  }, storage(authority), requestId);
}
