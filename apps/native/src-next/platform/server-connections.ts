import {accountExpectation, nativeAccountRead} from './account-work';
import type {CredentialAccessExpectation} from '../../../../packages/client-core/src/credentials';
import {nativeReadingPolicy, nativeReadingResponse, nativeReadingStreamResponse} from './reading-response';
import {revisionRequiredFetch} from './revision-required-fetch';
import { AppState, NativeEventEmitter, NativeModules, Platform } from 'react-native';
import {
  HttpLocalApi,
  assertFirstContactIdentity,
  type CredentialSessionService,
  type LocalSession,
} from '../../../../packages/client-core/src/index.ts';
import {
  RouteError,
  routeBase64,
  routeBytes,
  routeOrigin,
  routeJSON,
  type RouteCrypto,
} from '../../../../packages/client-core/src/route-identity.ts';
import {
  type RouteConnection,
  type DiscoveryRoute,
  sessionRoute,
  bonjourProvesHome,
} from '../../../../packages/client-core/src/route-connection.ts';
import {
  forgetNativeSession,
  restoreNativeSession,
  type ConnectionEnvironment,
  type HostedConnectionSource,
} from '../../../../packages/client-core/src/server-connections.ts';
import {nativeAppSessions} from './app-session-storage';
import {knownServerRoutes, type NewServerConnection} from '../../../../packages/client-core/src/app-sessions';
import { routePath } from '../../../../packages/contracts/src/routes.ts';
import { checkApplePlainHttp } from './apple-plain-http';
import { loadConnectionPreference, nativeConnectionPolicy } from './connection-preference';
import { isAccountAuthority } from './account-authority';
import {homeActionDiagnostics} from './home-action-diagnostics';
import { certificateOrigin, parseSavedCertificates, savedCertificateOrigin, settleCertificate, pendingCertificate, type CertificateContext, type CertificatePlatform, type SavedCertificate } from './certificate-trust';
type SubtitleUploadFile = {uri: string; name: string; type: string};
/** RN's FormData exposes descriptors, never file bytes. No other object body is bridged. */
function subtitleUploadParts(body: unknown): {metadata: string; file: SubtitleUploadFile; companion?: SubtitleUploadFile} | undefined {
  try {
    if (typeof FormData !== 'function' || !(body instanceof FormData)) return;
    const getParts = (FormData.prototype as FormData & {getParts?: () => unknown}).getParts;
    if (typeof getParts !== 'function') return;
    const parts: unknown = getParts.call(body);
    if (!Array.isArray(parts) || (parts.length !== 2 && parts.length !== 3)) return;
    const metadata = parts[0];
    if (!metadata || typeof metadata !== 'object' || metadata.fieldName !== 'metadata' || typeof metadata.string !== 'string' ||
        Object.keys(metadata).some(key => !['fieldName', 'string', 'headers'].includes(key))) return;
    const filePart = (part: unknown, fieldName: string): part is SubtitleUploadFile => {
      if (!part || typeof part !== 'object') return false;
      const value = part as Record<string, unknown>;
      return value.fieldName === fieldName && typeof value.uri === 'string' && value.uri.startsWith('file://') &&
        typeof value.name === 'string' && value.name.length > 0 && value.type === 'application/octet-stream' &&
        Object.keys(value).every(key => ['fieldName', 'uri', 'name', 'type', 'headers'].includes(key));
    };
    if (!filePart(parts[1], 'file') || (parts.length === 3 && !filePart(parts[2], 'companion'))) return;
    return {metadata: metadata.string, file: parts[1], companion: parts[2]};
  } catch {return undefined;}
}
/** One request through the native transport, as it is. */
const nativeRequest: typeof fetch = async (input, options = {}) => {
  const upload = options.body != null && typeof options.body !== 'string' ? subtitleUploadParts(options.body) : undefined;
  if (
    typeof input !== 'string' ||
    options.signal?.aborted ||
    (options.body != null && typeof options.body !== 'string' && !upload)
  )
    throw new RouteError('cancelled', 'Connection request cancelled.');
  const id = NativeModules.PorticoRuntime.playbackRequestId(),
    cancel = () => NativeModules.PorticoRuntime.cancelQueueRequest(id);
  options.signal?.addEventListener('abort', cancel, { once: true });
  try {
    const headers: Record<string, string> = {};
    new Headers(options.headers).forEach((v, k) => (headers[k] = v));
    const reading = nativeReadingPolicy(input, options.method ?? 'GET', headers);
    let raw: {status: number; headers: Record<string, string>; body?: string; bodyBase64?: string};
    const diagnostic = homeActionDiagnostics.request(options.method ?? 'GET', input);
    try {
      raw = upload
        ? await NativeModules.PorticoRuntime.networkUploadRequest(id, input, options.method ?? 'GET', headers,
          upload.metadata, upload.file.uri, upload.file.name, upload.companion?.uri ?? null, upload.companion?.name ?? null)
        : reading?.sequential
        ? await NativeModules.PorticoRuntime.networkStreamOpen(id, input, headers)
        : reading?.binaryLimit !== undefined
        ? await NativeModules.PorticoRuntime.networkBytesRequest(id, input, options.method ?? 'GET', headers, reading.binaryLimit)
        : await NativeModules.PorticoRuntime.networkRequest(
        id,
        input,
        options.method ?? 'GET',
        headers,
        options.body ?? null,
      );
    } catch (error) {
      homeActionDiagnostics.rejected(diagnostic, error);
      // The caller gave up on it: that is a cancellation, whatever the transport said as it stopped.
      if (options.signal?.aborted) throw new RouteError('cancelled', 'Connection request cancelled.');
      throw error;
    }
    homeActionDiagnostics.response(diagnostic, raw.status);
    if (options.signal?.aborted)
      throw new RouteError('cancelled', 'Connection request cancelled.');
    if (reading?.sequential) {
      // The pull body now owns cancellation until EOF, not merely headers.
      options.signal?.removeEventListener('abort', cancel);
      return nativeReadingStreamResponse(raw, {read: () => NativeModules.PorticoRuntime.networkStreamRead(id), cancel}, options.signal ?? undefined);
    }
    if (reading) return nativeReadingResponse(raw, reading.binaryLimit, options.signal ?? undefined);
    return new Response(
      [204, 205, 304].includes(raw.status) ? null : raw.body,
      { status: raw.status, headers: raw.headers },
    );
  } finally {
    options.signal?.removeEventListener('abort', cancel);
  }
};
/**
 * The native transport for every API request. On Apple, plain HTTP to an address off the local
 * network is refused here with the reason (the system would refuse it anyway). A request that
 * meets the owner's own certificate settles it (`certificate-trust.ts`) and is retried once.
 */
