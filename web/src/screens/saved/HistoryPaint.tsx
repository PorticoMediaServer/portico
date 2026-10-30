import {useLayoutEffect, useRef, type ReactNode} from 'react';
import type {LayoutExit} from '../../kit/layout/retention';

/** Own only opacity; the outer roster owns gap transforms. Each admitted exit
 * keeps its delay/deadline when another cohort is removed. */
export function HistoryPaint({exit, reduced, scope, children, stagger = true}: {exit?: LayoutExit<unknown>; reduced: boolean; scope: object; children: ReactNode; stagger?: boolean}) {
  const node = useRef<HTMLDivElement>(null);
  const painted = useRef<{node: HTMLDivElement; scope: object} | undefined>(undefined);
  const owned = useRef<{node: HTMLDivElement; animation: Animation; scope: object} | undefined>(undefined);
  // One admitted exit paints with the cohort fields it was given; only its token
  // decides whether this pass re-runs.
  const admitted = useRef(exit);
  useLayoutEffect(() => {admitted.current = exit;});
  useLayoutEffect(() => {
    const exit = admitted.current, element = node.current, old = owned.current;
    if (!element) return;
    // Read partial paint before cancelling. A new original scope never inherits
    // the old scope's pose or lets its settled completion touch this node.
    const same = painted.current?.node === element && painted.current.scope === scope;
    const opacity = same ? Number(getComputedStyle(element).opacity) : 1;
    painted.current = {node: element, scope};
    old?.animation.cancel();owned.current = undefined;
    if (!exit && (!same || !old && element.style.opacity === '')) {element.style.opacity = '';return;}
    const now = performance.now(), delay = exit && !reduced && !exit.reduced ? Math.max(0, exit.admittedAt + (stagger ? exit.delay : 0) - now) : 0;
    const phaseEnd = exit && !stagger ? exit.deadline - exit.delay : exit?.deadline;
    const duration = exit ? Math.max(0, Math.min(Math.min(phaseEnd!, exit.retireAt) - now - delay, reduced ? 160 : Infinity)) : reduced ? 160 : 180;
    if (!duration) {element.style.opacity = exit ? '0' : '';return;}
    const animation = element.animate([{opacity}, {opacity: exit ? 0 : 1}], {delay, duration, easing: 'linear', fill: 'both'});
    const job = {node: element, animation, scope};owned.current = job;
    const finish = () => {
      if (owned.current !== job) return;
      if (!element.isConnected || node.current !== element) {owned.current = undefined;animation.cancel();return;}
      element.style.opacity = exit ? '0' : '';owned.current = undefined;animation.cancel();
    };
    void animation.finished.then(finish, () => {if (owned.current === job) owned.current = undefined;});
    // Replacement reads the painted opacity before cancellation in its setup.
  }, [exit?.token, exit?.reduced, reduced, scope, stagger]);
  useLayoutEffect(() => () => {const job = owned.current;owned.current = undefined;job?.animation.cancel();}, []);
  return <div ref={node}>{children}</div>;
}
