export type PlatformPostureBounds = Readonly<{left:number; top:number; right:number; bottom:number}>;
export type PlatformFold = Readonly<{
  bounds:PlatformPostureBounds; orientation:'vertical'|'horizontal'; state:'flat'|'half-opened';
  isSeparating:boolean; occlusion:'none'|'full';
}>;
export type PlatformPostureSnapshot = Readonly<{
  status:'available'|'pending'|'unavailable';
  /** Current whole window in logical DP including insets, with a window-local fold origin. */
  window:Readonly<{width:number; height:number}>|null;
  folds:readonly PlatformFold[];
}>;
export type PlatformPostureTransport = Readonly<{
  getSnapshot():Promise<unknown>;
  subscribe(listener:(value:unknown)=>void):()=>void;
}>;
const folds: readonly PlatformFold[] = Object.freeze([]);
const unavailable: PlatformPostureSnapshot = Object.freeze({status:'unavailable',window:null,folds});
const pending: PlatformPostureSnapshot = Object.freeze({status:'pending',window:null,folds});
const record = (value:unknown): Record<string,unknown>|undefined => value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string,unknown> : undefined;
const finite = (value:unknown): value is number => typeof value === 'number' && Number.isFinite(value);

function parse(value:unknown): {revision:number; snapshot:PlatformPostureSnapshot}|undefined {
  const raw = record(value);
  if (!raw || !Number.isSafeInteger(raw.revision) || (raw.revision as number) < 0 || !Array.isArray(raw.folds)) return;
  if (raw.status !== 'available' && raw.status !== 'pending' && raw.status !== 'unavailable') return;
  const window = record(raw.window);
  if (raw.window !== null && (!window || !finite(window.width) || !finite(window.height) || window.width <= 0 || window.height <= 0)) return;
  if (raw.status === 'available' && !window || raw.status === 'unavailable' && window || raw.status !== 'available' && raw.folds.length) return;
  const decoded:PlatformFold[] = [];
  for (const candidate of raw.folds) {
    const fold = record(candidate), bounds = record(fold?.bounds);
    if (!fold || !bounds || !finite(bounds.left) || !finite(bounds.top) || !finite(bounds.right) || !finite(bounds.bottom) || bounds.left > bounds.right || bounds.top > bounds.bottom) return;
    if ((fold.orientation !== 'vertical' && fold.orientation !== 'horizontal') || (fold.state !== 'flat' && fold.state !== 'half-opened') || typeof fold.isSeparating !== 'boolean' || (fold.occlusion !== 'none' && fold.occlusion !== 'full')) return;
    decoded.push(Object.freeze({bounds:Object.freeze({left:bounds.left,top:bounds.top,right:bounds.right,bottom:bounds.bottom}),orientation:fold.orientation,state:fold.state,isSeparating:fold.isSeparating,occlusion:fold.occlusion}));
  }
  return {revision:raw.revision as number,snapshot:Object.freeze({status:raw.status,window:window ? Object.freeze({width:window.width as number,height:window.height as number}) : null,folds:decoded.length ? Object.freeze(decoded) : folds})};
}

/** Transport revision orders OS events; equal content preserves external-store identity.
 * Last unsubscribe retires pending reads and the sole native subscription. */
export function createPlatformPostureStore(transport:PlatformPostureTransport|null) {
  let snapshot = transport ? pending : unavailable, revision = -1, epoch = 0;
  let detach: (()=>void)|undefined;
  const listeners = new Set<()=>void>();
  const publish = (value:PlatformPostureSnapshot) => {
    if (JSON.stringify(value) === JSON.stringify(snapshot)) return;
    snapshot = value;
    for (const listener of [...listeners]) listener();
  };
  const start = () => {
    if (!transport) return;
    const generation = ++epoch;
    revision = -1; publish(pending);
    const receive = (raw:unknown) => {
      if (generation !== epoch) return;
      const next = parse(raw);
      if (!next || next.revision <= revision) return;
      revision = next.revision; publish(next.snapshot);
    };
    try {
      detach = transport.subscribe(receive);
      void transport.getSnapshot().then(receive, () => { if (generation === epoch && revision < 0) publish(unavailable); });
    } catch {
      epoch++;
      detach?.(); detach = undefined; publish(unavailable);
    }
  };
  return Object.freeze({
    getSnapshot: () => snapshot,
    subscribe: (listener:()=>void) => {
      const subscription = () => listener();
      listeners.add(subscription);
      if (listeners.size === 1) start();
      let live = true;
      return () => {
        if (!live) return;
        live = false; listeners.delete(subscription);
        if (!listeners.size) { epoch++; detach?.(); detach = undefined; }
      };
    },
  });
}
