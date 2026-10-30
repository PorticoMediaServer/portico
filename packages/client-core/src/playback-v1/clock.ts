/**
 * Server time for group sync and live timeshift (spec §10: NTP-style, four timestamps; fixes
 * BE-MEDIA-14). Each exchange gives
 *   t0 client send, t1 server receive, t2 server send, t3 client receive
 *   offset = ((t1 − t0) + (t2 − t3)) / 2,  rtt = (t3 − t0) − (t2 − t1).
 * The sample with the smallest RTT is the most trustworthy (least queuing asymmetry), so the
 * clock keeps a small window of recent samples and uses the best of them.
 */
export type ClockSample = Readonly<{t0: number; t1: number; t2: number; t3: number}>;

export type ClockEstimate = Readonly<{offsetMs: number; rttMs: number; samples: number}>;

export class ServerClock {
  private window: {offset: number; rtt: number; at: number}[] = [];
  private size: number;
  private maxAgeMs: number;
  private now: () => number;

  constructor(options: {window?: number; maxAgeMs?: number; now?: () => number} = {}) {
    this.size = Math.max(1, options.window ?? 8);
    this.maxAgeMs = options.maxAgeMs ?? 10 * 60_000;
    this.now = options.now ?? Date.now;
  }

  /** Add one exchange. Impossible samples (negative RTT, non-finite) are ignored. */
  add(s: ClockSample): boolean {
    const rtt = (s.t3 - s.t0) - (s.t2 - s.t1);
    const offset = ((s.t1 - s.t0) + (s.t2 - s.t3)) / 2;
    if (![rtt, offset].every(Number.isFinite) || rtt < 0 || s.t2 < s.t1) return false;
    this.window.push({offset, rtt, at: s.t3});
    if (this.window.length > this.size) this.window.shift();
    return true;
  }

  /** The best current estimate, or undefined before any usable sample. */
  estimate(): ClockEstimate | undefined {
    const now = this.now();
    const fresh = this.window.filter(w => now - w.at <= this.maxAgeMs);
    if (!fresh.length) return undefined;
    const best = fresh.reduce((a, b) => (b.rtt < a.rtt ? b : a));
    return Object.freeze({offsetMs: best.offset, rttMs: best.rtt, samples: fresh.length});
  }

  /** Estimated server time now (client time when there is no estimate yet). */
  serverNow(): number {
    return this.now() + (this.estimate()?.offsetMs ?? 0);
  }

  /** Map a server timestamp to the client's clock. */
  toClient(serverMs: number): number {
    return serverMs - (this.estimate()?.offsetMs ?? 0);
  }
}

/**
 * Where a shared timeline is now (spec §10 group timeline `{state, positionMs, rate, atServerTimeMs}`),
 * and how a member should correct: nudge the rate by up to ±5 % under 500 ms of drift, seek above.
 */
export function timelinePosition(t: Readonly<{state: string; positionMs: number; rate: number; atServerTimeMs: number}>, serverNow: number): number {
  if (t.state !== 'playing') return t.positionMs;
  return t.positionMs + Math.max(0, serverNow - t.atServerTimeMs) * t.rate;
}

export type DriftCorrection = Readonly<{kind: 'none'} | {kind: 'rate'; rate: number} | {kind: 'seek'; positionMs: number}>;

export function driftCorrection(localMs: number, targetMs: number, baseRate = 1, options: {toleranceMs?: number; seekAboveMs?: number; maxNudge?: number} = {}): DriftCorrection {
  const tolerance = options.toleranceMs ?? 40;
  const seekAbove = options.seekAboveMs ?? 500;
  const maxNudge = options.maxNudge ?? 0.05;
  const drift = targetMs - localMs;
  if (Math.abs(drift) <= tolerance) return {kind: 'none'};
  if (Math.abs(drift) > seekAbove) return {kind: 'seek', positionMs: Math.max(0, targetMs)};
  // Close the gap over ~2 s, within ±5 %.
  const nudge = Math.max(-maxNudge, Math.min(maxNudge, drift / 2000));
  return {kind: 'rate', rate: baseRate * (1 + nudge)};
}
