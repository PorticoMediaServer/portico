import {useRef} from 'react';
import {defaultI18n} from '@i18n';
import {Icon} from '../Icon';
import type {MediaLayout} from './types';
import type {MediaTranslate} from './Equalizer';
import styles from './media.module.css';

/** Only videoTrackName(labelParts, words) produces these display fields. HLS label/NAME
 * remains an opaque identity at the adapter; this view never splits or rewrites it. */
export type TrackChoice = Readonly<{
  id: string; primary: string; secondary?: string; fullName: string;
  selected: boolean; unavailable?: boolean; addable?: boolean; /** The source's own reason a row cannot be used or added. */ reason?: string;
}>;
export type TrackChoicesProps = Readonly<{
  title: string; choices: readonly TrackChoice[]; layout?: MediaLayout;
  onSelect(id: string): void | Promise<void>;
  onAdd?(id: string): void | Promise<void>;
  footer?: string; reportError(error: unknown): void; t?: MediaTranslate;
}>;

/** The title's session choice only. Device preference defaults are never written here. */
export function TrackChoices({title, choices, layout = 'desktop', onSelect, onAdd, footer, reportError, t = defaultI18n.t}: TrackChoicesProps) {
  const rows = useRef<(HTMLButtonElement | null)[]>([]);
  const selected = choices.find(choice => choice.selected);
  const invoke = (action: () => void | Promise<void>) => { try { void Promise.resolve(action()).catch(reportError); } catch (error) { reportError(error); } };
  return <section className={styles.trackChoices} data-layout={layout} aria-label={title}>
    <h3>{title}{selected && <span>{selected.primary}</span>}</h3>
    <div role="radiogroup" aria-label={title}>{choices.map((choice, index) => <div className={styles.trackItem} key={choice.id}>
      <button type="button" ref={element => { rows.current[index] = element; }} role="radio" aria-checked={choice.selected} aria-disabled={choice.unavailable || choice.addable || undefined} aria-label={choice.fullName}
        tabIndex={choice.selected || !selected && index === 0 ? 0 : -1} className={styles.trackRow} data-selected={choice.selected || undefined} data-unavailable={choice.unavailable || undefined}
        onClick={() => { if (!choice.unavailable && !choice.addable) invoke(() => onSelect(choice.id)); }}
        onKeyDown={event => {
          const direction = ['ArrowDown', 'ArrowRight'].includes(event.key) ? 1 : ['ArrowUp', 'ArrowLeft'].includes(event.key) ? -1 : 0;
          if (!direction) return; event.preventDefault();
          const next = (index + direction + choices.length) % choices.length;
          rows.current[next]?.focus();
          const target = choices[next]; if (target && !target.unavailable && !target.addable) invoke(() => onSelect(target.id));
        }}>
        <span className={styles.trackText}>{choice.primary}{choice.secondary && <small>{choice.secondary}</small>}</span>
        {choice.unavailable ? <span className={styles.unavailable}>{choice.reason ?? t('player.opt.unavailable')}</span> : <Icon name="i-check" size={layout === 'tv' ? 28 : 20} className={styles.trackCheck} />}
      </button>
      {choice.addable && onAdd && <button type="button" className={styles.trackAdd} onClick={() => invoke(() => onAdd(choice.id))}>{t('player.opt.add')}</button>}
    </div>)}</div>
    {footer && <p className={styles.trackFooter}>{footer}</p>}
  </section>;
}
