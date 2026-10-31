import {HttpLocalApi, type LocalSession} from './index.ts';
import type {AppSessionStore} from './app-sessions.ts';
import {parseDirectSignIn, type DirectSignIn} from './profile-management.ts';
import {sameServerPin, webRouteCrypto, type RouteCrypto} from './route-identity.ts';
import {connectPairedPorticoServer, connectPairedServer, rememberNativeSession, type ConnectionEnvironment} from './server-connections.ts';
import {CodedError} from './server-messages.ts';
import {setupAttemptBound, setupAttemptProofMatches, setupOwnerGrantFor} from './setup-custody.ts';
import {verifySetupClaimStart} from './setup-claim-start.ts';
import {readSetupState} from './setup-state.ts';
import type {SetupAttempt, SetupStore} from './setup-store.ts';
import {routePath} from '../../contracts/src/route-paths.ts';

export type SetupExchangePorts = Readonly<{
  sessions: Pick<AppSessionStore, 'read' | 'connectionStorage'>; environment: (serverId: string) => ConnectionEnvironment;
  now?: () => number; crypto?: RouteCrypto;
  connect?: typeof connectPairedServer; connectPortico?: typeof connectPairedPorticoServer; remember?: typeof rememberNativeSession;
  retire?: (api: HttpLocalApi, session: LocalSession) => Promise<void>;
}>;
export type SetupExchanged = Readonly<{api: HttpLocalApi; result: Extract<DirectSignIn, {kind: 'signed-in'}>; connection: {dispose: () => void}}>;
export const setupExchangeInvalid = () => new CodedError('invalid_return', 'This setup continuation is no longer available.');
export const setupExchangeCancelled = () => new CodedError('cancelled', 'The pending setup or server selection changed.');

/** Finishes a Portico Account setup from its bound v2 attempt (B-DESIGN §6.3), for the web Return and the native
 * exchange alike. The attempt is spent before any network I/O, so it can be used once; a lost completion is final,
 * and ordinary sign-in then admits the installed owner. Only this setup, installed, is exchanged, and only its owner
 * grant is admitted. Anything that fails after the grant removes what was remembered and ends the session.
 * `current` throws when the caller's run was replaced; `admitted` says the caller still wants the admission. */
export async function finishSetupExchange(ports: SetupExchangePorts, input: Readonly<{
  attempt: SetupAttempt; store: SetupStore; signal: AbortSignal; current: () => void; admitted: () => boolean;
}>): Promise<SetupExchanged> {
  const {attempt, store, signal, current} = input, now = ports.now ?? Date.now;
  let connected: Awaited<ReturnType<typeof connectPairedServer>> | undefined, bound: Awaited<ReturnType<typeof connectPairedPorticoServer>> | undefined;
  let grant: DirectSignIn | undefined, adopted = false, admissionServer: string | undefined;
  try {
    const local = await store.read();
    current();
    const record = local.record;
    if (!record || !setupAttemptBound(attempt, record) || record.authMode !== 'hosted') throw setupExchangeInvalid();
    // Spent before any network I/O: this attempt can never be replayed.
    if (!await store.change({attemptId: attempt.attemptId}, {attempt: null})) throw setupExchangeCancelled();
    current();
    const selection = await ports.sessions.read();
    current();
    const proof = await verifySetupClaimStart(attempt.handoff.claim, attempt.handoff.start, attempt.handoff.binding, now());
    if (!await setupAttemptProofMatches(attempt, proof, ports.crypto ?? webRouteCrypto)) throw setupExchangeInvalid();
    current();
    const pin = attempt.handoff.binding.pin, environment = ports.environment(attempt.serverId);
    connected = await (ports.connect ?? connectPairedServer)(attempt.origin, pin, environment, signal);
    current();
    connected.api.setAccessToken(record.setupToken);
    // Only this setup, installed, completes admission.
    const state = await readSetupState(connected.api, signal);
    current();
    if (typeof state.setup !== 'object' || state.setup.phase !== 'installed' || state.setup.requestId !== record.requestId) throw setupExchangeInvalid();
    grant = parseDirectSignIn(await connected.api.request(routePath('post_auth_portico_return_exchange'), 'POST', {transactionId: attempt.handoff.transactionId, state: attempt.state,
      verifier: attempt.verifier, installationId: attempt.installationId, serverChallenge: proof.start.serverChallenge, purpose: 'setup'}, signal));
    current();
    if (!setupOwnerGrantFor(grant, attempt, now())) throw setupExchangeInvalid();
    const accountId = grant.snapshot.account.hostedAccountId!;
    bound = await (ports.connectPortico ?? connectPairedPorticoServer)(attempt.origin, pin,
      {origin: attempt.hostedOrigin, accountId, request: async () => {throw setupExchangeInvalid();}}, environment, signal);
    current();
    bound.api.setAccessToken(grant.session.accessToken);
    admissionServer = attempt.serverId;
    await (ports.remember ?? rememberNativeSession)(bound.api, grant.session,
      {...environment, storage: ports.sessions.connectionStorage(attempt.serverId, {signIn: 'portico', hostedAccount: bound.hosted, expectedRevision: selection.revision})}, bound.hosted, input.admitted);
    const stored = await ports.sessions.read(), entry = stored.servers.find(value => value.connection.pin.serverId === stored.selectedServerId);
    if (!input.admitted() || !entry || entry.signIn !== 'portico' || !sameServerPin(entry.connection.pin, pin) || entry.connection.session.sessionFamilyId !== grant.session.sessionFamilyId ||
      entry.connection.session.viewer.accountId !== grant.snapshot.account.id || entry.connection.hosted?.origin !== attempt.hostedOrigin || entry.connection.hosted.accountId !== accountId) throw setupExchangeCancelled();
    // Admitted: the setup record has done its work.
    await store.change({requestId: record.requestId}, {record: null});
    adopted = true;
    return {api: bound.api, result: grant, connection: bound.connection};
  } finally {
    try {
      if (!adopted && grant?.kind === 'signed-in') {
        const family = grant.session.sessionFamilyId;
        if (admissionServer) await ports.sessions.connectionStorage(admissionServer).change(old => old?.session.sessionFamilyId === family ? undefined : old).catch(() => {});
        if (connected) await retire(ports, connected.api, grant.session);
      }
    } finally {connected?.connection.dispose(); if (!adopted) bound?.connection.dispose();}
  }
}

async function retire(ports: SetupExchangePorts, api: HttpLocalApi, session: LocalSession) {
  if (ports.retire) return ports.retire(api, session);
  const temporary = new HttpLocalApi(api.baseUrl, session.accessToken), routes = api.getRouteConnection();
  if (routes) temporary.useRoutes(routes);
  await temporary.request(routePath('delete_sessions_current'), 'DELETE', undefined, AbortSignal.timeout(5000)).catch(() => {});
}
