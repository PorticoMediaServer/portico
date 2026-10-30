/**
 * The Server pages' status lines in words, once for every client: whether the server can be
 * reached from outside, what the certificate is doing, what playback can deliver right now.
 * A code never reaches the screen (X-04); it stays in the logs.
 */
import {remoteAccessSummary, type RemoteAccess} from '../remote-access.ts';
import type {CertificateStatus} from '../certificates.ts';
import type {ConnectionDocument, ConnectivityStatus, CustomCertificateStatus} from '../server-administration.ts';
import type {PlaybackSettingsSnapshot} from '../playback-settings.ts';
import type {MessageId, MessageValues} from '../../../i18n/src/index.ts';
import {sentence} from './console-words.ts';

type Translate = (id: MessageId, values?: MessageValues) => string;
export type StatusTone = 'healthy' | 'warning' | 'danger' | 'accent' | 'neutral';

/** "Can people reach this server from outside?", in one sentence. Empty until the server has answered. */
export function remoteStatusLine(t: Translate, input: {unavailable: boolean; remote?: RemoteAccess; certificate?: CertificateStatus; connectivity?: ConnectivityStatus}): {tone: StatusTone; text: string} {
  const st = input.remote;
  if (input.unavailable) return {tone: 'neutral', text: t('settings.server.remote.notSetUp')};
  if (!st) return {tone: 'neutral', text: ''};
  const https = !!input.certificate?.tlsReady || input.connectivity?.tls.customCertificate?.state === 'ready';
  const address = input.certificate?.routeUrl || st.candidates.find(c => c.class === 'public' && c.state === 'verified')?.baseUrl || st.candidates.find(c => c.class !== 'lan')?.baseUrl || st.topology.public[0];
  switch (st.state) {
    case 'reachable': return {tone: 'healthy', text: [address ? t('settings.server.remote.reachable', {address: address.replace(/^https?:\/\//, '')}) : t('settings.server.remote.reachableNoAddress'), t(https ? 'settings.server.remote.https' : 'settings.server.remote.noHttps')].join(' · ')};
    case 'disabled': return {tone: 'neutral', text: t('settings.server.remote.off')};
    case 'checking': return {tone: 'accent', text: t('settings.server.remote.checking')};
    // The states the server reports while a route is not proven: the catalogue already says
    // what each one means, so the line uses that instead of calling every one unreachable.
    case 'claim_required':
    case 'lan_only':
    case 'manual_only': return {tone: 'neutral', text: remoteAccessSummary(st, t)};
    case 'certificate_pending': return {tone: 'accent', text: remoteAccessSummary(st, t)};
    case 'hosted_unavailable':
    case 'remote_unavailable': return {tone: 'warning', text: remoteAccessSummary(st, t)};
    default: return {tone: 'warning', text: t('settings.server.remote.unreachable')};
  }
}

/** Router mapping failures in plain words. */
export function mappingProblem(t: Translate, code: string): string {
  switch (code) {
    case 'mapping_not_authorized': return t('web.network.mapping.refused');
    case 'mapping_unsupported': return t('web.network.mapping.unsupported');
    case 'mapping_conflict': return t('web.network.mapping.conflict');
    default: return t('web.network.mapping.failed');
  }
}

/** Connectivity warnings in plain words. */
export function connectivityWarning(t: Translate, code: string): string {
  switch (code) {
    case 'secure_connections_required_without_tls': return t('web.network.warning.tlsRequired');
    case 'remote_sign_in_without_public_address': return t('web.network.warning.noPublicAddress');
    case 'lan_discovery_unsupported_on_this_host': return t('web.network.warning.noDiscovery');
    default: return t('web.network.warning.other');
  }
}

/**
 * The addresses worth listing: each once, without IPv6 link-local ones (fe80::/10), which only
 * work on the same wire and say nothing an owner can use. The server's own order is kept.
 */
export function listedAddresses<T extends Readonly<{address: string}>>(addresses: readonly T[]): readonly T[] {
  const seen = new Set<string>();
  return addresses.filter(a => {
    const address = a.address.trim().toLowerCase();
    if (/^fe[89ab][0-9a-f]:/.test(address) || seen.has(address)) return false;
    seen.add(address);
    return true;
  });
}

/** What kind of address the server answers on. */
export function addressKind(t: Translate, kind: string): string {
  return t(kind === 'lan' ? 'server.address.home' : kind === 'public' ? 'server.address.public' : 'server.address.thisMachine');
}

/** Playback › Right now: what the server can deliver, as label and value pairs. */
export function deliveryStatusRows(t: Translate, d: NonNullable<NonNullable<PlaybackSettingsSnapshot['observation']>['diagnostics']>): readonly (readonly [string, string])[] {
  return [
    [t('server.delivery.original'), d.directConfigured ? t('server.delivery.available') : t('server.delivery.notConfigured')],
    [t('server.delivery.converted'), d.hlsConfigured ? (d.lifecycle === 'running' ? t('server.delivery.available') : sentence(d.lifecycle)) : t('server.delivery.notConfigured')],
    [t('server.delivery.active'), d.activeConversionSessions == null ? t('server.delivery.notMeasured') : d.conversionSessionLimit != null ? t('server.delivery.activeOf', {count: d.activeConversionSessions, limit: d.conversionSessionLimit}) : String(d.activeConversionSessions)],
  ];
}

/** Where the owner is connected from right now and which side of the home-network list that is; nothing when the server could not say. */
export function connectionLine(t: Translate, connection: ConnectionDocument | undefined): {tone: StatusTone; text: string} | undefined {
  if (!connection?.clientAddress) return undefined;
  return {tone: 'neutral', text: t(connection.remote ? 'server.connection.away' : 'server.connection.home', {address: connection.clientAddress})};
}

const OWN_CERTIFICATE_PROBLEMS: Readonly<Record<string, MessageId>> = {
  custom_certificate_unreadable: 'web.connectivity.customCertUnreadable', custom_certificate_invalid: 'web.connectivity.customCertInvalid',
  custom_certificate_name_mismatch: 'web.connectivity.customCertMismatch', custom_certificate_expired: 'web.connectivity.customCertExpired',
};

/**
 * The owner's own certificate in one line: in use and until when, or what is wrong with the
 * files. When replacement files cannot be used the server keeps serving the pair that worked,
 * and the line says so; nothing when no certificate is set.
 */
export function ownCertificateLine(t: Translate, certificate: CustomCertificateStatus | undefined, date: (iso: string) => string): {tone: StatusTone; text: string} | undefined {
  if (!certificate || certificate.state === 'none') return undefined;
  if (certificate.state === 'ready') return {tone: 'healthy', text: certificate.notAfter ? t('web.connectivity.customCertReady', {date: date(certificate.notAfter)}) : t('server.ownCert.inUse')};
  const problem = t(OWN_CERTIFICATE_PROBLEMS[certificate.errorCode ?? ''] ?? 'server.ownCert.problem');
  return certificate.serving ? {tone: 'warning', text: t('server.ownCert.keptPrevious', {problem})} : {tone: 'danger', text: problem};
}