function nativeFetchForIdentity(expected?: string): typeof fetch {return revisionRequiredFetch(async (input, options = {}) => {
  if (Platform.OS === 'ios' && typeof input === 'string') checkApplePlainHttp(input);
  const origin = typeof input === 'string' && !isAccountAuthority(input) ? certificateOrigin(input) : undefined;
  if (origin && certificateTrustAvailable()) ensureMonitoring();
  const networkRevision = revision;
  const decision: CertificateContext = async (proof, fingerprint) => {
    const state = await nativeAppSessions.read();
    if (options.signal?.aborted) throw new RouteError('cancelled', 'certificate_decision_cancelled');
    const knownRoutes = knownServerRoutes(state);
    assertFirstContactIdentity(origin!, proof, {linkId: expected, knownRoutes});
    // Only the exact proof's native TLS observation can qualify current-network
    // discovery. TXT fp remains the identity-key hash; a private address alone
    // never grants home trust (and CGNAT stays on the away path).
    const records = await browseNearby('servers', options.signal ?? new AbortController().signal).catch(() => []);
    if (options.signal?.aborted || networkRevision !== revision) throw new RouteError('cancelled', 'certificate_network_changed');
    const home = bonjourProvesHome(origin!, proof, fingerprint, records as readonly DiscoveryRoute[]);
    return {expected, home, knownServerIds: state.servers.map(value => value.connection.pin.serverId)};
  };
  // A pending code decision permits no normal request, including a public name
  // read. The proof client below is separate and has one fixed method/path.
  if (origin && pendingCertificate(origin)) throw new RouteError('identity_code_needed', 'identity_code_needed');
  try {
    const result = await nativeRequest(input, options);
    // A Portico-pinned leaf can complete TLS while the system still refuses it. It must
    // pass the same identity decision, rather than masquerade as system trust.
    if (origin && certificateTrustAvailable() && await nativeCertificatePlatform.refused(origin))
      await settleCertificate(origin, nativeCertificatePlatform, nativeRouteCrypto, decision, options.signal ?? undefined);
    return result;
  } catch (error) {
    // An owner's own certificate is settled only for the owner's server, never the account service (NS-02).
    if (!origin || (error as {code?: unknown})?.code !== 'certificate_untrusted' || !certificateTrustAvailable()) throw error;
    await settleCertificate(origin, nativeCertificatePlatform, nativeRouteCrypto, decision, options.signal ?? undefined);
    return nativeRequest(input, options);
  }
});}
/** Root47's credential-free contact never enters certificate settlement, discovery,
 * route refresh or ordinary connection admission. Only the two canonical public exchanges
 * at the originally captured origin are permitted; existing native TLS validation remains. */
