import {useLayoutEffect, useRef, type RefObject} from 'react';
import {easings, motion} from '@design';
import {useReducedMotion} from '../overlays/shared';

/** One row's own vertical place, in layout pixels (transforms never reach it). */
export type PanelRowPlacement = Readonly<{key: string; top: number}>;

/** motion S.9: a row that leaves closes the rows below up in slow 320 in-out. Only a real
 * add or remove moves a row, so a value update that leaves every row in place moves nothing,
 * and a row that is new has no earlier place to travel from. */
export function panelRowMoves(previous: ReadonlyMap<string, PanelRowPlacement>, next: readonly PanelRowPlacement[]): readonly Readonly<{key: string; from: number}>[] {
  const moves: {key: string; from: number}[] = [];
  for (const row of next) {
    const before = previous.get(row.key);
    if (!before) continue;
    const delta = before.top - row.top;
    if (Math.abs(delta) >= 1) moves.push({key: row.key, from: delta});
  }
  return moves;
}

/**
 * The popover's size follows its rows. A value-only update leaves every row where it was, so
 * nothing animates; a row arriving or leaving moves the rows below it in slow 320 in-out and the
 * surface follows them, per the menu motion's S.9 rule.
 *
 * `ref` is the panel's content box. Rows mark themselves with `data-panel-row`, and only they
 * move: the sections that hold them keep their own layout, so no row is nested inside another
 * moving row and no offset is applied twice. Places and heights are read from `offsetTop` and
 * `offsetHeight`, which are transform-free, so the popover's own entrance tween is not mistaken
 * for a row moving.
 */
export function usePanelMotion(ref: RefObject<HTMLElement | null>) {
  const reduced = useReducedMotion(ref);
  const measured = useRef<{rows: Map<string, PanelRowPlacement>; height: number} | undefined>(undefined);
  const resize = useRef<Animation | undefined>(undefined);
  const moves = useRef(new Map<string, Animation>());
  useLayoutEffect(() => {
    const node = ref.current;
    // A running tween is the previous commit's answer; drop it before reading the new natural
    // layout, so the next one starts from the height this panel really has now.
    resize.current?.cancel(); resize.current = undefined;
    for (const animation of moves.current.values()) animation.cancel();
    moves.current.clear();
    if (!node) { measured.current = undefined; return; }
    const rows: PanelRowPlacement[] = [];
    for (const row of node.querySelectorAll<HTMLElement>('[data-panel-row]')) {
      const key = row.dataset.panelRow;
      if (key) rows.push({key, top: row.offsetTop});
    }
    const height = node.offsetHeight, previous = measured.current;
    measured.current = {rows: new Map(rows.map(row => [row.key, row])), height};
    // The first committed panel is the popover's own entrance: nothing has moved yet.
    if (!previous || reduced) return;
    if (height !== previous.height) {
      resize.current = node.animate([{height: `${previous.height}px`}, {height: `${height}px`}], {duration: motion.slow, easing: easings.inOut});
    }
    for (const move of panelRowMoves(previous.rows, rows)) {
      const row = node.querySelector<HTMLElement>(`[data-panel-row="${CSS.escape(move.key)}"]`);
      if (!row) continue;
      const animation = row.animate([{transform: `translateY(${move.from}px)`}, {transform: 'none'}], {duration: motion.slow, easing: easings.inOut});
      moves.current.set(move.key, animation);
      void animation.finished.then(() => { if (moves.current.get(move.key) === animation) moves.current.delete(move.key); }, () => {});
    }
  });
  useLayoutEffect(() => () => {
    resize.current?.cancel(); resize.current = undefined;
    for (const animation of moves.current.values()) animation.cancel();
    moves.current.clear();
  }, []);
}
