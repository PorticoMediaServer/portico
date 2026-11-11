import {serverPath} from './server-api.ts';
import {routeOrigin, RouteError} from '../route-identity.ts';
type OwnerRecoveryPlatform = {isAccountAuthority: (origin: string) => boolean; pendingCertificate: (origin: string) => unknown;
  checkPlainHttp?: (input: string) => void; certificateTrustAvailable: () => boolean; refused: (origin: string) => Promise<unknown>};

export function createOwnerRecoveryFetch(capturedOrigin: string, isCurrent: () => boolean, fetcher: typeof fetch, platform: OwnerRecoveryPlatform): typeof fetch {
  const origin = routeOrigin(capturedOrigin);
  if (platform.isAccountAuthority(origin)) throw new RouteError('invalid_address', 'Choose the server address.');
  const check = (signal?: AbortSignal | null) => {
    if (!signal || signal.aborted || !isCurrent()) throw new RouteError('cancelled', 'Owner recovery contact cancelled.');
    if (platform.pendingCertificate(origin)) throw new RouteError('identity_code_needed', 'identity_code_needed');
  };
  return async (input, options = {}) => {
    check(options.signal);
    const method = options.method ?? 'GET';
    if (typeof input !== 'string' ||
        !(input === origin + serverPath('POST /v1/system/identity-proof') && method === 'POST' ||
          input === origin + serverPath('GET /v1/system') && method === 'GET' && options.body == null))
      throw new RouteError('invalid_address', 'Owner recovery contact is limited to the captured server.');
    const headers = new Headers(options.headers);
    headers.forEach((_value, name) => {
      if (name !== 'accept' && name !== 'content-type')
        throw new RouteError('invalid_request', 'Owner recovery contact carries no credentials.');
    });
    platform.checkPlainHttp?.(input);
    const response = await fetcher(input, {...options, headers, credentials: 'omit', redirect: 'error', referrerPolicy: 'no-referrer'});
    try {
      check(options.signal);
      // The strict path cannot settle an OS-refused leaf or install new native certificate trust.
      if (platform.certificateTrustAvailable()) {
        const refused = await platform.refused(origin);
        check(options.signal);
        if (refused) throw new RouteError('certificate_untrusted', 'The server certificate is not trusted.');
      }
      return response;
    } catch (error) {void response.body?.cancel().catch(() => {}); throw error;}
  };
}