export function nativeOwnerRecoveryFetch(capturedOrigin: string, isCurrent: () => boolean): typeof fetch {
  const origin = routeOrigin(capturedOrigin);
  if (isAccountAuthority(origin)) throw new RouteError('invalid_address', 'Choose the server address.');
  const check = (signal?: AbortSignal | null) => {
    if (!signal || signal.aborted || !isCurrent()) throw new RouteError('cancelled', 'Owner recovery contact cancelled.');
    if (pendingCertificate(origin)) throw new RouteError('identity_code_needed', 'identity_code_needed');
  };
  return async (input, options = {}) => {
    check(options.signal);
    const method = options.method ?? 'GET';
    if (typeof input !== 'string' ||
        !(input === origin + routePath('prove_network_identity') && method === 'POST' ||
          input === origin + routePath('get_system') && method === 'GET' && options.body == null))
      throw new RouteError('invalid_address', 'Owner recovery contact is limited to the captured server.');
    const headers = new Headers(options.headers);
    headers.forEach((_value, name) => {
      if (name !== 'accept' && name !== 'content-type')
        throw new RouteError('invalid_request', 'Owner recovery contact carries no credentials.');
    });
    if (Platform.OS === 'ios') checkApplePlainHttp(input);
    const response = await nativeRequest(input, {...options, headers, credentials: 'omit', redirect: 'error', referrerPolicy: 'no-referrer'});
    try {
      check(options.signal);
      // The strict path cannot settle an OS-refused leaf or install new native certificate trust.
      if (certificateTrustAvailable()) {
        const refused = await nativeCertificatePlatform.refused(origin);
        check(options.signal);
        if (refused) throw new RouteError('certificate_untrusted', 'The server certificate is not trusted.');
      }
      return response;
    } catch (error) {void response.body?.cancel().catch(() => {}); throw error;}
  };
}

