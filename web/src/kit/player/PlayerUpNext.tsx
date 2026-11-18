import type {HTMLAttributes, ReactNode, Ref} from 'react';
import styles from './up-next.module.css';

export type PlayerUpNextAction = Readonly<{id: string; label: string; onPress: () => void; /** The white one: Play now. */ primary?: boolean; glyph?: 'play' | 'next'}>;
export type PlayerUpNextProps = Omit<HTMLAttributes<HTMLDivElement>, 'title'> & Readonly<{
  ref?: Ref<HTMLDivElement>;
  /** The form it is drawn for: the full card, the tablet's, or the phone's compact one. */
  layout: 'desktop' | 'tablet' | 'phone';
  /** "Up next" over the title. */
  heading: string;
  /** The next title's picture; its box is kept when there is none. */
  still: ReactNode;
  title: string;
  /** The episode code and the running time. */
  facts?: readonly string[];
  /** "Playing in 9" or "Starts in 0:42". */
  when?: string;
  /** 0 to 1: how far the countdown has run. */
  progress?: number;
  actions: readonly PlayerUpNextAction[];
  /** One line under the buttons (a scene after the credits). */
  note?: string;
}>;

const glyphs = {play: 'M7 4.8v14.4c0 .8.9 1.3 1.6.9l11.4-7.2a1 1 0 0 0 0-1.8L8.6 3.9C7.9 3.5 7 4 7 4.8z',
  next: 'M18.5 5.2a1.1 1.1 0 0 0-2.2 0v13.6a1.1 1.1 0 0 0 2.2 0zm-3.9 7.6a1 1 0 0 0 0-1.6L5.9 5.1A1 1 0 0 0 4.3 6v12a1 1 0 0 0 1.6.9z'} as const;

/** The Up next card in the player's prompt corner during the credits and at the end of an episode (desktop-player 4,
 * phone-player 6). The player screen owns when it shows, its countdown and its motion; this draws it. Moved from the
 * player screen. */
export function PlayerUpNext({layout, heading, still, title, facts = [], when, progress, actions, note, className, ref, ...props}: PlayerUpNextProps) {
  return <div {...props} ref={ref} role="group" aria-label={heading} className={[styles.card, className].filter(Boolean).join(' ')} data-layout={layout}>
    <div className={styles.row}>
      <div className={styles.still}>{still}</div>
      <div className={styles.content}><small>{heading}</small><b>{title}</b>
        {facts.length > 0 && <div className={styles.facts}>{facts.map((fact, index) => <span key={fact} style={{display: 'contents'}}>{index > 0 && <i aria-hidden="true" />}{fact}</span>)}</div>}
        {when && <div className={styles.when}>{when}</div>}
      </div>
    </div>
    {progress !== undefined && <div className={styles.meter} aria-hidden="true"><i style={{width: `${progress * 100}%`}} /></div>}
    <div className={styles.actions}>{actions.map(action => <button key={action.id} type="button" className={action.primary ? styles.play : undefined} onClick={action.onPress}>
      {action.glyph && <svg width={18} height={18} viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path fill="currentColor" d={glyphs[action.glyph]} /></svg>}{action.label}</button>)}</div>
    {note && <div className={styles.note}>{note}</div>}
  </div>;
}
