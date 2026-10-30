import type {DockRect} from './types';

export function initialPictureRect(rect: DockRect | undefined): rect is DockRect {
  'worklet';
  return !!rect && Number.isFinite(rect.x) && Number.isFinite(rect.y) && Number.isFinite(rect.width) && Number.isFinite(rect.height) && rect.width > 0 && rect.height > 0;
}
/** Both inputs are actual window measurements; there is no window/dock fallback. */
export function initialPictureInHost(source: DockRect, host: DockRect): DockRect | undefined {
  if (!initialPictureRect(source) || !initialPictureRect(host)) return undefined;
  return {x: source.x - host.x, y: source.y - host.y, width: source.width, height: source.height};
}
export function initialPictureMix(from: DockRect, to: DockRect, progress: number): DockRect {
  'worklet';
  const p = Math.max(0, Math.min(1, progress));
  return {x: from.x + (to.x - from.x) * p, y: from.y + (to.y - from.y) * p,
    width: from.width + (to.width - from.width) * p, height: from.height + (to.height - from.height) * p};
}
export function sameInitialPictureRect(a: DockRect | undefined, b: DockRect | undefined): boolean {
  'worklet';
  return a === b || !!a && !!b && a.x === b.x && a.y === b.y && a.width === b.width && a.height === b.height;
}
export function initialPictureSettled(goal: number, progress: number, velocity: number, dragging: boolean, arrived: boolean, manual: boolean, hasDock: boolean): boolean {
  'worklet';
  if (dragging || !Number.isFinite(progress) || !Number.isFinite(velocity) || Math.abs(velocity) >= .02) return false;
  return goal === 1 ? hasDock && (!manual || arrived) && Math.abs(progress - 1) < .002 : arrived && Math.abs(progress) < .002;
}
/** Fixed clip box and inverse image compensation keep the texture proportional
 * while the shared element changes from the actual poster aspect to16:9. */
export function initialPictureTransform(base: DockRect, frame: DockRect) {
  'worklet';
  const scaleX = frame.width / base.width, scaleY = frame.height / base.height;
  const cover = Math.max(scaleX, scaleY);
  return {translateX: frame.x + frame.width / 2 - base.x - base.width / 2,
    translateY: frame.y + frame.height / 2 - base.y - base.height / 2,
    scaleX, scaleY, imageScaleX: cover / scaleX, imageScaleY: cover / scaleY};
}
const keys = new WeakMap<object, string>(); let sequence = 0;
const retired = new WeakSet<object>();
export function initialPictureRetired(original: object): boolean {return retired.has(original);}
export function retireInitialPicture(original: object): void {retired.add(original);}
export function initialPictureKey(original: object): string {
  let key = keys.get(original);
  if (!key) {key = `initial-picture-${++sequence}`; keys.set(original, key);}
  return key;
}
