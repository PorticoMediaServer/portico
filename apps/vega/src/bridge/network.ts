import {gcm} from '@noble/ciphers/aes';
import {ed25519, x25519} from '@noble/curves/ed25519';
import {hkdf} from '@noble/hashes/hkdf';
import {hmac} from '@noble/hashes/hmac';
import {sha256} from '@noble/hashes/sha2';
import {addEventListener as watchNetwork, type NetInfoState} from '@amazon-devices/keplerscript-netmgr-lib';
import {base64, base64Url, fromBase64, fromBase64Url, fromUtf8, isUuid, utf8} from './encoding';
import {ModuleEvents, failure, withEvents} from './events';
import {verifiedCredentialOrigin} from './credential-boundary';
import {randomBytes, uuid} from './random';
import {readRecord, serialized, writeRecord} from './storage';

/**
 * `NativeModules.PorticoNetwork` (`Native/PorticoNetwork.swift`): identity checks, the
 * television's sign-in key, and the state of the network path. Event: `portico.network`.
 *
 * Nearby discovery is the one part with no counterpart: the Vega SDK has no service-discovery
 * (mDNS) API, so a search finds nothing and nothing is advertised. Sign-in then works the way it
 * does on an Apple TV without Local Network access: by code, or by the server's address.
 */
const events = new ModuleEvents();
const invalid = new Error('invalid');

/** A secret held in the app's storage; made by `create` when there is none (or refused when
 * `create` is absent, so opening a response never makes a new key). */
function secret(account: string, create?: () => Uint8Array): Promise<Uint8Array> {
  return serialized('network:' + account, async () => {
    const held = await readRecord('network', account);
    if (held !== null) {
      const bytes = fromBase64(held);
      if (!bytes || bytes.length !== 32) throw invalid;
      return bytes;
    }
    if (!create) throw invalid;
    const made = create();
    await writeRecord('network', account, base64(made));
    return made;
  });
}

let stopWatching: (() => void) | undefined;
let lastPath: string | undefined;

/** One coarse word for the kind of link: the only thing about the path that leaves the device. */
function transportOf(state: NetInfoState): string {
  const type = String(state.type);
  return type === 'ethernet' ? 'ethernet' : type === 'wifi' ? 'wifi' : type === 'cellular' ? 'cellular' : 'unknown';
}

async function pathChanged(state: NetInfoState): Promise<void> {
  const transport = transportOf(state), online = state.isConnected === true;
  const details = state.details as null | {ipAddress?: unknown; subnet?: unknown; ssid?: unknown};
  const parts = [String(online), String(state.isInternetReachable), transport, String(details?.ipAddress ?? ''), String(details?.subnet ?? '')].sort();
  let generation: string;
  try {
    // A keyed digest of the path, so "the network changed" can be told without the addresses
    // themselves being held or sent anywhere.
    const key = await secret('path-hmac-v1', () => randomBytes(32));
    generation = base64Url(hmac(sha256, key, utf8(parts.join('\n'))));
    if (generation === lastPath) return;
    lastPath = generation;
  } catch {
    generation = uuid();
  }
  events.emit('portico.network', {generation, online, transport});
}

const codePattern = /^[ABCDEFGHJKMNPQRSTUVWXYZ23456789]{4}-[ABCDEFGHJKMNPQRSTUVWXYZ23456789]{4}$/;
const idPattern = /^[A-Za-z0-9_-]{1,128}$/;

