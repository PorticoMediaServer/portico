import {useId, useLayoutEffect, useRef, type ReactNode} from 'react';
import {createPortal} from 'react-dom';
import styles from './dashboard.module.css';
export type TipPoint = Readonly<{x: number; y: number}>;
/** The drawing's shared pointer tip. Values remain the actual selected person/hour facts. */
export function FloatingTip({point, children}: {point?: TipPoint; children: ReactNode}) {
  const ref = useRef<HTMLDivElement>(null), id = useId();
  useLayoutEffect(() => {
    const tip = ref.current; if (!tip || !point) return;
    const x = point.x + 16 + tip.offsetWidth > innerWidth - 4 ? point.x - tip.offsetWidth - 16 : point.x + 16;
    tip.style.left = `${x}px`; tip.style.top = `${point.y + 16}px`;
  }, [point, children]);
  return point ? createPortal(<div ref={ref} id={id} className={`${styles.chartTip} ${styles.floatingTip}`} role="tooltip">{children}</div>, document.body) : null;
}
