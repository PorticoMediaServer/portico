import {pbkdf2, pbkdf2Async} from '@noble/hashes/pbkdf2';
import {sha256} from '@noble/hashes/sha2';
import {sameBytes, utf8} from '../../../../../vega/src/bridge/encoding';

/**
 * PBKDF2-HMAC-SHA256 for the profile trust vault's PIN key (200,000 rounds on every platform), as
 * the Fire TV bridge's `kdf.ts` does it, with the browser's own implementation (Web Crypto) in
 * place of the SDK's. JavaScript alone takes far too long on a television. The browser's answer
 * is trusted only after it has reproduced, once per launch, what the JavaScript implementation
 * gives for a short derivation; otherwise (or where Web Crypto is missing) the JavaScript
 * implementation is used: slow, and exactly right.
 */
const buffer = (bytes: Uint8Array): ArrayBuffer => bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) as ArrayBuffer;

async function native(subtle: SubtleCrypto, secret: Uint8Array, salt: Uint8Array, iterations: number, length: number): Promise<Uint8Array> {
  const key = await subtle.importKey('raw', buffer(secret), 'PBKDF2', false, ['deriveBits']);
  const derived = new Uint8Array(await subtle.deriveBits({name: 'PBKDF2', hash: 'SHA-256', salt: buffer(salt), iterations}, key, length * 8));
  if (derived.length !== length) throw new Error('unexpected length');
  return derived;
}

let checked: Promise<SubtleCrypto | null> | undefined;

function trustedNative(): Promise<SubtleCrypto | null> {
  checked ??= (async () => {
    try {
      const subtle = globalThis.crypto?.subtle;
      if (!subtle) return null;
      const secret = utf8('portico profile trust self-check '.repeat(4)), salt = utf8('a salt of some length, 32 bytes!');
      const expected = pbkdf2(sha256, secret, salt, {c: 600, dkLen: 64});
      return sameBytes(await native(subtle, secret, salt, 600, 64), expected) ? subtle : null;
    } catch {
      return null;
    }
  })();
  return checked;
}

export async function pbkdf2Sha256(secret: Uint8Array, salt: Uint8Array, iterations: number, length: number): Promise<Uint8Array> {
  const subtle = await trustedNative();
  if (subtle) {
    try { return await native(subtle, secret, salt, iterations, length); } catch { /* fall through to the reference */ }
  }
  return pbkdf2Async(sha256, secret, salt, {c: iterations, dkLen: length});
}

export const usesNativeKdf = async (): Promise<boolean> => (await trustedNative()) !== null;
