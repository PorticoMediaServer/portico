import {Crypto, DigestAlgorithm, KeyPurpose} from '@amazon-devices/keplercrypto';
import {pbkdf2, pbkdf2Async} from '@noble/hashes/pbkdf2';
import {sha256} from '@noble/hashes/sha2';
import {sameBytes, utf8} from './encoding';

/**
 * PBKDF2-HMAC-SHA256 for the profile trust vault's PIN key (200,000 rounds on every platform).
 *
 * In JavaScript alone that takes far too long on a television, so the SDK's native
 * implementation does the work. It is trusted only after it has reproduced, once per launch, what
 * the JavaScript implementation gives for a short derivation; if it does not (or is missing), the
 * JavaScript implementation is used: slow, and exactly right.
 */
const buffer = (bytes: Uint8Array): ArrayBuffer => bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) as ArrayBuffer;

async function native(crypto: Crypto, secret: Uint8Array, salt: Uint8Array, iterations: number, length: number): Promise<Uint8Array> {
  const keys = crypto.makeSymmetricKeyBuilder();
  keys.bits = secret.length * 8;
  keys.exportable = false;
  keys.purposes = [KeyPurpose.DERIVE];
  const key = await keys.buildFromRaw(buffer(secret));
  const builder = crypto.makePbkdf2ContextBuilder();
  builder.iterations = iterations;
  builder.salt = buffer(salt);
  builder.hashAlgorithm = crypto.getDigestAlgorithmByName(DigestAlgorithm.SHA256);
  const derived = new Uint8Array(await builder.buildKeyDerivationContext(key).deriveBits(length));
  if (derived.length !== length) throw new Error('unexpected length');
  return derived;
}

let checked: Promise<Crypto | null> | undefined;

/** The SDK's implementation, once it has matched the reference on a derivation of its own: a
 * secret longer than one hash block and an output of two blocks, as the vault's are. */
function trustedNative(): Promise<Crypto | null> {
  checked ??= (async () => {
    try {
      const crypto = new Crypto();
      const secret = utf8('portico profile trust self-check '.repeat(4)), salt = utf8('a salt of some length, 32 bytes!');
      const expected = pbkdf2(sha256, secret, salt, {c: 600, dkLen: 64});
      return sameBytes(await native(crypto, secret, salt, 600, 64), expected) ? crypto : null;
    } catch {
      return null;
    }
  })();
  return checked;
}

export async function pbkdf2Sha256(secret: Uint8Array, salt: Uint8Array, iterations: number, length: number): Promise<Uint8Array> {
  const crypto = await trustedNative();
  if (crypto) {
    try { return await native(crypto, secret, salt, iterations, length); } catch { /* fall through to the reference */ }
  }
  return pbkdf2Async(sha256, secret, salt, {c: iterations, dkLen: length});
}

/** Whether the native implementation is in use (for diagnostics). */
export const usesNativeKdf = async (): Promise<boolean> => (await trustedNative()) !== null;
