import {throwIfAborted} from './abort-lifetime.ts';
import {routePath} from '../../contracts/src/route-paths.ts';
import type {HttpLocalApi} from './index.ts';
import {installationIdentity, validInstallationId} from './installation.ts';
import {captureSetupHandoff, checkSetupHandoff, type SetupClaimHandoff} from './setup-claim-client.ts';
import type {SetupStartExpectation} from './setup-claim-start.ts';
import {routeBytes, routeOrigin, sameServerPin, webRouteCrypto, type RouteCrypto} from './route-identity.ts';
import {setupAddressAllowed} from './setup-address.ts';
import {CodedError} from './server-messages.ts';

/** This caller's half of a setup approval: everything the signed start must name except the claim operation,
 * which the server chooses (B-DESIGN S6). */
export type SetupApprovalStart = Omit<SetupStartExpectation, 'operationId' | 'localGeneration'>;
export type SetupApprovalPorts = Readonly<{
  /** Re-read the setup record and attempt and refuse a successor; called after every await. */
  assertCurrent: () => Promise<void>;
  /** Bind the verified handoff and its approval link to this same attempt (compare-and-swap). A refused write must not navigate. */
  bind: (handoff: SetupClaimHandoff, approvalUrl: string) => Promise<void>;
}>;
type Api = Pick<HttpLocalApi, 'baseUrl' | 'request' | 'getRouteConnection'>;
const invalid = () => new CodedError('invalid_return', 'This setup continuation no longer matches its captured operation.');
const object = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value);
const counter = (value: unknown): value is string => typeof value === 'string' && /^(0|[1-9][0-9]{0,18})$/.test(value) && BigInt(value) <= 9223372036854775807n;
const identifier = (value: unknown): value is string => typeof value === 'string' && /^[A-Za-z0-9_-]{1,128}$/.test(value);

/** POST /v1/setup/approval with the setup token as the api's bearer: the server prepares the claim if needed, signs this
 * start and registers it with Portico Services. The answer's signed pair must bind this exact start to the operation it
 * names, under the pinned server key, before the handoff is bound and the approval link returned. `renewal`: an
 * installed setup whose admission lapsed renews its start (S7), with no second approval. */
export async function requestSetupApproval(api: Api, start: SetupApprovalStart, ports: SetupApprovalPorts,
  options: Readonly<{signal?: AbortSignal; now?: () => number; crypto?: RouteCrypto; renewal?: true}> = {}): Promise<Readonly<{handoff: SetupClaimHandoff; approvalUrl: string}>> {
  start = Object.freeze({pin: Object.freeze({...start.pin}), origin: start.origin, hostedOrigin: start.hostedOrigin, setupRequestId: start.setupRequestId,
    setupRevision: start.setupRevision, state: start.state, codeChallenge: start.codeChallenge, installationId: start.installationId});
  const now = options.now ?? Date.now;
  if (!validInstallationId(start.installationId) || !/^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/.test(start.setupRequestId) ||
      !counter(start.setupRevision) || start.setupRevision === '0' || routeOrigin(start.origin) !== start.origin || api.baseUrl !== start.origin ||
      routeOrigin(start.hostedOrigin) !== start.hostedOrigin || !start.hostedOrigin.startsWith('https://') || !setupAddressAllowed(start.origin)) throw invalid();
  routeBytes(start.state, 32); routeBytes(start.codeChallenge, 32);
  const current = async () => {
    throwIfAborted(options.signal);
    await ports.assertCurrent();
    throwIfAborted(options.signal);
    const route = api.getRouteConnection();
    if (!route || route.getSnapshot().phase !== 'ready' || !sameServerPin(route.pin, start.pin) || installationIdentity()?.installationId !== start.installationId) throw invalid();
  };
  await current();
  const raw: unknown = await api.request(routePath('post_setup_approval'), 'POST',
    {setupStart: {state: start.state, codeChallenge: start.codeChallenge, installationId: start.installationId}}, options.signal);
  // The operation is the server's choice; the signed start below must name it together with this caller's start.
  const operation = object(raw) && object(raw.operation) ? raw.operation : undefined;
  if (!operation || !identifier(operation.operationId) || !counter(operation.localGeneration) || !counter(operation.revision) || operation.revision === '0') throw invalid();
  const expected = Object.freeze({operationId: operation.operationId, localGeneration: operation.localGeneration, revision: operation.revision});
  const binding: SetupStartExpectation = Object.freeze({...start, operationId: expected.operationId, localGeneration: expected.localGeneration});
  const {handoff, approvalURL, code} = captureSetupHandoff(raw, expected, binding, options.renewal === true);
  await current();
  await checkSetupHandoff(handoff, approvalURL, code, now(), options.crypto ?? webRouteCrypto);
  await current();
  const fresh = () => {const time = now(); if (!Number.isSafeInteger(time) || time >= Date.parse(handoff.expiresAt)) throw invalid();};
  fresh();
  await ports.bind(handoff, approvalURL);
  await current();
  fresh();
  return Object.freeze({handoff, approvalUrl: approvalURL});
}
