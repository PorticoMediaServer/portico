import type {ReactNode} from 'react';
import {Artwork, type ArtworkSource} from '../artwork';
import {Button} from '../controls';
import {Icon, type IconName} from '../Icon';
import {Skeleton} from '../states';
import styles from './frames.module.css';

/** Rule 12: what an artwork box shows when there is no picture. A diagonal raised fill, a 1 px inner line and the
 * kind's icon at a third of the short side. It fills its parent, so the box is always full. */
export function ArtPlaceholder({icon}: {icon: IconName}) {
  return <span className={styles.placeholder} aria-hidden="true"><Icon name={icon} /></span>;
}

/** An artwork slot that keeps its box: the picture when there is one (arriving with the kit's fade), the placeholder
 * under it otherwise. `icon` is the kind's icon (music, book, film, tv). */
export function ArtBox({kind, source, icon, alt = ''}: {kind: 'poster' | 'square' | 'wide'; source?: ArtworkSource | null; icon: IconName; alt?: string}) {
  return <span className={styles.art} data-kind={kind}><ArtPlaceholder icon={icon} />{source && <Artwork source={source} alt={alt} />}</span>;
}

/** A block of a title page: its heading at the gutter, then its content. (A row of cards on a Browse page is the kit's `Shelf`.) */
export function Section({title, trailing, children}: {title: string; trailing?: ReactNode; children: ReactNode}) {
  return <section><div className={styles.sectionHead}><h2 className={styles.sectionHeading}>{title}</h2>{trailing}</div>{children}</section>;
}

/** Rule 9, stale data: the section keeps what it last had and says so beside its label. */
export type SectionStale = Readonly<{label: string; retryLabel: string; onRetry: () => void}>;

/** Rule 7: one group of a settings or owner page. Its label, then one raised card of rows. In-section loading,
 * empty and error are a `StateRow` as the card's only child. */
export function SectionCard({label, stale, children}: {label?: string; stale?: SectionStale; children: ReactNode}) {
  return <section>
    {label !== undefined && <div className={styles.cardLabel}><h3 className={styles.groupLabel}>{label}</h3>
      {stale && <><span className={`${styles.rowHelp} ${styles.stale}`}>{stale.label}</span><Button size="xs" onClick={stale.onRetry}>{stale.retryLabel}</Button></>}</div>}
    <div className={styles.card}>{children}</div>
  </section>;
}

export type StateRowProps =
  | Readonly<{kind: 'loading'}>
  | Readonly<{kind: 'empty' | 'error'; icon: IconName; title: string; reason?: string; action?: Readonly<{label: string; onPress: () => void}>}>;

/** Rule 9: loading, empty or error inside one section, as one row of the section's own card. It has a list row's
 * shape and height (tile, one bold line, one line of reason, one extra-small button), so nothing jumps when the rows
 * arrive, and the rest of the page carries on. Never a centred block and never a large title. */
export function StateRow(props: StateRowProps) {
  if (props.kind === 'loading') return <div className={styles.stateRow} aria-hidden="true"><Skeleton quiet width={30} height={30} radius={8} sheen /><div className={styles.stateLines}><Skeleton quiet line width="46%" /><Skeleton quiet line small width="72%" /></div></div>;
  return <div className={styles.stateRow} role={props.kind === 'error' ? 'alert' : undefined}>
    <span className={styles.tile} data-error={props.kind === 'error' || undefined}><Icon name={props.icon} size={18} /></span>
    <div className={styles.stateText}><b className={`${styles.rowLabel} ${styles.clamp2}`}>{props.title}</b>{props.reason !== undefined && <p className={`${styles.rowHelp} ${styles.clamp2}`}>{props.reason}</p>}</div>
    {props.action && <Button size="xs" onClick={props.action.onPress}>{props.action.label}</Button>}
  </div>;
}
