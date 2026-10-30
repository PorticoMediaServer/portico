import {ApiError} from '../../../../packages/client-core/src';
import {presentError} from '../../../../packages/client-core/src/presentation/errors';

/** Issued only by the official Hosted request closure, never by a server API. */
export class HostedSignInUnavailable extends Error {
  readonly retryAfterSeconds: number;
  constructor(retryAfterSeconds?: number) {
    super('native_hosted_sign_in_unavailable');
    this.name = 'HostedSignInUnavailable';
    this.retryAfterSeconds = typeof retryAfterSeconds === 'number' && Number.isFinite(retryAfterSeconds) && retryAfterSeconds > 0
      ? Math.ceil(retryAfterSeconds) : 20;
  }
}

export function hostedSignInUnavailable(error: unknown): HostedSignInUnavailable | undefined {
  const presentation = presentError(error, 'portico-account');
  if (presentation.silent || ['authentication', 'permission', 'changed', 'unsupported-version', 'invalid-input'].includes(presentation.category)) return undefined;
  const availability = error instanceof ApiError ? error.status === 429 || error.status >= 500 && error.status <= 599
    : ['offline', 'unreachable', 'timeout'].includes(presentation.category);
  return availability ? new HostedSignInUnavailable(presentation.retryAfterSeconds) : undefined;
}
