import type {ReactNode} from 'react';
import {motion, reducedMotion} from '@design';
import {usePresence, useRetained} from './shared';
import styles from './panel-presence.module.css';

/** A panel its owner closes keeps painting its last content while it leaves (motion 3.7–3.9: panels
 * arrive in 180 and leave in 120, `in`; fade-only under Reduce motion, in the reduced duration). The
 * hold covers the longer of the two, and the leaving panel is inert so it can't take a click. */
export function useLeavingPresence<T>(value: T | null | undefined): Readonly<{shown: T | undefined; leaving: boolean}> {
  const open = value !== null && value !== undefined;
  const present = usePresence(open, Math.max(motion.quick, reducedMotion.durationMs));
  const retained = useRetained(value);
  return {shown: open ? value : present ? retained : undefined, leaving: !open && present};
}

/** No box of its own (display: contents): the panel keeps its own placement and plays the exit. */
export function PanelPresence({leaving, children}: {leaving: boolean; children: ReactNode}) {
  return <div className={styles.presence} data-leaving={leaving || undefined} inert={leaving || undefined}>{children}</div>;
}
