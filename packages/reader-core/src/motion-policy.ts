import type {ReaderMotionPolicy} from './ports.ts';

/** Capture both original method objects before the controller first awaits.
 * A missing legacy policy uses the system; a malformed supplied policy reduces. */
export function captureReaderMotion(policy: ReaderMotionPolicy | undefined): ReaderMotionPolicy | undefined {
  if (policy === undefined) return;
  try {
    const read = policy.reduced, subscribe = policy.subscribe;
    if (typeof read !== 'function' || typeof subscribe !== 'function') throw new Error('invalid_motion_policy');
    return Object.freeze({reduced: read.bind(policy), subscribe: subscribe.bind(policy)});
  } catch {return Object.freeze({reduced: () => true, subscribe: () => () => {}});}
}

type SystemMotion = Readonly<{readonly matches: boolean; addEventListener(type: 'change', callback: () => void): void; removeEventListener(type: 'change', callback: () => void): void}>;
/** This observer carries only presentation policy, never session authority. */
export function watchReaderMotion(policy: ReaderMotionPolicy | undefined, system: SystemMotion | undefined, changed: (reduced: boolean) => void) {
  const original = captureReaderMotion(policy);
  let refused = false;
  let attachSystem: ((callback: () => void) => void) | undefined, detachSystem: ((callback: () => void) => void) | undefined;
  try {if (system) {attachSystem = system.addEventListener.bind(system, 'change');detachSystem = system.removeEventListener.bind(system, 'change');}} catch {refused = true;}
  let live = true, off: (() => void) | undefined;
  const reduced = () => {
    if (!live || refused) return true;
    try {
      const systemReduced = system?.matches;
      const appReduced = original ? original.reduced() : false;
      return systemReduced !== false || appReduced !== false;
    } catch {return true;}
  };
  const notify = () => {if (!live) return;const value = reduced();if (live) {try {changed(value);} catch {refused = true;}}};
  const dispose = () => {if (!live) return;live = false;try {detachSystem?.(notify);} catch { /* Original watcher cleanup is isolated. */ }const original = off;off = undefined;try {original?.();} catch { /* Motion withdrawal never changes source authority. */ }};
  try {attachSystem?.(notify);} catch {refused = true;}
  try {
    const stop = original?.subscribe(notify);
    if (typeof stop === 'function') {if (live) off = stop;else stop();}
    else if (original) {refused = true;notify();}
  } catch {refused = true;if (live) {try {changed(true);} catch { /* A paint refusal does not retire a source. */ }}}
  return {reduced, dispose};
}
