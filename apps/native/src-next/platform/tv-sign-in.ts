import {revokeCurrentSession, createLocalTVAuthorization, pollLocalTVAuthorization} from '../../../../packages/client-core/src/query/connection-operations.ts';
import {NativeModules, Platform} from 'react-native';
import {HttpLocalApi, type HostedSession, type LocalSession, type DeviceAuthorization} from '../../../../packages/client-core/src';
import {SetupCodeService, type SetupCodeStorage, type SetupCodeJournal} from '../../../../packages/client-core/src/setup-code';
import {parseLocalSession} from '../../../../packages/client-core/src/local-session';
import {porticoAccount, ServicesError} from '../../../../packages/client-core/src/portico-account';
import {heldFor, hostedSessionFrom, porticoApiError} from '../../../../packages/client-core/src/portico-services-native';
import {ApiError} from '../../../../packages/client-core/src/http';
import {routeBase64} from '../../../../packages/client-core/src/route-identity';
import {installationIdentity} from '../../../../packages/client-core/src/installation';
import type {ServerSignIn} from '../../../../packages/client-core/src/sign-in';
import {NativeQueueStore} from './queue-storage';
import {nativeNetworkFetch, nativeRouteCrypto} from './server-connections';
import {hostedUrl} from './account-authority';

const invalid = () => Object.assign(new Error('native_tv_consent_invalid'), {code: 'invalid_consent'});

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
    create: (id, signal) => createLocalTVAuthorization<DeviceAuthorization>(api, id, signal),
    poll: async (entry, signal) => {
      const session = parseLocalSession(await pollLocalTVAuthorization(api, entry.authorization.deviceCode, signal), {authority: 'local', serverId: server.id});
      if (session.installationId !== installed.installationId || installationIdentity() !== installed) throw invalid(); return session;
    },
    abandoned: entry => {
      if (entry.grant) void revokeCurrentSession(api.withAccessToken(entry.grant.accessToken)).catch(() => {});
    },
  }, storage('local:' + server.id), requestId);
}

/** Portico Services has no TV cancel route: the journal forgets the code locally. */
export function createNativePorticoTVSignIn() {
  const authority = hostedUrl + '#tv-codes', store = new NativeQueueStore();
  const bindingKey = 'accounts.tv-binding:' + authority;
  const account = porticoAccount(heldFor(''), hostedUrl, {fetcher: nativeNetworkFetch});
  const forget = (id: string) => store.change(bindingKey, raw => {
    const saved = raw as {requestId?: string} | null;
    return {record: saved?.requestId === id ? null : raw, value: undefined};
  });
  return new SetupCodeService<HostedSession>(authority, {
    create: async (id, signal) => {
      // Save before sending: a lost create answer must reuse BOTH the secret and key.
      const candidate = routeBase64(nativeRouteCrypto.random(32));
      const secret = await store.change(bindingKey, raw => {
        const saved = raw as {requestId?: string; secret?: string} | null;
        const secret = saved?.requestId === id ? saved.secret : candidate;
        if (typeof secret !== 'string' || !/^[A-Za-z0-9_-]{43}$/.test(secret)) throw invalid();
        return {record: {requestId: id, secret}, value: secret};
      });
      const secretHash = routeBase64(await nativeRouteCrypto.sha256(new TextEncoder().encode(secret)));
      const device = {name: Platform.OS === 'ios' ? 'Portico on Apple TV' : 'Portico on TV', kind: 'tv'};
      let answer;
      try {answer = await account.createTV(device, secretHash, id, signal);} catch (error) {throw porticoApiError(error);}
      if (typeof answer.id !== 'string' || !answer.id) throw invalid();
      const expiresIn = Math.ceil((Date.parse(answer.expiresAt) - Date.now()) / 1000);
      // deviceCode stays inside the protected journal; snapshots expose only code and QR.
      const authorization: DeviceAuthorization & {id: string} = {id: answer.id, deviceCode: secret, userCode: answer.code,
        verificationUri: answer.verifyUrl, verificationUriComplete: answer.verifyUrlWithCode,
        expiresAt: answer.expiresAt, expiresIn, interval: Math.ceil(answer.pollAfterMs / 1000)};
      return authorization;
    },
    poll: async (entry, signal, checkpoint) => {
      const authorization = entry.authorization as DeviceAuthorization & {id: string};
      let answer;
      try {answer = await account.redeemTV(authorization.id, authorization.deviceCode, signal);}
      catch (error) {
        if (error instanceof ServicesError && ['tv_code_gone', 'tv_code_denied'].includes(error.code))
          throw new ApiError(410, error.code === 'tv_code_denied' ? 'access_denied' : 'expired_token', error.code);
        throw porticoApiError(error);
      }
      if (answer.state === 'waiting') throw new ApiError(202, 'authorization_pending', 'tv_code_waiting', false, (answer.pollAfterMs ?? 5000) / 1000);
      if (answer.state !== 'approved' || !answer.tokens) throw invalid();
      const tokens = answer.tokens;
      try {
        const person = await porticoAccount(heldFor(tokens.accessToken), hostedUrl, {fetcher: nativeNetworkFetch}).account(signal);
        const session = hostedSessionFrom(tokens, {id: person.id, username: person.username, displayName: person.displayName}, entry.requestId);
        await checkpoint(session); return session;
      } catch {
        // Redemption is single-use. End an issued sign-in that cannot be saved/adopted.
        void account.endWith(tokens.accessToken).catch(() => {});
        throw new ApiError(410, 'invalid_grant', 'tv_code_sign_in_not_saved');
      }
    },
    forgotten: entry => {void forget(entry.requestId).catch(() => {});},
    abandoned: entry => {
      void forget(entry.requestId).catch(() => {});
      if (entry.grant) void account.endWith(entry.grant.accessToken).catch(() => {});
    },
  }, storage(authority), requestId);
}