export const nativeNetworkFetch = nativeFetchForIdentity();
/** Expected identity stays captured for every route probe and authenticated request. */
export const nativeServerFetch = (serverId: string) => nativeFetchForIdentity(serverId);
/** Whether this device can trust an owner's own certificate (Android and Apple; not Vega). */
export function certificateTrustAvailable(): boolean {
  return typeof NativeModules.PorticoNetwork?.refusedCertificate === 'function';
}
/** Every certificate this device trusts at an address (none on a device that cannot trust one). */
export async function savedCertificates(): Promise<SavedCertificate[]> {
  if (typeof NativeModules.PorticoNetwork?.pinnedCertificates !== 'function') return [];
  return parseSavedCertificates(await NativeModules.PorticoNetwork.pinnedCertificates());
}
/** Stops trusting the certificate saved at `address`: the next connection there checks it again. */
export function forgetSavedCertificate(address: string): void {
  NativeModules.PorticoNetwork?.forgetCertificate?.(savedCertificateOrigin(address));
}
/** Confirmation removes only this displayed tuple, atomically with native pin writers. */
export async function forgetCapturedCertificate(value: SavedCertificate): Promise<void> {
  const parsed = parseSavedCertificates([value]);
  const remove = NativeModules.PorticoNetwork?.forgetCapturedCertificate;
  if (parsed.length !== 1 || typeof remove !== 'function') throw new RouteError('cancelled', 'certificate_changed');
  const captured = parsed[0]!;
  if (await remove(savedCertificateOrigin(captured.address), captured.fingerprint, captured.serverId) !== true)
    throw new RouteError('cancelled', 'certificate_changed');
}
const proofTLSLeaves = new WeakMap<Response, string>();
/** Native TLS metadata for this exact proof Response, never an HTTP header or TXT. */
export function nativeProofTLSLeaf(response: Response): string | undefined {return proofTLSLeaves.get(response);}
export const nativeCertificatePlatform: CertificatePlatform = {
  refused: async origin => {
    const found = await NativeModules.PorticoNetwork.refusedCertificate(origin);
    return typeof found?.fingerprint === 'string' ? found.fingerprint : undefined;
  },
  pinned: async origin => {
    const found = await NativeModules.PorticoNetwork.pinnedCertificate(origin);
    return typeof found?.fingerprint === 'string' && typeof found?.serverId === 'string' ? {fingerprint: found.fingerprint, serverId: found.serverId} : undefined;
  },
  pin: async (origin, pin) => {
    await NativeModules.PorticoNetwork.pinCertificate(origin, pin.fingerprint, pin.serverId);
  },
  // The identity proof only: its exact address, method and body, over the one certificate.
  observedTLSLeaf: nativeProofTLSLeaf,
  proofFetcher: (origin, fingerprint) => async (input, options = {}) => {
    if (options.signal?.aborted) throw new RouteError('cancelled', 'certificate_proof_cancelled');
    if (typeof input !== 'string' || input !== origin + routePath('prove_network_identity') || options.method !== 'POST' || typeof options.body !== 'string')
      throw new RouteError('cancelled', 'Connection request cancelled.');
    const raw = await NativeModules.PorticoNetwork.proofRequest(origin, fingerprint, options.body);
    if (options.signal?.aborted) throw new RouteError('cancelled', 'Connection request cancelled.');
    const response = new Response(raw.body, {status: raw.status, headers: raw.headers});
    if (typeof raw.tlsLeafFingerprint !== 'string' || !/^[A-Za-z0-9_-]{43}$/.test(raw.tlsLeafFingerprint) || raw.tlsLeafFingerprint !== fingerprint)
      throw new RouteError('certificate_unverified', 'certificate_unverified');
    proofTLSLeaves.set(response, raw.tlsLeafFingerprint);
    return response;
  },
};
export const nativeRouteCrypto: RouteCrypto = {
  random: n => {
    const values = NativeModules.PorticoRuntime.playbackRandomBytes(n);
    if (
      !Array.isArray(values) ||
      values.length !== n ||
      values.some(v => !Number.isInteger(v) || v < 0 || v > 255)
    )
      throw new RouteError(
        'random_unavailable',
        'Secure connection verification is unavailable.',
      );
    return new Uint8Array(values);
  },
  sha256: async data =>
    routeBytes(await NativeModules.PorticoNetwork.sha256(routeBase64(data)), 32),
  verify: async (publicKey, signature, payload) =>
    !!(await NativeModules.PorticoNetwork.verify(
      routeBase64(publicKey),
      routeBase64(signature),
      routeBase64(payload),
    )),
};
export async function browseNearby(
  kind: 'servers' | 'tv' | 'players',
  signal: AbortSignal,
): Promise<readonly unknown[]> {
  if (signal.aborted) throw new RouteError('cancelled', 'Discovery cancelled.');
  const id = NativeModules.PorticoRuntime.playbackRequestId(),
    cancel = () => NativeModules.PorticoNetwork.cancelBrowse(id);
  signal.addEventListener('abort', cancel, { once: true });
  try {
    const result = await NativeModules.PorticoNetwork.browse(id, kind);
    if (signal.aborted)
      throw new RouteError('cancelled', 'Discovery cancelled.');
    return Array.isArray(result) ? result : [];
  } catch (e) {
    if (
      typeof e === 'object' &&
      e &&
      (e as { code?: string }).code === 'permission_denied'
    )
      throw new RouteError(
        'permission_denied',
        'Local Network access is denied. Manual addresses and TV codes still work.',
      );
    throw e;
  } finally {
    signal.removeEventListener('abort', cancel);
  }
}
// Loaded before the first saved connection is read, so a race never runs looser than the setting.
void loadConnectionPreference();
/** `/v1/networking/connection` without credentials, for connection details and an insecure route's
 * home check (route-connection.ts `readConnection`). */