export const PorticoNetwork = withEvents({
  sha256(payload: string): Promise<string> {
    try { return Promise.resolve(base64Url(sha256(fromBase64Url(payload)))); } catch { return Promise.reject(failure('crypto_input', 'Invalid identity data.')); }
  },

  /** An Ed25519 signature by `publicKey` over `payload` (all URL-safe base64). */
  verify(publicKey: string, signature: string, payload: string): Promise<boolean> {
    try {
      const key = fromBase64Url(publicKey, 32), tag = fromBase64Url(signature, 64), message = fromBase64Url(payload);
      let valid = false;
      try { valid = ed25519.verify(tag, message, key); } catch { valid = false; }
      if (valid) { try { verifiedCredentialOrigin(JSON.parse(fromUtf8(message) ?? 'null')); } catch { /* other signed documents */ } }
      return Promise.resolve(valid);
    } catch {
      return Promise.reject(failure('crypto_input', 'Invalid signed identity.'));
    }
  },

  startMonitoring(): void {
    if (stopWatching) return;
    try {
      stopWatching = watchNetwork(state => { void pathChanged(state); });
    } catch (error) {
      // Without the network service the app still works; it just isn't told when the path changes.
      console.warn('Network monitoring is unavailable.', error);
      stopWatching = () => {};
    }
  },

  browse(identifier: string, kind: string): Promise<unknown[]> {
    if (typeof identifier !== 'string' || !identifier || identifier.length > 64 || !['servers', 'tv', 'players'].includes(kind)) {
      return Promise.reject(failure('discovery_busy', 'Discovery is already in progress.'));
    }
    return Promise.resolve([]);
  },
  cancelBrowse(_identifier: string): void {},

  /** The public half of this sign-in attempt's X25519 key; the private half never leaves storage. */
  async tvPublicKey(requestId: string): Promise<string> {
    try {
      if (!isUuid(requestId)) throw invalid;
      const key = await secret('tv-v1:' + requestId, () => randomBytes(32));
      return base64Url(x25519.getPublicKey(key));
    } catch {
      throw failure('secure_storage', 'TV setup identity could not be saved.');
    }
  },

  /**
   * Opens the sign-in an approving device sealed for this television: X25519 with the issuer's
   * key, HKDF-SHA256 over the envelope's salt, AES-256-GCM, all bound to the issuer, the request
   * and both public keys. Anything that does not match is refused and installs nothing.
   */
  async openTVGrant(requestId: string, envelope: Record<string, unknown>): Promise<string> {
    try {
      const {issuer, publicKey: serverKey, recipientKey: recipient, salt, nonce, ciphertext} = envelope ?? {};
      if (!isUuid(requestId) || envelope.kind !== 'portico.tv-grant' || envelope.version !== 1 || envelope.requestId !== requestId) throw invalid;
      if (typeof issuer !== 'string' || typeof serverKey !== 'string' || typeof recipient !== 'string' || typeof salt !== 'string' || typeof nonce !== 'string' || typeof ciphertext !== 'string') throw invalid;
      const local = await secret('tv-v1:' + requestId);
      if (base64Url(x25519.getPublicKey(local)) !== recipient) throw invalid;
      const peer = fromBase64Url(serverKey, 32);
      const context = utf8(['portico.tv-grant.v1', issuer, requestId, recipient, serverKey].join('\u0000'));
      const shared = x25519.getSharedSecret(local, peer);
      const key = hkdf(sha256, shared, fromBase64Url(salt, 32), context, 32);
      const sealed = fromBase64Url(ciphertext, undefined, 65536);
      if (sealed.length <= 16) throw invalid;
      const plain = gcm(key, fromBase64Url(nonce, 12), context).decrypt(sealed);
      const json = fromUtf8(plain);
      if (json === null) throw invalid;
      return json;
    } catch {
      throw failure('tv_grant_invalid', 'The TV setup response could not be verified. No sign-in was installed.');
    }
  },

  forgetTVKey(requestId: string): void {
    if (!isUuid(requestId)) return;
    void serialized('network:tv-v1:' + requestId, () => writeRecord('network', 'tv-v1:' + requestId, null)).catch(() => {});
  },

  /** The hint is checked as on Apple, then not advertised (no discovery API): resolves false. */
  advertiseTV(value: Record<string, unknown>): Promise<boolean> {
    const expiry = typeof value?.expiresAt === 'string' ? Date.parse(value.expiresAt) : NaN;
    const left = expiry - Date.now();
    if (typeof value?.code !== 'string' || !codePattern.test(value.code) || !isUuid(value.requestId) || typeof value.issuer !== 'string' || utf8(value.issuer).length >= 240 ||
        typeof value.publicKey !== 'string' || value.publicKey.length !== 43 || !(left > 0 && left <= 605000)) {
      return Promise.reject(failure('tv_discovery_invalid', 'Invalid TV discovery hint.'));
    }
    return Promise.resolve(false);
  },
  advertisePlayer(value: Record<string, unknown>): Promise<boolean> {
    if (typeof value?.receiverId !== 'string' || !idPattern.test(value.receiverId) || typeof value.serverId !== 'string' || !idPattern.test(value.serverId) || !['tv', 'phone', 'tablet', 'computer'].includes(value.kind as string)) {
      return Promise.reject(failure('player_discovery_invalid', 'Invalid player advertisement.'));
    }
    return Promise.resolve(false);
  },
  stopPlayerAdvertisement(): void {},
  stopTVAdvertisement(): void {},
}, events);
