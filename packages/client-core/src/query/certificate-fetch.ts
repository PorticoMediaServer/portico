import {assertFirstContactIdentity} from '../index.ts';
import {knownServerRoutes, type AppSessionRecord} from '../app-sessions.ts';
import {RouteError, type RouteCrypto} from '../route-identity.ts';
import {bonjourProvesHome, type DiscoveryRoute} from '../route-connection.ts';
import {certificateOrigin, pendingCertificate, settleCertificate, type CertificateContext, type CertificatePlatform} from '../certificate-trust.ts';
import {revisionRequiredFetch} from './revision-required-fetch.ts';
type NativeCertificateFetchPlatform = {fetcher: typeof fetch; checkPlainHttp: (url: string) => void; isAccountAuthority: (url: string) => boolean;
  available: () => boolean; monitor: () => void; networkRevision: () => number; readSessions: () => Promise<AppSessionRecord>;
  browse: (signal: AbortSignal) => Promise<readonly unknown[]>; certificate: () => CertificatePlatform; crypto: () => RouteCrypto};

export function createNativeCertificateFetch(platform: NativeCertificateFetchPlatform, expected?: string): typeof fetch {return revisionRequiredFetch(async (input, options = {}) => {
  if (typeof input === 'string') platform.checkPlainHttp(input);
  const origin = typeof input === 'string' && !platform.isAccountAuthority(input) ? certificateOrigin(input) : undefined;
  if (origin && platform.available()) platform.monitor();
  const networkRevision = platform.networkRevision();
  const decision: CertificateContext = async (proof, fingerprint) => {
    const state = await platform.readSessions();
    if (options.signal?.aborted) throw new RouteError('cancelled', 'certificate_decision_cancelled');
    const knownRoutes = knownServerRoutes(state);
    assertFirstContactIdentity(origin!, proof, {linkId: expected, knownRoutes});
    // Only the exact proof's native TLS observation can qualify current-network
    // discovery. TXT fp remains the identity-key hash; a private address alone
    // never grants home trust (and CGNAT stays on the away path).
    const records = await platform.browse( options.signal ?? new AbortController().signal).catch(() => []);
    if (options.signal?.aborted || networkRevision !== platform.networkRevision()) throw new RouteError('cancelled', 'certificate_network_changed');
    const home = bonjourProvesHome(origin!, proof, fingerprint, records as readonly DiscoveryRoute[]);
    return {expected, home, knownServerIds: state.servers.map(value => value.connection.pin.serverId)};
  };
  // A pending code decision permits no normal request, including a public name
  // read. The proof client below is separate and has one fixed method/path.
  if (origin && pendingCertificate(origin)) throw new RouteError('identity_code_needed', 'identity_code_needed');
  try {
    const result = await platform.fetcher(input, options);
    // A Portico-pinned leaf can complete TLS while the system still refuses it. It must
    // pass the same identity decision, rather than masquerade as system trust.
    if (origin && platform.available() && await platform.certificate().refused(origin))
      await settleCertificate(origin, platform.certificate(), platform.crypto(), decision, options.signal ?? undefined);
    return result;
  } catch (error) {
    // An owner's own certificate is settled only for the owner's server, never the account service (NS-02).
    if (!origin || (error as {code?: unknown})?.code !== 'certificate_untrusted' || !platform.available()) throw error;
    await settleCertificate(origin, platform.certificate(), platform.crypto(), decision, options.signal ?? undefined);
    return platform.fetcher(input, options);
  }
});}
