/**
 * An exclusive lock between the tabs of one browser that works on a plain-HTTP page.
 *
 * Web Locks are given only to secure pages, but local storage and its `storage` event are not.
 * The holder writes a lease (who holds it, until when) and renews it while its work runs; the
 * others wait for it to be released or to run out. Local storage has no compare-and-set, so a
 * tab that writes a lease reads it again a moment later and holds it only if its write is the
 * one that stayed. Should two tabs still both believe they hold it (one was frozen past its
 * lease), the next renewal tells the one that lost, and its work is stopped through `held`.
 *
 * Nothing here carries a credential: a lease names a random holder and a time.
 */
export type LeaseEnvironment = Readonly<{
  read(key: string): string | null;
  write(key: string, value: string): void;
  remove(key: string): void;
  /** Hears another tab change `key` (the `storage` event). Returns the way to stop. */
  onChange(key: string, listener: () => void): () => void;
  /** Hears this page being left, when a held lease is released at once. Returns the way to stop. */
  onLeave?(listener: () => void): () => void;
  now(): number;
  random(): number;
  setTimer(run: () => void, ms: number): unknown;
  clearTimer(timer: unknown): void;
}>;

export type LeaseTiming = Readonly<{
  /** How long a lease lasts without renewal. */
  ttlMs: number;
  /** How often the holder renews it. */
  renewMs: number;
  /** How long a writer waits before reading its lease back. */
  settleMs: number;
  /** How often a waiting tab looks again when it has heard nothing. */
  pollMs: number;
}>;

const DEFAULT_TIMING: LeaseTiming = {ttlMs: 8_000, renewMs: 2_000, settleMs: 120, pollMs: 1_500};

type Lease = {owner: string; until: number};

function readLease(env: LeaseEnvironment, key: string): Lease | undefined {
  try {
    const raw = env.read(key);
    if (!raw || raw.length > 256) return undefined;
    const value = JSON.parse(raw) as Partial<Lease>;
    return typeof value.owner === 'string' && typeof value.until === 'number' && Number.isFinite(value.until) ? {owner: value.owner, until: value.until} : undefined;
  } catch { return undefined; }
}

function abortReason(signal: AbortSignal): unknown {
  return signal.reason ?? new DOMException('The request was aborted.', 'AbortError');
}

/** A `lock` for `sharedStreamOpener` over leases in local storage. */
export function leaseLock(env: LeaseEnvironment, timing: Partial<LeaseTiming> = {}) {
  const {ttlMs, renewMs, settleMs, pollMs} = {...DEFAULT_TIMING, ...timing};
  return function lock(name: string, signal: AbortSignal, work: (held: AbortSignal) => Promise<void>): Promise<void> {
    const key = 'portico.lease.' + name;
    const owner = `${env.now().toString(36)}.${Math.floor(env.random() * 2 ** 48).toString(36)}`;
    const claim = () => { try { env.write(key, JSON.stringify({owner, until: env.now() + ttlMs})); return true; } catch { return false; } };
    const mine = () => readLease(env, key)?.owner === owner;
    const release = () => { try { if (mine()) env.remove(key); } catch { /* storage gone: the lease runs out */ } };

    const acquire = () => new Promise<void>((resolve, reject) => {
      let timer: unknown;
      let settling = false;
      const stop = () => { if (timer !== undefined) env.clearTimer(timer); timer = undefined; offChange(); signal.removeEventListener('abort', aborted); };
      const aborted = () => { stop(); if (settling) release(); reject(abortReason(signal)); };
      const wait = (ms: number) => { if (timer !== undefined) env.clearTimer(timer); timer = env.setTimer(attempt, ms); };
      function attempt() {
        timer = undefined;
        if (signal.aborted) return;
        if (settling) {
          settling = false;
          if (mine()) { stop(); resolve(); return; }
          wait(pollMs);
          return;
        }
        const current = readLease(env, key);
        if (current && current.owner !== owner && current.until > env.now()) { wait(pollMs); return; }
        if (!claim()) { wait(pollMs); return; }
        settling = true;
        wait(settleMs);
      }
      // A release or a change elsewhere is worth a look straight away (spread out a little, so
      // every waiting tab does not write in the same instant).
      const offChange = env.onChange(key, () => { if (!settling) wait(Math.floor(env.random() * 50)); });
      signal.addEventListener('abort', aborted, {once: true});
      attempt();
    });

    return acquire().then(async () => {
      const held = new AbortController();
      let renewal: unknown;
      const renew = () => {
        renewal = undefined;
        if (!mine()) { held.abort(); return; }
        claim();
        renewal = env.setTimer(renew, renewMs);
      };
      renewal = env.setTimer(renew, renewMs);
      const offLeave = env.onLeave?.(release);
      try {
        await work(held.signal);
      } finally {
        if (renewal !== undefined) env.clearTimer(renewal);
        offLeave?.();
        release();
      }
    });
  };
}

/** The browser's local storage and `storage` event, or undefined where storage is refused. */
export function browserLeaseEnvironment(): LeaseEnvironment | undefined {
  let storage: Storage;
  try {
    storage = window.localStorage;
    const probe = 'portico.lease.probe';
    storage.setItem(probe, '1');
    storage.removeItem(probe);
  } catch { return undefined; }
  return {
    read: key => storage.getItem(key),
    write: (key, value) => storage.setItem(key, value),
    remove: key => storage.removeItem(key),
    onChange: (key, listener) => {
      const heard = (event: StorageEvent) => { if (event.key === key || event.key === null) listener(); };
      window.addEventListener('storage', heard);
      return () => window.removeEventListener('storage', heard);
    },
    onLeave: listener => {
      window.addEventListener('pagehide', listener);
      return () => window.removeEventListener('pagehide', listener);
    },
    now: () => Date.now(),
    random: () => Math.random(),
    setTimer: (run, ms) => setTimeout(run, ms),
    clearTimer: timer => clearTimeout(timer as ReturnType<typeof setTimeout>),
  };
}
