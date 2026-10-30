/** Pure lifecycle for the shared loading boundary; no React Native imports so the
 * Node test harness can exercise it without a renderer. */
export type LoadingPhase = 'quiet' | 'skeleton' | 'content';
export type LoadingEvent = Readonly<{type: 'mounted'; ready: boolean; cached: boolean}> | Readonly<{type: 'quietElapsed'}> | Readonly<{type: 'readyChanged'; ready: boolean}>;

/** The drawn order, as one transfer function: a cached ready mount goes straight to
 * content; anything else starts quiet; the quiet window reveals the shape; and only a
 * real ready change leaves a phase, so a repeat ready does not restart the window. */
export function nextLoadingPhase(previous: LoadingPhase, event: LoadingEvent): LoadingPhase {
  if (event.type === 'mounted') return event.ready && event.cached ? 'content' : 'quiet';
  if (event.type === 'quietElapsed') return previous === 'quiet' ? 'skeleton' : previous;
  return event.ready ? 'content' : 'quiet';
}

/** Reduced motion uses the shared reduced duration for the content fade. */
export function contentFadeDuration(reduce: boolean, base = 180, reduced = 160): number {
  return reduce ? reduced : base;
}

/** The content fade's authored curve: the design system's out bezier by default,
 * linear under reduced motion. Values are the shared easing tokens, not a new curve. */
export const contentFadeEasing = Object.freeze({out: [0.2, 0.8, 0.2, 1] as const, linear: [0, 0, 1, 1] as const});

/** Cached-at-mount only covers the first cycle: any later loading cycle must fade in. */
export function cachedAfterLoadingCycle(cached: boolean, ready: boolean): boolean {
  return ready ? cached : false;
}

/** A painted skeleton is only valid for the cycle it completed: a stale completion
 * from the previous ready state can never flash over a new loading cycle. */
export function paintSkeletonVisible(ready: boolean, doneCycle: number, cycle: number): boolean {
  return !ready && doneCycle === cycle;
}
