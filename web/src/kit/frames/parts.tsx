import type {ReactNode} from 'react';
import {Artwork, type ArtworkSource} from '../artwork';
import {Button, Switch} from '../controls';
import {Icon, type IconName} from '../Icon';
import {SettingsGroup} from '../settings/SettingsGroups';
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

/** A block that is not a row of cards (a title page's episode list or details): its heading at the gutter, then its
 * content. A row of cards is the kit `Shelf`, which draws its own heading; never put a `Shelf` inside a `Section`. */
export function Section({title, subtitle, trailing, children}: {title: string; /** A quieter line beside the heading: "Movies, TV Shows and Anime". */ subtitle?: string; trailing?: ReactNode; children: ReactNode}) {
  return <section><div className={styles.sectionHead}><div className={styles.sectionTitle}><h2 className={styles.rowHeading}>{title}</h2>{subtitle && <span className={styles.rowHelp}>{subtitle}</span>}</div>{trailing}</div>{children}</section>;
}

/** Rule 9, stale data: the section keeps what it last had and says so beside its label. */
export type SectionStale = Readonly<{label: string; retryLabel: string; onRetry: () => void}>;

/** Rule 7: one group of a settings or owner page. Its label, then one raised card of rows. In-section loading,
 * empty and error are a `StateRow` as the card's only child. The kit's `SettingsGroup`, at the frame's inset. */
export function SectionCard({label, help, stale, children}: {label?: string; /** The section's help paragraph under its label (desktop-settings f1 "When the server has found an intro…"). */ help?: string; stale?: SectionStale; children: ReactNode}) {
  return <div className={styles.groupSlot}><SettingsGroup title={label} description={help}
    actions={stale && <><span className={`${styles.rowHelp} ${styles.stale}`}>{stale.label}</span><Button size="xs" onClick={stale.onRetry}>{stale.retryLabel}</Button></>}>{children}</SettingsGroup></div>;
}

export type StateRowProps =
  | Readonly<{kind: 'loading'}>
  | Readonly<{kind: 'empty' | 'error'; icon: IconName; title: string; reason?: string; action?: Readonly<{label: string; onPress: () => void}>}>;

/** Rule 9: loading, empty or error inside one section, as one row of the section's own card. It has a list row's
 * shape and height (tile, one bold line, one line of reason, one extra-small button), so nothing jumps when the rows
 * arrive, and the rest of the page carries on. Never a centred block and never a large title. */
export function StateRow(props: StateRowProps) {
  if (props.kind === 'loading') return <div className={styles.stateRow} aria-hidden="true"><Skeleton quiet className={styles.tile} sheen /><div className={styles.stateLines}><Skeleton quiet line width="46%" /><Skeleton quiet line small width="72%" /></div></div>;
  return <div className={styles.stateRow} role={props.kind === 'error' ? 'alert' : undefined}>
    <span className={styles.tile} data-error={props.kind === 'error' || undefined}><Icon name={props.icon} /></span>
    <div className={styles.stateText}><b className={`${styles.rowLabel} ${styles.clamp2}`}>{props.title}</b>{props.reason !== undefined && <p className={`${styles.rowHelp} ${styles.clamp2}`}>{props.reason}</p>}</div>
    {props.action && <Button size="xs" onClick={props.action.onPress}>{props.action.label}</Button>}
  </div>;
}

/** The episodes bar under a show's tabs (phone-player 4, tablet-play 3): the season trigger, the season's count, and
 * Hide watched at the end. A stand-in for the gallery's watch page: the live show page draws its own bar (a toolbar Chip
 * with the season menu and the owner's menu), which Stage 2 moves into the kit in this part's place (FRAMES.md). */
export function SeasonBar({label, count, onSeasons, hideWatched, hideWatchedLabel, onHideWatched}: {label: string; count?: string; onSeasons: () => void; hideWatched: boolean; hideWatchedLabel: string; onHideWatched: (value: boolean) => void}) {
  return <div className={styles.seasonBar}>
    <Button size="small" onClick={onSeasons}>{label}<Icon name="i-chdown" size={16} /></Button>
    {count !== undefined && <span className={styles.rowHelp}>{count}</span>}
    <label className={styles.seasonHide}>{hideWatchedLabel}<Switch checked={hideWatched} onCheckedChange={onHideWatched} /></label>
  </div>;
}

/** A show's episodes as rows: each kit `EpisodeTile` inside is laid out with its picture beside its words and its
 * synopsis under both. (The show page's own copy of these rules, `screens/detail/show.module.css` `.episodeRow`, goes in Stage 2.) */
export function EpisodeRows({children}: {children: ReactNode}) {
  return <div className={styles.episodeRows}>{children}</div>;
}
