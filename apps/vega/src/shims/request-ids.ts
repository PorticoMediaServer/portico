import {randomBytes, uuid} from '../bridge/random';

/**
 * Shared services that are not handed an id source of their own make their request ids with
 * `crypto.randomUUID`, or build one from `crypto.getRandomValues` (client-core `randomId`). The
 * Apple and Android apps install the first at launch (`apps/native/src-next/platform/random-id.ts`),
 * because their JavaScript runtime has no `crypto`. This is the same thing for Vega, from the
 * system's random source (`bridge/random.ts`), and it is careful in one more way: whatever
 * `crypto` this runtime may already have is left exactly as it is, and nothing here can stop the
 * app from starting.
 */
type Crypto = {randomUUID?: () => string; getRandomValues?: <T extends ArrayBufferView>(array: T) => T};
const scope = globalThis as {crypto?: Crypto};

try {
  const held = scope.crypto;
  if (typeof held?.randomUUID !== 'function') {
    // The system's source is settled on before anything is put in its place (it reads `crypto`).
    randomBytes(1);
    const getRandomValues = <T extends ArrayBufferView>(array: T): T => {
      new Uint8Array(array.buffer, array.byteOffset, array.byteLength).set(randomBytes(array.byteLength));
      return array;
    };
    const randomUUID = () => uuid();
    if (!held) scope.crypto = {randomUUID, getRandomValues};
    else {
      // A runtime with a `crypto` of its own keeps it; it only gains what it lacks.
      try { held.randomUUID = randomUUID; } catch { /* a sealed object: tried below */ }
      if (typeof held.getRandomValues !== 'function') { try { held.getRandomValues = getRandomValues; } catch { /* as above */ } }
      if (typeof scope.crypto?.randomUUID !== 'function') scope.crypto = {randomUUID, getRandomValues: typeof held.getRandomValues === 'function' ? held.getRandomValues.bind(held) : getRandomValues};
    }
  }
} catch {
  // Services that are handed their own id source (playback, sign-in, queues) are unaffected.
}
