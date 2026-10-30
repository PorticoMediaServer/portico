/** DEV collector primitives for public RN host refs. No props, stores or assets are read. */
export type HostRect = Readonly<{x: number; y: number; width: number; height: number}>;
export type HostMeasurement = Readonly<{rect: HostRect; source: 'bounding-client-rect' | 'measure-in-window'}>
  | Readonly<{unavailable: string}>;

export type NativeHostReader = Readonly<{
  getBoundingClientRect?: () => unknown;
  measureInWindow?: (callback: (x: number, y: number, width: number, height: number) => void) => void;
}>;

function rect(value: unknown): HostRect | undefined {
  if (typeof value !== 'object' || value === null) return undefined;
  const raw = value as Record<string, unknown>;
  const {x, y, width, height} = raw;
  if (typeof x !== 'number' || typeof y !== 'number' || typeof width !== 'number' || typeof height !== 'number'
      || ![x, y, width, height].every(Number.isFinite) || width < 0 || height < 0) return undefined;
  return Object.freeze({x, y, width, height});
}

/** Preserve raw logical API coordinates. Cross-API/window equivalence needs observation;
 * a method name or a zero origin does not establish it. DPR is never applied here. */
export async function measureNativeHost(
  host: NativeHostReader | null | undefined,
  options: Readonly<{signal?: AbortSignal; timeoutMs?: number}> = {},
): Promise<HostMeasurement> {
  const {signal} = options;
  if (signal?.aborted) return {unavailable: 'capture cancelled'};
  if (!host) return {unavailable: 'native host is not mounted'};
  if (typeof host.getBoundingClientRect === 'function') {
    try {
      const measured = rect(host.getBoundingClientRect());
      if (signal?.aborted) return {unavailable: 'capture cancelled'};
      if (measured) return {rect: measured, source: 'bounding-client-rect'};
    } catch { /* Use the real callback API if this host does not support synchronous measurement. */ }
  }
  if (typeof host.measureInWindow !== 'function') return {unavailable: 'native host exposes no window measurement API'};
  const timeoutMs = options.timeoutMs ?? 1000;
  if (!Number.isFinite(timeoutMs) || timeoutMs <= 0 || timeoutMs > 5000) return {unavailable: 'invalid measurement deadline'};
  return new Promise(resolve => {
    let finished = false;
    const finish = (measurement: HostMeasurement) => {
      if (finished) return;
      finished = true;
      clearTimeout(timer);
      signal?.removeEventListener('abort', onAbort);
      resolve(measurement);
    };
    const onAbort = () => finish({unavailable: 'capture cancelled'});
    const timer = setTimeout(() => finish({unavailable: 'native measurement callback did not arrive'}), timeoutMs);
    signal?.addEventListener('abort', onAbort, {once: true});
    if (signal?.aborted) { onAbort(); return; }
    try {
      host.measureInWindow!((x, y, width, height) => {
        const measured = rect({x, y, width, height});
        finish(measured ? {rect: measured, source: 'measure-in-window'} : {unavailable: 'native measurement was not finite'});
      });
    } catch { finish({unavailable: 'native host measurement failed'}); }
  });
}

/** Root-relative conversion uses the actual root origin, with no frame-specific compensation. */
export function relativeToRoot(measured: HostRect, root: HostRect): HostRect {
  return Object.freeze({x: measured.x - root.x, y: measured.y - root.y, width: measured.width, height: measured.height});
}