async function readNativeConnection(origin: string, signal: AbortSignal): Promise<unknown> {
  return routeJSON(await nativeNetworkFetch(routeOrigin(origin) + routePath('get_connection'), {method: 'GET', signal, credentials: 'omit', redirect: 'error', referrerPolicy: 'no-referrer', headers: {Accept: 'application/json'}}), signal, 8192);
}
const nativeEnvironment = {
  fetcher: nativeNetworkFetch,
  crypto: nativeRouteCrypto,
  policy: nativeConnectionPolicy,
  readConnection: readNativeConnection,
  onProbeFailure: homeActionDiagnostics.probeFailure,
  discover: async (signal: AbortSignal) =>
    (await browseNearby('servers', signal)) as readonly DiscoveryRoute[],
};
/** Read the existing protected record once, unchanged. Only route-source shape
 * crosses the diagnostic boundary; never an origin, pin, envelope or credential. */
function observedRouteStorage(storage: ConnectionEnvironment['storage']): ConnectionEnvironment['storage'] {
  return {
    read: async () => {
      try {
        await loadConnectionPreference();
        const record = await storage.read();
        homeActionDiagnostics.routeMemory({routeSource: !record ? 'none' : record.hosted ? 'account' : 'local',
          signedCurrent: !!record?.routes.current, signedPrevious: !!record?.routes.previous,
          pairedCount: record?.routes.paired.length ?? 0, lastGood: !!record?.routes.lastGood});
        return record;
      } catch (error) {homeActionDiagnostics.error('route-memory', error); throw error;}
    },
    change: (update, beforeCommit) => storage.change(update, beforeCommit),
  };
}
/** Capture one server for the whole runtime. A later switch never retargets its refresh writes. */
// Each environment's probe reports carry its own ordinal: a report that repeats at steady state
// names the one connection that keeps re-resolving (P2-74 round 4b).
let environments = 0;
const probeReports = (kind: 'server' | 'selected') => {
  const env = ++environments;
  return {onProbeFailure: (failure: Parameters<typeof homeActionDiagnostics.probeFailure>[0]) => homeActionDiagnostics.probeFailure(failure, {env, kind})};
};
export function nativeServerEnvironment(serverId: string, admission?: NewServerConnection): ConnectionEnvironment {
  return {...nativeEnvironment, ...probeReports('server'), fetcher: nativeServerFetch(serverId), storage: observedRouteStorage(nativeAppSessions.connectionStorage(serverId, admission))};
}
export function nativeSelectedConnectionEnvironment(): ConnectionEnvironment {
  return {...nativeEnvironment, ...probeReports('selected'), storage: observedRouteStorage(nativeAppSessions.selectedStorage())};
}

let activeApi: HttpLocalApi | undefined,
  transport = 'unknown',
  pathOnline: boolean | undefined;
let active: RouteConnection | undefined,
  monitoring = false,
  revision = 0;
export function trackNativeConnection(api: HttpLocalApi) {
  const next = api.getRouteConnection();
  if (active && active !== next) {
    const retired = active;
    setTimeout(() => retired.dispose(), 16000);
  }
  active = next;
  // Which surface this is and what it is connected over scope the viewer's quality preferences.
  // Both are plain declarations; the server decides what they mean.
  activeApi = api;
  api.setDeviceClass(Platform.isTV ? 'television' : 'mobile');
  api.setTransportClass(transport);
  active?.setForeground(AppState.currentState === 'active');
  ensureMonitoring();
}
/** The last native path fact, or undefined until the monitor has reported one.
 * A true path proves only a default network with Internet capability, never
 * that this server, its identity or the Internet is reachable. */
