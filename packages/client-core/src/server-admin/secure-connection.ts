/**
 * Server settings › Remote access › Secure connection, in words, once for every client: whether
 * devices reach this server encrypted, and what the owner can do about it. A Portico Account
 * connection gives the server a certificate every device trusts, asked for and renewed by the
 * server itself; an owner's own certificate does the same job. Without either, plain HTTP still
 * works everywhere (it is never blocked), and this says how to turn encryption on.
 */
import type {CertificateStatus} from '../certificates.ts';
import type {ClaimStatus} from '../claim-onboarding.ts';
import type {CustomCertificateStatus} from '../server-administration.ts';
import type {MessageId} from '../../../i18n/src/index.ts';
import type {StatusTone} from './status-words.ts';

export type SecureConnectionState =
  /** A Portico certificate is installed and serving. */
  | 'on'
  /** The owner's own certificate is serving. */
  | 'ownCertificate'
  /** Connected to a Portico Account and turned on; the certificate is on its way. */
  | 'starting'
  /** Connected and turned on, but no certificate yet after a failure; the server keeps trying. */
  | 'problem'
  /** Connected, and the owner turned it off. */
  | 'off'
  /** The Portico Account connection is waiting for the owner's approval. */
  | 'waiting'
  /** No Portico Account connection and no certificate of the owner's own. */
  | 'notSetUp';

export type SecureConnectionView = Readonly<{
  state: SecureConnectionState;
  tone: StatusTone;
  badge: MessageId;
  body: MessageId;
  /** Where devices reach the server securely, when the server knows. */
  address?: string;
  /** Offer to connect the server to a Portico Account (the one step that turns it on). */
  canConnect: boolean;
}>;

/** Certificate states that mean the server has no installed Portico Account connection yet.
 * `unconfigured` is what the certificate service reports for a claim-required server
 * (`certificate_manager.go` maps that claim state to it), so it reads as unclaimed, not as off. */
const UNCLAIMED = new Set(['claim_required', 'claim_pending', 'authority_unavailable', 'unconfigured']);

/** Whether the certificate service has a Portico Account connection to work with. */
export function certificateClaimed(status: CertificateStatus | undefined): boolean {
  return !!status && !UNCLAIMED.has(status.state);
}

/** A claim that is connected, from the claim status the account panel reads. */
export function claimConnected(status: ClaimStatus | undefined): boolean {
  return !!status && (status.state === 'installed' || status.state === 'claimed' || status.state === 'connected');
}

/** A claim that waits for someone to approve it in their Portico Account. */
export function claimWaiting(status: ClaimStatus | undefined): boolean {
  return !!status && !claimConnected(status) && (status.approvalRequired || !!status.operation);
}

/**
 * The section's one status line. `certificate` is the certificate form's status (absent when
 * this server has no certificate service); `own` is the owner's certificate files, from the
 * connectivity document; `claim` is the Portico Account connection.
 */
export function secureConnectionView(input: {certificate?: CertificateStatus; own?: CustomCertificateStatus; claim?: ClaimStatus}): SecureConnectionView {
  const {certificate, own, claim} = input;
  const claimed = certificateClaimed(certificate) || claimConnected(claim);
  if (certificate?.tlsReady) {
    const address = certificate.routeUrl;
    return {state: 'on', tone: 'healthy', badge: 'settings.server.secure.badgeOn', body: address ? 'settings.server.secure.onAt' : 'settings.server.secure.on', ...(address ? {address: address.replace(/^https:\/\//, '').replace(/\/$/, '')} : {}), canConnect: false};
  }
  if (own?.serving) {
    return {state: 'ownCertificate', tone: own.state === 'ready' ? 'healthy' : 'warning', badge: 'settings.server.secure.badgeOn', body: own.domain ? 'settings.server.secure.ownAt' : 'settings.server.secure.own', ...(own.domain ? {address: own.domain} : {}), canConnect: false};
  }
  if (claimed && certificate) {
    if (!certificate.config.enabled) return {state: 'off', tone: 'neutral', badge: 'settings.server.secure.badgeOff', body: 'settings.server.secure.off', canConnect: false};
    if (certificate.errorCode || certificate.state === 'invalid' || certificate.state === 'expired') return {state: 'problem', tone: 'warning', badge: 'settings.server.secure.badgeProblem', body: 'web.network.certificateProblem', canConnect: false};
    return {state: 'starting', tone: 'accent', badge: 'settings.server.secure.badgeStarting', body: 'settings.server.secure.starting', canConnect: false};
  }
  if (claimWaiting(claim)) return {state: 'waiting', tone: 'warning', badge: 'settings.server.secure.badgeWaiting', body: 'settings.server.secure.waiting', canConnect: false};
  return {state: 'notSetUp', tone: 'neutral', badge: 'settings.server.secure.badgeOff', body: certificate ? 'settings.server.secure.notSetUp' : 'settings.server.secure.notSetUpOwnOnly', canConnect: !!certificate && !claimConnected(claim)};
}
