import type {ReactNode, Ref} from 'react';
import {Button} from '../controls';
import styles from './rules.module.css';
export type RuleTone = 'documentary' | 'news' | 'sport';
/** A full keyboard-operable rule card; its action affordance opens the same editor. */
export function RuleCard({title, glyph, tone, selected, paused, options, next, conflict, state, upcoming, actionsLabel, onOpen, buttonRef, disabled}: {
  title: string; glyph: string; tone?: RuleTone; selected: boolean; paused: boolean; options: readonly string[]; next: string; conflict?: boolean;
  state: string; upcoming?: string; actionsLabel: string; onOpen: () => void; buttonRef?: Ref<HTMLButtonElement>; disabled?: boolean;
}) {
  return <article className={styles.rule} data-tone={tone} data-selected={selected || undefined} data-paused={paused || undefined}
    onClick={event => {if (!disabled && event.target instanceof Element && !event.target.closest('button')) onOpen();}}>
    <div className={styles.art} aria-hidden="true"><div className={styles.generated}><span>{glyph}</span></div></div>
    <button ref={buttonRef} type="button" className={styles.copy} disabled={disabled} onClick={onOpen} aria-pressed={selected}><b>{title}</b><small className={styles.options}>{options.map((option, index) => <span key={index}>{option}</span>)}</small><span className={styles.next} data-conflict={conflict || undefined}>{next}</span></button>
    <div className={styles.state}><span className={styles.pill} data-on={!paused}>{state}</span>{upcoming && <small>{upcoming}</small>}</div>
    <Button className={styles.more} variant="icon" icon="i-more" iconOnly iconSize={22} disabled={disabled} aria-label={actionsLabel} onClick={onOpen}/>
  </article>;
}
/** Framed editor decoration and form rows share the channel palette with cards. */
export function RuleDecoration({glyph, tone}: {glyph: string; tone?: RuleTone}) { return <div className={styles.decoration} data-tone={tone} aria-hidden="true"><div className={styles.glow}/><span className={styles.glyph}>{glyph}</span></div>; }
export function RuleField({label, children}: {label: string; children: ReactNode}) { return <div className={styles.field}><span>{label}</span>{children}</div>; }
export function RuleGap() { return <i className={styles.gap} aria-hidden="true"/>; }
