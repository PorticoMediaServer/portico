/** Why each Home document read happened (E-mobile-01 cold launch: four reads where one was owed).
 * A caller names the reason just before it asks; a read nobody named came from Home's own
 * service (a stale row page or a retry). Release builds log the first reads of each runtime,
 * reason and order only. */
export type HomeReadReason = 'restore' | 'mount' | 'freshness' | 'refresh-round' | 'live-expiry' | 'dropped-rows' | 'pull' | 'my-list';

let pending: {reason: HomeReadReason; at: number} | undefined;

/** A reason holds for the read that follows within a second; a caller that ends up reading nothing leaves no label behind. */
export function noteHomeRead(reason: HomeReadReason) {pending = {reason, at: Date.now()};}

export function createHomeReadLog(sink: (line: string) => void = line => console.warn(line), limit = 12) {
  let count = 0;
  const started = Date.now();
  return () => {
    const reason = pending && Date.now() - pending.at < 1000 ? pending.reason : 'home-service'; pending = undefined;
    if (++count <= limit) try {sink(`portico_home_read ${JSON.stringify({n: count, reason, ms: Date.now() - started})}`);} catch { /* passive */ }
    return reason;
  };
}
