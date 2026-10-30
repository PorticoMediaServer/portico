import {randomId} from '@core/random-id.ts';
import {setRouteCryptoFallback} from '@core/route-identity.ts';
import {sha256} from '@noble/hashes/sha2.js';
import {ed25519} from '@noble/curves/ed25519.js';
/**
 * PERF-27: older TV browsers (Chromium < 116, Safari < 17.4) lack `AbortSignal.any`, and some
 * lack `AbortSignal.timeout`; without them every artwork and account request throws before it is
 * sent. Installed once, before anything else runs.
 */
type Signals = typeof AbortSignal & {any?: (signals: AbortSignal[]) => AbortSignal; timeout?: (ms: number) => AbortSignal};

export function installAbortSignalPolyfills(target: Signals = AbortSignal as Signals): void {
  if (typeof target.timeout !== 'function') {
    target.timeout = (ms: number) => {
      const controller = new AbortController();
      setTimeout(() => controller.abort(new DOMException('The operation timed out.', 'TimeoutError')), ms);
      return controller.signal;
    };
  }
  if (typeof target.any !== 'function') {
    target.any = (signals: AbortSignal[]) => {
      const controller = new AbortController();
      const done = signals.find(signal => signal.aborted);
      if (done) { controller.abort(done.reason); return controller.signal; }
      const stops: (() => void)[] = [];
      const abort = (signal: AbortSignal) => () => { for (const stop of stops) stop(); controller.abort(signal.reason); };
      for (const signal of signals) {
        const listener = abort(signal);
        signal.addEventListener('abort', listener, {once: true});
        stops.push(() => signal.removeEventListener('abort', listener));
      }
      return controller.signal;
    };
  }
}

if (typeof AbortSignal !== 'undefined') installAbortSignalPolyfills();

/**
 * Plain HTTP is first-class: a server's own web app opened from another device on the LAN
 * (http://192.168.x.x:32500) is not a secure context, and browsers hide `crypto.randomUUID`,
 * `navigator.locks` and `crypto.subtle` there. The first two are filled in here; the server
 * identity check gets an audited JavaScript SHA-256 and Ed25519 (the same verification, not a
 * weaker one); stored credentials fall back to plain storage (bridge/server-connections.ts).
 */
export function installInsecureContextFallbacks(target: {crypto?: Crypto; navigator?: Navigator} = globalThis as never): void {
  const c = target.crypto as (Crypto & {randomUUID?: () => string}) | undefined;
  if (c && typeof c.randomUUID !== 'function') {
    Object.defineProperty(c, 'randomUUID', {configurable: true, value: randomId});
  }
  const nav = target.navigator as (Navigator & {locks?: unknown}) | undefined;
  if (nav && !nav.locks) Object.defineProperty(nav, 'locks', {configurable: true, value: tabLocks()});
  if (!c?.subtle) setRouteCryptoFallback({sha256: async b => sha256(b), verify: async (key, sig, raw) => { try { return ed25519.verify(sig, raw, key); } catch { return false; } }});
}

/** Exclusive locks within this tab: without Web Locks, tabs can't coordinate, but work in one
 * tab still runs one at a time per name (and a waiting request honors its abort signal). */
function tabLocks() {
  const tails = new Map<string, Promise<unknown>>();
  return {
    request<T>(name: string, optionsOrWork: {signal?: AbortSignal} | (() => Promise<T>), maybeWork?: () => Promise<T>): Promise<T> {
      const work = typeof optionsOrWork === 'function' ? optionsOrWork : maybeWork!;
      const signal = typeof optionsOrWork === 'function' ? undefined : optionsOrWork.signal;
      const previous = tails.get(name) ?? Promise.resolve();
      const run = previous.catch(() => undefined).then(() => {
        if (signal?.aborted) throw signal.reason ?? new DOMException('The request was aborted.', 'AbortError');
        return work();
      });
      const tail = run.catch(() => undefined);
      tails.set(name, tail);
      void tail.then(() => { if (tails.get(name) === tail) tails.delete(name); });
      return run;
    },
  };
}

installInsecureContextFallbacks();
