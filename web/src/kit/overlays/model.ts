import {springs, type Spring} from '@design';

export type SheetDetent = Readonly<{height: number; index: number}>;
export function sheetDetents(values: readonly number[], available: number): readonly SheetDetent[] {
  const ceiling = Math.max(1, Number.isFinite(available) ? available : 1);
  const seen = new Set<number>();
  return values.flatMap((height, index) => {
    if (!Number.isFinite(height) || height <= 0) return [];
    const bounded = Math.min(ceiling, height);
    if (seen.has(bounded)) return [];
    seen.add(bounded);
    return [{height: bounded, index}];
  }).sort((a, b) => a.height - b.height);
}

/** The finger's projected position after 200ms chooses the nearest resting height. */
export function releaseSheet(height: number, velocityY: number, detents: readonly SheetDetent[]): SheetDetent | null {
  if (!detents.length) return null;
  const projected = height - velocityY * .2;
  const first = detents[0]!;
  if (projected <= first.height / 2) return null;
  return detents.reduce((best, value) => Math.abs(value.height - projected) < Math.abs(best.height - projected) ? value : best, first);
}

export function resistedHeight(height: number, maximum: number): number {
  return height > maximum ? maximum + (height - maximum) * .2 : Math.max(0, height);
}
export function dismissToast(x: number, y: number, velocityX: number, velocityY: number): boolean {
  return Math.abs(x) > 40 || y > 40 || Math.abs(velocityX) > 500 || velocityY > 500;
}

/** Exact damped spring samples carry the release velocity into WAAPI. */
export function springFrames(from: number, to: number, velocity: number, spring: Spring = springs.settle, durationMs = 500): readonly number[] {
  const w0 = Math.sqrt(spring.stiffness / spring.mass), decay = spring.damping / (2 * spring.mass);
  const wd = Math.sqrt(Math.max(.0001, w0 * w0 - decay * decay)), displacement = from - to;
  const b = (velocity + decay * displacement) / wd;
  return Array.from({length: 33}, (_, i) => {
    if (i === 32) return to;
    const time = i / 32 * durationMs / 1000;
    return to + Math.exp(-decay * time) * (displacement * Math.cos(wd * time) + b * Math.sin(wd * time));
  });
}

/** A keyboard can shrink/pan Safari's visual viewport while fixed positioning
 * still uses the layout viewport. Keep the sheet within the visible rectangle. */
export function sheetViewport(layoutHeight: number, visual?: Readonly<{height: number; offsetTop: number}> | null) {
  const height = visual?.height ?? layoutHeight;
  return {height, bottom: Math.max(0, layoutHeight - height - (visual?.offsetTop ?? 0))};
}
