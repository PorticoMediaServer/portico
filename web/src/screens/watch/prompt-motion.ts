import {useEffect, useLayoutEffect, useRef, useState, type CSSProperties} from 'react';
import {easings, reducedMotion, springs, springTiming} from '@design';
import type {WebPlaybackRuntimeSnapshot} from '../../platform/playback';
import {fenceKey} from '../../shell/playback-model';
export {useReducedMotion as usePromptReducedMotion} from '../../kit/overlays/shared';

const snap = springTiming(springs.snap);

/** The prompt's canonical snap spring, shared by the three watch prompts. */
export const promptSnapStyle: CSSProperties = {
  '--prompt-snap-ms': `${snap.durationMs}ms`,
  '--prompt-spring': snap.css,
} as unknown as CSSProperties;

/** The authority, queue fence and visibility a painted prompt belongs to. A retained
 * paint never crosses these facts: another source or authority, a collapsed player or
 * a quiet dock replaces it at once rather than animating an offer the owner withdrew. */
type PromptScope = Readonly<{authority: WebPlaybackRuntimeSnapshot['authority']; owner: object; key: string}>;
export function promptScope(snapshot: WebPlaybackRuntimeSnapshot, expanded: boolean, owner: object): PromptScope {
  const authority = snapshot.authority;
  return {authority, owner, key: JSON.stringify([authority, snapshot.fence ? fenceKey(snapshot.fence) : null,
    expanded, snapshot.disposed, snapshot.dock.remote?.phase === 'playing'])};
}
const sameScope = (a: PromptScope, b: PromptScope) => a.authority === b.authority && a.owner === b.owner && a.key === b.key;

export type PromptPhase = 'in' | 'out' | 'idle';

/** Only the prompt's presentation lifetime. The caller keeps its admission, expiry,
 * segment state and handlers; an outgoing paint keeps the exact value it was drawn
 * with and is inert from the first render that withdraws it. */
export function usePromptPresence<T>(value: T | undefined, key: string | undefined, scope: PromptScope, reduced: boolean) {
  type Paint = Readonly<{value: T; key: string; scope: PromptScope}>;
  const last = useRef<Paint | undefined>(undefined), [released, setReleased] = useState<Paint>();
  // Only committed paint can outlive an offer. A speculative render never
  // publishes its callbacks or value into the retained record.
  useLayoutEffect(() => {
    if (key !== undefined && value !== undefined) last.current = {value, key, scope};
    else if (last.current && !sameScope(last.current.scope, scope)) last.current = undefined;
  });
  const held = last.current && last.current !== released && sameScope(last.current.scope, scope) ? last.current : undefined;
  const active = key !== undefined && value !== undefined;
  const present = active ? value : held?.value;
  const phase: PromptPhase = active ? 'in' : present !== undefined ? 'out' : 'idle';
  useLayoutEffect(() => {
    if (active || !held) return;
    const original = held;
    const timer = setTimeout(() => setReleased(original), reduced ? 160 : 120);
    return () => clearTimeout(timer);
  }, [active, reduced, held]);
  useLayoutEffect(() => () => {last.current = undefined;}, []);
  return {present, phase, paintKey: active ? `${scope.key}\u0000${key}` : held ? `${held.scope.key}\u0000${held.key}` : undefined};
}

/** Presentation only: the prompt's 12 px rise and fade, retargeted from the pose that
 * is actually painted, including a reversal during either motion. */
export function usePromptMotion(phase: PromptPhase, paintKey: string | undefined, reduced: boolean) {
  const element = useRef<HTMLDivElement | null>(null);
  const painted = useRef<Readonly<{node: HTMLDivElement; animation: Animation}> | null>(null);
  const identity = useRef<string | undefined>(undefined);
  useLayoutEffect(() => {
    const node = element.current;
    if (!node) {painted.current?.animation.cancel(); painted.current = null; identity.current = undefined; return;}
    // A changed paint key is a new offered occurrence, even if the node is reused.
    const fresh = identity.current !== paintKey || painted.current?.node !== node;
    identity.current = paintKey;
    // Match the shared layout-owned signal immediately, before its state settles.
    const still = reduced || window.matchMedia('(prefers-reduced-motion: reduce)').matches || !!node.closest('[data-reduce-motion="true"]')
      || document.documentElement.dataset.reduceMotion === 'true' || document.body.dataset.reduceMotion === 'true';
    const prior = !fresh ? painted.current : null;
    const style = getComputedStyle(node), opacity = Number.parseFloat(style.opacity);
    const pose = {opacity: Number.isFinite(opacity) ? opacity : 1, translate: style.translate && style.translate !== 'none' ? style.translate : '0px'};
    painted.current?.animation.cancel();
    painted.current = null;
    if (phase === 'idle') return;
    const startOpacity = prior ? pose.opacity : phase === 'in' ? 0 : 1;
    const endOpacity = phase === 'in' ? 1 : 0;
    if (still) {
      // Reduced motion is a fade only: drop the individual translate at once, even
      // when a normal rise or an earlier exit is reversed mid-motion.
      node.style.translate = 'none';
      const animation = node.animate([{opacity: startOpacity}, {opacity: endOpacity}],
        {duration: reducedMotion.durationMs, easing: easings.linear, fill: phase === 'out' ? 'forwards' : 'none'});
      painted.current = {node, animation};
      return;
    }
    node.style.removeProperty('translate');
    const startTranslate = prior ? pose.translate : phase === 'in' ? '0px 12px' : '0px';
    // Leaving fades without moving; returning rises from whatever pose is painted.
    const endTranslate = phase === 'out' ? startTranslate : '0px';
    const animation = node.animate([{opacity: startOpacity, translate: startTranslate}, {opacity: endOpacity, translate: endTranslate}],
      {duration: phase === 'in' ? 180 : 120, easing: easings.out, fill: phase === 'out' ? 'forwards' : 'none'});
    painted.current = {node, animation};
  }, [phase, paintKey, reduced]);
  useEffect(() => () => {painted.current?.animation.cancel(); painted.current = null;}, []);
  return element;
}
