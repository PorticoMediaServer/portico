/** A known opacity job can hand its current paint to another job on the SAME original
 * node/source. No animation search or cancellation of an unrelated owner is permitted. */
type Paint = Readonly<{source: unknown; kind: 'arrival' | 'exit'; take: () => number | undefined}>;
const paints = new WeakMap<HTMLElement, Paint>();
export type ArrivalPaintLease = Readonly<{release: () => void; current: () => boolean}>;
export function ownArrivalPaint(node: HTMLElement, source: unknown, take: () => number | undefined, kind: 'arrival' | 'exit' = 'arrival'): ArrivalPaintLease {
  const original = {source, take, kind};paints.set(node, original);
  return {release: () => {if (paints.get(node) === original) paints.delete(node);}, current: () => paints.get(node) === original};
}
export function hasExitPaint(node: HTMLElement, source: unknown): boolean {
  const original = paints.get(node);return !!original && original.source === source && original.kind === 'exit';
}
export function takeArrivalPaint(node: HTMLElement, source: unknown): number | undefined {
  const original = paints.get(node);
  if (!original || original.source !== source) return;
  paints.delete(node);
  const value = original.take();
  return typeof value === 'number' && Number.isFinite(value) && value >= 0 && value <= 1 ? value : undefined;
}
