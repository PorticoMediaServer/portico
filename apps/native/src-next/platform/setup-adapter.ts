import {Linking} from 'react-native';
import {assertFirstContactIdentity} from '../../../../packages/client-core/src';
import {knownServerRoutes} from '../../../../packages/client-core/src/app-sessions';
import {installationIdentity} from '../../../../packages/client-core/src/installation';
import {CodedError} from '../../../../packages/client-core/src/server-messages';
import {connectPairedServer, inspectDirectServer, rememberNativeSession} from '../../../../packages/client-core/src/server-connections';
import {createSetupAdapter, type SetupAdapter, type SetupPairing} from '../../../../packages/client-core/src/setup-adapter';
import {setupApprovalCode} from '../../../../packages/client-core/src/setup-claim-attempt';
import {verifySetupClaimStart} from '../../../../packages/client-core/src/setup-claim-start';
import type {ServerSignIn} from '../../../../packages/client-core/src/sign-in';
import type {SetupAttempt} from '../../../../packages/client-core/src/setup-store';
import {hostedUrl} from './account-authority';
import {nativeAccounts} from './accounts';
import {nativeAppSessions} from './app-session-storage';
import {nativeRouteCrypto, nativeServerEnvironment, restoreNativeConnection} from './server-connections';
import {NativeSetupApproval} from './setup-approval';
import {nativeSetupRecords} from './setup-records';

const mismatch = () => Object.assign(new Error('native_setup_identity_changed'), {code: 'identity_mismatch'});
const invalid = () => new CodedError('invalid_return', 'This setup approval no longer matches its original request.');

export type NativeSetupTarget = Readonly<{origin: string; server: ServerSignIn; expected?: Readonly<{serverId: string; fingerprint: string}>}>;

/** The app's half of first-run setup (B-DESIGN §6, native): the same adapter as the web, paired with the same explicit
 * first-contact proof as sign-in, a token for an app, and a Portico Account setup finished here once the server reads
 * installed (the app has no Return to consume). The app's own Portico Account can approve in place (frame 1p, label 29);
 * otherwise the approval page opens in the browser and the app carries on when the person comes back. */
export function createNativeSetupAdapter(target: NativeSetupTarget, options: Readonly<{current?: () => boolean}> = {}): SetupAdapter {
  const {origin, server} = target;
  return createSetupAdapter({
    tokenRoute: 'start_app_setup',
    finish: {kind: 'here', ports: {sessions: nativeAppSessions, environment: serverId => nativeServerEnvironment(serverId), crypto: nativeRouteCrypto}},
    hostedOrigin: hostedUrl,
    pageOrigin: () => origin,
    pair: signal => pairAtFirstContact(target, signal),
    sessions: () => nativeAppSessions,
    environment: serverId => nativeServerEnvironment(serverId),
    restore: session => restoreNativeConnection(hostedUrl, () => nativeAccounts.accountCredentials(), session),
    remember: rememberNativeSession,
    store: nativeSetupRecords,
    installationId: () => installationIdentity()?.installationId,
    navigate: url => {void Linking.openURL(url).catch(() => {});},
    approveInApp: approveWithAppAccount,
    current: options.current,
    crypto: nativeRouteCrypto,
  });
}

/** Setup is admitted only after the same explicit first-contact proof as sign-in. */
async function pairAtFirstContact(target: NativeSetupTarget, signal: AbortSignal): Promise<SetupPairing> {
  const {origin, server, expected} = target, environment = nativeServerEnvironment(server.id);
  const pin = await inspectDirectServer(origin, environment, signal);
  if (pin.serverId !== server.id || expected && (pin.serverId !== expected.serverId || pin.fingerprint !== expected.fingerprint)) throw mismatch();
  assertFirstContactIdentity(origin, pin, {knownRoutes: knownServerRoutes(await nativeAppSessions.read())});
  const connected = await connectPairedServer(origin, pin, environment, signal);
  return {server, api: connected.api, pin, dispose: () => connected.connection.dispose()};
}

/** The app's signed-in Portico Account approves the claim this attempt is bound to, through the same lookup and decision
 * as the Portico Services page. An approval already made counts; a denial ends it there. */
async function approveWithAppAccount(input: Readonly<{attempt: SetupAttempt; approvalUrl: string; signal: AbortSignal; current: () => Promise<void>}>) {
  const {attempt, approvalUrl, signal, current} = input, handoff = attempt.handoff;
  if (!handoff) throw invalid();
  const proof = await verifySetupClaimStart(handoff.claim, handoff.start, handoff.binding, Date.now(), nativeRouteCrypto);
  const code = setupApprovalCode(approvalUrl, {hostedOrigin: attempt.hostedOrigin, operationId: handoff.expected.operationId, serverName: proof.claim.name});
  if (!code) throw invalid();
  await current();
  const live = () => !signal.aborted;
  const approval = new NativeSetupApproval({code, serverId: attempt.serverId, name: proof.claim.name, origin: attempt.origin, transactionId: handoff.transactionId, expiresAt: handoff.expiresAt},
    current, signal, {signal, current: live, canApprove: live}, () => {});
  try {
    const review = await approval.review();
    if (!review.approved) await approval.approve();
  } finally {approval.dispose();}
}
