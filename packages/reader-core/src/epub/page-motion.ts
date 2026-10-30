import {motion, reducedMotion, springs, springPosition} from '../../../design/src/index.ts';

/** motion.html5.1: quarter-width or a physical flick; RTL mirrors the intent. */
export function pageGesture(width: number, displacement: number, velocity: number, rtl = false): -1 | 0 | 1 {
  if (!Number.isFinite(width) || width <= 0 || !Number.isFinite(displacement) || !Number.isFinite(velocity)) return 0;
  const distance = displacement * (rtl ? -1 : 1), speed = velocity * (rtl ? -1 : 1);
  if (Math.abs(distance) < width / 4 && Math.abs(speed) < 600) return 0;
  const direction = Math.abs(speed) >= 600 ? speed : distance;
  return direction < 0 ? 1 : -1;
}

/** The settle spring sampled for WAAPI: linear keyframe positions from `from` to `to`, launched at the
 * finger's `velocity` (px/s), ending exactly at `to`, with motion.html's 0.002 rest threshold. */
export function settleFrames(from: number, to: number, velocity = 0): Readonly<{values: readonly number[]; duration: number}> {
  const distance = to - from, normalized = distance && Number.isFinite(velocity) ? velocity / distance : 0;
  let last = 0;
  for (let ms = 0; ms < 3000; ms++) if (Math.abs(springPosition(springs.settle, ms / 1000, normalized) - 1) >= .002) last = ms;
  const duration = last + 1, steps = Math.max(2, Math.ceil(duration / 16));
  return {values: Array.from({length: steps + 1}, (_, i) => i === steps ? to : from + distance * springPosition(springs.settle, duration * i / steps / 1000, normalized)), duration};
}

/** Canonical out (.2,.8,.2,1), evaluated against elapsed time, not parameter u. */
export function pageOut(time: number): number {
  if (time <= 0) return 0;if (time >= 1) return 1;
  let low = 0, high = 1;
  for (let n = 0; n < 24; n++) {const u = (low + high) / 2, x = .6 * u - .6 * u * u + u * u * u;if (x < time) low = u;else high = u;}
  const u = (low + high) / 2;return 2.4 * u - 1.8 * u * u + .4 * u * u * u;
}

export type PageTurnPaint = Readonly<{translation: number; opacity: number}>;
export type PageTurnPorts = Readonly<{
  frame(callback: (now: number) => void): number; cancelFrame(id: number): void;
  /** Exact original renderer/view/turn, never a new mounted publication. */
  current(): boolean; reduced(): boolean;
  paint(value: PageTurnPaint): void; commit(offset: number): void;
}>;

/** Construction is inert. The owner publishes this exact turn, then starts it.
 * Only transform/opacity paint while moving; the real scroll offset commits once. */
export function createPageTurnAnimation(input: Readonly<{from: number; to: number; offset: number; velocity?: number; spring?: boolean; ports: PageTurnPorts}>) {
  const {ports, from, to, offset} = input, velocity = Number.isFinite(input.velocity) ? input.velocity! : 0;
  let live = true, started = false, frame: number | undefined, start: number | undefined, fadeStart: number | undefined;
  let paint: PageTurnPaint = {translation: from, opacity: 1}, committed = false;
  let resolve!: (accepted: boolean) => void;
  const finished = new Promise<boolean>(done => {resolve = done;});
  const owns = () => {
    if (!live) return false;
    let accepted = false;try {accepted = ports.current();} catch { /* A refused original view has no paint authority. */ }
    return live && accepted;
  };
  const reduced = () => {try {return ports.reduced() !== false;} catch {return true;}};
  const settle = (accepted: boolean) => {if (!live) return;live = false;if (frame !== undefined) ports.cancelFrame(frame);frame = undefined;resolve(accepted);};
  const apply = (value: PageTurnPaint) => {if (!owns()) return false;paint = value;ports.paint(value);return owns();};
  const commit = () => {
    if (committed) return owns();
    if (!owns()) return false;
    ports.commit(offset);committed = true;return owns();
  };
  let duration = motion.slow;
  if (input.spring) {
    const distance = to - from, normalized = distance ? velocity / distance : 0;
    let last = 0;
    for (let ms = 0; ms < 3000; ms++) if (Math.abs(springPosition(springs.settle, ms / 1000, normalized) - 1) >= .002) last = ms;
    duration = last + 1;
  }
  function step(now: number) {
    frame = undefined;
    if (!owns()) {settle(false);return;}
    start ??= now;
    const reduce = reduced();
    if (!owns()) {settle(false);return;}
    if (reduce || fadeStart !== undefined) {
      fadeStart ??= now;
      const progress = Math.min(1, Math.max(0, (now - fadeStart) / reducedMotion.durationMs));
      if (progress < .5) {
        if (!apply({translation: paint.translation, opacity: 1 - progress * 2})) {settle(false);return;}
      } else {
        // Hide first, then snap to the real page at zero opacity. No spring or
        // transform interpolation survives a reduced-motion switch mid-turn.
        if (!committed && (!apply({translation: paint.translation, opacity: 0}) || !commit())) {settle(false);return;}
        if (!apply({translation: 0, opacity: (progress - .5) * 2})) {settle(false);return;}
      }
      if (progress >= 1) {settle(true);return;}
    } else {
      const elapsed = Math.max(0, now - start), progress = Math.min(1, elapsed / duration);
      const position = input.spring ? springPosition(springs.settle, elapsed / 1000, to === from ? 0 : velocity / (to - from)) : pageOut(progress);
      if (!apply({translation: from + (to - from) * position, opacity: 1})) {settle(false);return;}
      if (progress >= 1) {
        if (!commit() || !apply({translation: 0, opacity: 1})) {settle(false);return;}
        settle(true);return;
      }
    }
    if (owns()) frame = ports.frame(step);else settle(false);
  }
  return {finished,
    start() {if (!live || started) return;started = true;if (!Number.isFinite(from) || !Number.isFinite(to) || !Number.isFinite(offset)) {settle(false);return;}frame = ports.frame(step);},
    cancel() {
      if (!live) return;
      // Withdraw before cancellation/current callbacks can reenter this record.
      settle(false);
      let current = false;try {current = ports.current();} catch { /* Original view retired. */ }
      if (current) ports.paint({translation: 0, opacity: 1});
    },
  };
}
