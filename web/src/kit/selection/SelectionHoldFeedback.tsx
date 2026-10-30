import type {CSSProperties} from 'react';
import {selectionText} from './copy';
import {holdProgress} from './interaction';
import styles from './selection.module.css';

export type SelectionHoldFeedbackProps = Readonly<{
  active: boolean;
  progress: number | null;
  className?: string;
  style?: CSSProperties;
}>;

/** A renderer for the card's actual hold state; it owns no gesture, timer, haptic or selection. */
export function SelectionHoldFeedback({active, progress, className, style}: SelectionHoldFeedbackProps) {
  if (!active) return null;
  const fraction = holdProgress(progress);
  return <span className={[styles.holdFeedback, className].filter(Boolean).join(' ')} style={style} role="status">
    <span className={styles.holdTouch} aria-hidden="true">
      {fraction !== null && <svg className={styles.holdRing} viewBox="0 0 88 88" width={88} height={88} aria-hidden="true">
        <circle cx={44} cy={44} r={40} pathLength={1} fill="none" stroke="currentColor" strokeWidth={3} strokeDasharray="1" strokeDashoffset={1 - fraction} />
      </svg>}
    </span>
    <span className={styles.holdTip}>{selectionText('next.selection.keepHolding')}</span>
  </span>;
}
