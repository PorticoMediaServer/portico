import type {ReactNode} from 'react';
import type {ArtworkSource} from '../artwork';
import {Artwork} from '../artwork';
import {Button, Segmented} from '../controls';
import {LevelMeter} from '../dock/LevelMeter';
import {Icon, type IconName} from '../Icon';
import styles from './queue.module.css';

/** The audio player's queue panel and its rows (desktop-listen "now", "now-states" and "book-player"; tablet-play 9,
 * 10 and 12). `AudioPlayerFrame` places the panel (its `panel` region). The native app has the same parts with the
 * same names in `apps/native/src-next/kit/player/queue.tsx`. */

type Action = Readonly<{label: string; icon?: IconName; onPress: () => void}>;

/** The raised panel: segmented tabs (Up next | Lyrics, or Chapters | Up next), the list, and a footer with the count
 * and its action (Save as playlist) or a hint (Page Up and Page Down). */
export function AudioPanel({tabs, footer, children, label}: {
  tabs?: Readonly<{items: readonly Readonly<{value: string; label: string; icon: IconName}>[]; value: string; onValueChange: (value: string) => void}>;
  footer?: Readonly<{text: string; action?: Action}>; children: ReactNode; /** For assistive technology. */ label: string}) {
  return <section className={styles.panel} aria-label={label}>
    {tabs && <Segmented className={styles.tabs} items={tabs.items} value={tabs.value} onValueChange={tabs.onValueChange} aria-label={label} />}
    <div className={styles.list}>{children}</div>
    {footer && <div className={styles.foot}><span>{footer.text}</span>{footer.action && <Button variant="glass" size="small" icon={footer.action.icon} onClick={footer.action.onPress}>{footer.action.label}</Button>}</div>}
  </section>;
}

/** The panel's raised surface alone, for a screen whose own panel content (tabs, queue, lyrics, the equaliser) already lays
 * itself out: the same glass card as `AudioPanel`. */
export function AudioPanelSurface({label, children}: {label: string; children: ReactNode}) {
  return <section className={`${styles.panel} ${styles.surface}`} aria-label={label}>{children}</section>;
}

/** A group's label ("Now playing", "Next from After Hours", "Added by you") with its hint at the right ("Drag to reorder", "Chapter 15 of 26"). */
export function QueueGroup({label, hint}: {label: string; hint?: string}) {
  return <h3 className={styles.group}><span>{label}</span>{hint !== undefined && <small>{hint}</small>}</h3>;
}

/** One queue or chapter row. Its lead says what it is: playing (the level meter), a handle to drag, a finished chapter
 * (a tick) or a chapter number. The row playing is raised and its title is the accent. A title takes a second line
 * rather than being cut. With a pointer, a row's actions (Move up, Move down, Remove) take the length's place on hover
 * or focus; on touch it ends with its More. */
export function QueueRow({lead, artwork, title, subtitle, duration, progress, onPress, actions, more}: {
  lead: 'playing' | 'handle' | 'done' | number; artwork?: ArtworkSource; title: string; subtitle?: string; duration?: string;
  /** The chapter playing: how far through it is, 0 to 1. */ progress?: number; onPress: () => void;
  /** A pointer's row actions, shown on hover and focus. */ actions?: readonly (Action & {icon: IconName; disabled?: boolean})[];
  /** Touch: the row's More. */ more?: Action}) {
  return <div className={styles.row} data-playing={lead === 'playing' || undefined} data-hover={actions ? true : undefined}>
    <span className={styles.lead} aria-hidden="true">{lead === 'playing' ? <LevelMeter /> : lead === 'handle' ? <Icon name="i-grip" /> : lead === 'done' ? <Icon name="i-check" /> : lead}</span>
    {artwork && <span className={styles.art}><Artwork source={artwork} alt="" width="100%" height="100%" /></span>}
    <button type="button" className={styles.text} onClick={onPress}><b>{title}</b>{subtitle !== undefined && <small>{subtitle}{more && duration !== undefined ? <i>{duration}</i> : null}</small>}
      {progress !== undefined && <span className={styles.progress}><i style={{width: `${progress * 100}%`}} /></span>}</button>
    {duration !== undefined && !more && <span className={styles.duration}>{duration}</span>}
    {actions && <span className={styles.actions}>{actions.map(action => <Button key={action.label} variant="icon" size="small" icon={action.icon} aria-label={action.label} title={action.label} disabled={action.disabled} onClick={action.onPress} />)}</span>}
    {more && <Button variant="icon" size="small" icon="i-more" aria-label={more.label} onClick={more.onPress} />}
  </div>;
}

/** An audiobook's Speed or Sleep timer from 600 (desktop-listen "book-player"): its name and what it means now beside
 * one row of every choice, the chosen one in the accent. */
export function AudioChoiceRow({label, help, choices}: {label: string; help?: string; choices: readonly Readonly<{label: string; selected?: boolean; onPress: () => void}>[]}) {
  return <div className={styles.choiceRow}>
    <span className={styles.choiceLabel}>{label}{help !== undefined && <small>{help}</small>}</span>
    <span className={styles.choices} role="group" aria-label={label}>{choices.map(choice => <button key={choice.label} type="button" aria-pressed={!!choice.selected} onClick={choice.onPress}>{choice.label}</button>)}</span>
  </div>;
}
