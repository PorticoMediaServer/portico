import {createPortal} from 'react-dom';
import {usePresence, useSurfaceMotion} from '../overlays/shared';
import styles from './scrim.module.css';

/** The actual overlay motion helper retains and retargets the owned page dimmer. */
export function ShellScrim({open, context}: {open: boolean; context: 'account' | 'activity'}) {
  const present = usePresence(open, 160), ref = useSurfaceMotion(open, 'fade');
  return present && typeof document !== 'undefined' ? createPortal(<div ref={ref} className={styles.scrim} data-context={context} aria-hidden="true" />, document.body) : null;
}