export function deviceOnline(): boolean | undefined {return pathOnline;}
const networkListeners = new Set<(online: boolean | undefined) => void>();
function ensureMonitoring() {
  if (monitoring) return;
  monitoring = true;
  // A new monitor has no fact yet; an old boolean never carries over.
  pathOnline = undefined;
  new NativeEventEmitter(NativeModules.PorticoNetwork).addListener(
    'portico.network',
    (event?: {online?: unknown; transport?: string}) => {
      pathOnline = typeof event?.online === 'boolean' ? event.online : undefined;
      transport = event?.transport ?? transport;
      activeApi?.setTransportClass(transport);
      active?.networkChanged(String(++revision));
      for (const listener of networkListeners) listener(pathOnline);
    },
  );
  NativeModules.PorticoNetwork.startMonitoring();
  let previous = AppState.currentState;
  AppState.addEventListener('change', state => {
    active?.setForeground(state === 'active');
    if (state === 'active' && previous !== 'active')
      active?.foregroundActivity();
    previous = state;
  });
}
/** Called when the device's network path changes, with the same unknown/true/false
 * fact as deviceOnline(). A listener never receives a stale value from a prior monitor. */
export function onNetworkChange(listener: (online: boolean | undefined) => void): () => void {
  ensureMonitoring();
  networkListeners.add(listener);
  return () => {
    networkListeners.delete(listener);
  };
}
export function nativeHostedSource(
  origin: string,
  credentials: CredentialSessionService | (() => CredentialSessionService),
  accountId: string,
  expected?: CredentialAccessExpectation,
): HostedConnectionSource {
  const captured = expected && Object.freeze({...expected});
  return {
    origin,
    accountId,
    request: async <T>(path: string, signal: AbortSignal) => {
      if (signal.aborted) throw new RouteError('cancelled', 'account_route_cancelled');
      const owner = typeof credentials === 'function' ? credentials() : credentials;
      const original = owner.getSnapshot().session;
      if (!original || original.account.id !== accountId || captured && (captured.accountId !== accountId || original.familyId !== captured.familyId))
        throw new RouteError('account_changed', 'Your Portico Account changed.');
      return nativeAccountRead(owner, session => new HttpLocalApi(origin, session.accessToken, nativeNetworkFetch)
        .request<T>(path, 'GET', undefined, signal), {expected: captured ?? accountExpectation(original), signal,
          isCurrent: () => !signal.aborted});
    },
  };
}
export async function restoreNativeConnection(
  origin: string,
  credentials: CredentialSessionService | (() => CredentialSessionService) | undefined,
  expected: LocalSession,
  env = nativeServerEnvironment(expected.viewer.serverId),
  signal?: AbortSignal,
) {
  return restoreNativeSession(
    env,
    record =>
      record.hosted && credentials && record.hosted.origin === origin
        ? nativeHostedSource(origin, credentials, record.hosted.accountId)
        : undefined,
    expected,
    signal,
  );
}
export async function forgetNativeConnection(session: LocalSession) {
  const captured = sessionRoute(session.accessToken);
  const env = nativeServerEnvironment(session.viewer.serverId);
  await forgetNativeSession(env, session);
  const retained = await env.storage.read();
  if (retained && sessionRoute(retained.session.accessToken) === captured)
    return;
  if (active === captured) active = undefined;
  setTimeout(() => {
    if (active !== captured) captured?.dispose();
  }, 16000);
}

/** Play on: a signed-in Portico player found on this network (opaque ids only). */
export type NearbyAdvert = {receiverId: string; serverId: string; name: string; kind: 'tv' | 'phone' | 'tablet' | 'computer'};

/** Play on: this device is a player others may send to (only while it is ready to receive). */
export function advertisePlayer(advert: NearbyAdvert): void {
  void NativeModules.PorticoNetwork?.advertisePlayer?.(advert)?.catch?.(() => {});
}
export function stopPlayerAdvertisement(): void {
  NativeModules.PorticoNetwork?.stopPlayerAdvertisement?.();
}
