import {ApiError} from '@core/index.ts';
import {RouteError} from '@core/route-identity.ts';

/** Only a failed request to an identified Hosted endpoint may carry this
 * source. A server probe/route error never proves the account service is down. */
export class DirectoryAccountUnavailable extends Error {
  readonly retryAfterSeconds?: number;
  constructor(readonly cause: unknown, retryAfterSeconds?: number) {
    super('Portico Accounts are temporarily unavailable.');
    this.name = 'DirectoryAccountUnavailable';
    this.retryAfterSeconds = retryAfterSeconds;
  }
}
export function accountDirectoryFailure(error: unknown): unknown {
  if (error instanceof DirectoryAccountUnavailable) return error;
  if (error instanceof ApiError && (error.status === 429 || error.status >= 500)) return new DirectoryAccountUnavailable(error, error.retryAfterSeconds);
  if (error instanceof Error && ['TypeError','NetworkError','TimeoutError'].includes(error.name)) return new DirectoryAccountUnavailable(error);
  return error;
}
export function directoryFailurePhase(error: unknown, automatic: boolean): 'account-waiting'|'identity'|'clock'|'update'|'waiting'|'unreachable'|undefined {
  if (error instanceof DirectoryAccountUnavailable) return 'account-waiting';
  if (error instanceof ApiError && (error.status === 429 || error.status >= 500)) return automatic ? 'waiting' : 'unreachable';
  // A network failure that is not an identified Portico Accounts request is the server not
  // answering (its name not resolving, the connection refused): never the account (H12).
  if (error instanceof Error && ['TypeError', 'NetworkError', 'TimeoutError'].includes(error.name)) return automatic ? 'waiting' : 'unreachable';
  if (!(error instanceof RouteError)) return;
  if (error.code === 'clocks_disagree') return 'clock';
  if (error.code === 'api_level_mismatch') return 'update';
  if (['identity_mismatch','invalid_address','invalid_identity','invalid_routes','invalid_signature','invalid_account_key','certificate_unverified','untrusted_certificate','route_stale'].includes(error.code)) return 'identity';
  if (['route_unavailable','server_unavailable','network_unavailable'].includes(error.code)) return automatic ? 'waiting' : 'unreachable';
}
