import {NativeModules} from 'react-native';

/**
 * Hermes has no Web Crypto. Shared code makes its request ids with `crypto.randomUUID` when it
 * exists (client-core `randomId`), and its mutation keys and tokens with `crypto.getRandomValues`
 * (client-core `newUserMutationKey`, built when the shell starts). The app provides both once:
 * the UUID from the native source that already makes playback request ids, the bytes from the
 * system's secure random source (`playbackRandomBytes`, at most 64 per call). Without them, the
 * signed-in start throws before Home and a shared service built without its own id source (the
 * downloads service) throws before it sends anything. A `crypto` the runtime already has keeps
 * what it has.
 */
type Runtime = {playbackRequestId?: () => string; playbackRandomBytes?: (count: number) => unknown};
type Crypto = {randomUUID?: () => string; getRandomValues?: <T extends ArrayBufferView | null>(array: T) => T};
const runtime = NativeModules.PorticoRuntime as undefined | Runtime;
const scope = globalThis as {crypto?: Crypto};

const additions: Crypto = {};
if (typeof scope.crypto?.randomUUID !== 'function' && typeof runtime?.playbackRequestId === 'function') {
  const source = runtime.playbackRequestId.bind(runtime);
  additions.randomUUID = () => source();
}
if (typeof scope.crypto?.getRandomValues !== 'function' && typeof runtime?.playbackRandomBytes === 'function') {
  const source = runtime.playbackRandomBytes.bind(runtime);
  additions.getRandomValues = <T extends ArrayBufferView | null>(array: T): T => {
    if (!array) return array;
    const out = new Uint8Array(array.buffer, array.byteOffset, array.byteLength);
    for (let at = 0; at < out.length; at += 64) {
      const n = Math.min(64, out.length - at), values = source(n);
      if (!Array.isArray(values) || values.length !== n || values.some(v => !Number.isInteger(v) || v < 0 || v > 255))
        throw new Error('Secure random bytes are unavailable.');
      out.set(values as number[], at);
    }
    return array;
  };
}
if (additions.randomUUID || additions.getRandomValues) scope.crypto = Object.assign(scope.crypto ?? {}, additions);
