import {decodeGetSetupStateResponse} from '../../contracts/src/decoders.ts';
import {routePath} from '../../contracts/src/route-paths.ts';
import type {HttpLocalApi} from './index.ts';

/** The server's setup phases a caller with its own setup token can see (server setupflow). */
export const setupPhases = ['started', 'awaiting_approval', 'expired', 'approved', 'finishing', 'exhausted', 'installed', 'installed_lapsed'] as const;
export type SetupPhase = typeof setupPhases[number];

export type SetupOperation = Readonly<{operationId: string; localGeneration: string; revision: string}>;
/** The server applying an approval it was given: since the approval, failed tries, the next one. */
export type SetupDelivery = Readonly<{attempts: number; slow: boolean; since?: string; nextRetryAt?: string}>;
export type OwnSetup = Readonly<{
  phase: SetupPhase; requestId: string; revision: string;
  /** Wall-clock end of the approval window (started, awaiting approval) or the owner admission window (installed). */
  expiresAt?: string;
  operation?: SetupOperation; cancelling: boolean; renewable: boolean;
  delivery?: SetupDelivery;
  /** Started again because its Portico Account declined the last request. */
  outcome?: 'denied';
}>;
/** Anyone sees `status`; `setup` is present only for the token of the Portico Account setup in progress.
 * `setup: 'unknown'` is a phase this app doesn't know: a newer server, shown honestly, never guessed at. */
export type SetupState = Readonly<{
  /** `unknown` is a status from a newer server: shown as "needs a newer app", never guessed at. */
  status: 'ready' | 'in_progress' | 'complete' | 'unknown';
  retryAfterSeconds?: number;
  setup?: OwnSetup | 'unknown';
}>;

/** Reads GET /v1/setup/state. `api` carries the setup token as its bearer when the caller has one. */
export async function readSetupState(api: Pick<HttpLocalApi, 'request'>, signal?: AbortSignal): Promise<SetupState> {
  return parseSetupState(await api.request<unknown>(routePath('get_setup_state'), 'GET', undefined, signal));
}

export function parseSetupState(raw: unknown): SetupState {
  const decoded = decodeGetSetupStateResponse(raw);
  const status: SetupState['status'] = decoded.status === 'ready' || decoded.status === 'in_progress' || decoded.status === 'complete' ? decoded.status : 'unknown';
  const out: {status: SetupState['status']; retryAfterSeconds?: number; setup?: OwnSetup | 'unknown'} = {status};
  if (typeof decoded.retryAfterSeconds === 'number') out.retryAfterSeconds = decoded.retryAfterSeconds;
  const setup = decoded.setup;
  if (setup) {
    if ('unknownVariant' in setup || !(setupPhases as readonly string[]).includes(setup.variant)) out.setup = 'unknown';
    else out.setup = Object.freeze({phase: setup.variant as SetupPhase, requestId: setup.requestId, revision: setup.revision,
      ...(setup.expiresAt ? {expiresAt: setup.expiresAt} : {}),
      ...(setup.operation ? {operation: Object.freeze({operationId: setup.operation.operationId, localGeneration: setup.operation.localGeneration, revision: setup.operation.revision})} : {}),
      ...(setup.delivery ? {delivery: Object.freeze({attempts: setup.delivery.attempts, slow: setup.delivery.slow,
        ...(setup.delivery.since ? {since: setup.delivery.since} : {}), ...(setup.delivery.nextRetryAt ? {nextRetryAt: setup.delivery.nextRetryAt} : {})})} : {}),
      ...(setup.outcome === 'denied' ? {outcome: 'denied' as const} : {}),
      cancelling: setup.cancelling === true, renewable: setup.renewable === true});
  }
  return Object.freeze(out);
}
