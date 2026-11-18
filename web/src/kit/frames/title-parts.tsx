import {useEffect, useRef, useState, type CSSProperties, type ReactNode} from 'react';
import type {ArtworkSource} from '../artwork';
import {Artwork, PersonCircle, nameInitials} from '../artwork';
import {Button, Chip, Segmented, Switch, TextField} from '../controls';
import {Icon, type IconName} from '../Icon';
import {Popover} from '../overlays';
import styles from './title-parts.module.css';

/** The parts a title page's tab panel and side are built from (desktop-detail f1 to f8, phone-detail d1 to d3,
 * desktop-listen). The title page and the gallery draw these same parts. FRAMES.md lists them. */

/** The bar over a show's episodes (desktop-detail f5, f6; phone-detail d2; tablet f8): the season control, the
 * season's count and watched progress, then the episode order, Filters, Hide watched, list or tiles, and the
 * season's ⋯. Moved from the show page (`screens/detail/Show.tsx`), which draws this same part. Below 1024 the
 * progress and the list or tiles toggle are not drawn (the phone keeps the list). */
export function SeasonBar({season, count, progress, actions, hideWatched, layout, more}: Readonly<{
  /** The season trigger: `SeasonMenu`, or a menu of groups. */
  season: ReactNode;
  count?: string;
  progress?: Readonly<{watched: number; total: number; label: string}>;
  /** Episode order and Filters, at the bar's end. */
  actions?: ReactNode;
  hideWatched?: Readonly<{label: string; checked: boolean; onCheckedChange: (checked: boolean) => void}>;
  layout?: Readonly<{value: 'list' | 'grid'; onValueChange: (value: 'list' | 'grid') => void; label: string; listLabel: string; gridLabel: string}>;
  /** The season's own menu (Play season, Mark season as watched, the owner's Episode display). */
  more?: ReactNode;
}>) {
  return <div className={styles.toolbar}>
    {season}
    {count !== undefined && <span className={styles.progress}>{count}</span>}
    {progress && <><span className={styles.progressDivider} aria-hidden="true" /><span className={styles.watchedProgress}><span className={styles.watchedMeter} aria-hidden="true"><i style={{width: `${progress.total > 0 ? 100 * progress.watched / progress.total : 0}%`}} /></span>{progress.label}</span></>}
    <div className={styles.toolbarActions}>
      {actions}
      {hideWatched && <label className={styles.hide}>{hideWatched.label}<Switch checked={hideWatched.checked} onCheckedChange={hideWatched.onCheckedChange} /></label>}
      {layout && <span className={styles.layoutToggle} role="group" aria-label={layout.label}>
        <button type="button" aria-label={layout.listLabel} aria-pressed={layout.value === 'list'} onClick={() => layout.onValueChange('list')}><Icon name="i-list" size={18} /></button>
        <button type="button" aria-label={layout.gridLabel} aria-pressed={layout.value === 'grid'} onClick={() => layout.onValueChange('grid')}><Icon name="i-grid" size={18} /></button>
      </span>}
      {more}
    </div>
  </div>;
}

export type SeasonMenuRow = Readonly<{id: string; label: string; years?: string; count?: string; watched?: boolean; progress?: number; special?: boolean}>;
/** The season menu for a show with many seasons (desktop-detail f6, phone-detail d2 frame 5): every loaded season with
 * its years, episode count and how much is watched, Specials at the end, and a box to type a season number when there
 * are more than 30. Moved from the show page (`screens/detail/SeasonMenu.tsx`, which now maps its seasons to rows). */
export function SeasonMenu({open, onOpenChange, label, title, heading, rows, selectedId, watchedLabel, jump, paging, onSelect}: Readonly<{
  open: boolean; onOpenChange: (open: boolean) => void;
  /** The trigger's label: the season shown. */
  label: string;
  /** The menu's accessible name. */
  title: string;
  /** "36 seasons". */
  heading: string;
  rows: readonly SeasonMenuRow[]; selectedId?: string;
  watchedLabel: string;
  /** Shown when the show has more than 30 seasons. */
  jump?: Readonly<{placeholder: string; range: string; maximum: number; onJump: (number: number) => Promise<boolean>}>;
  paging?: Readonly<{earlierLabel?: string; laterLabel?: string; onEarlier: () => void; onLater: () => void}>;
  onSelect: (id: string) => void;
}>) {
  const [query, setQuery] = useState('');
  const firstRow = useRef<HTMLButtonElement>(null), jumpField = useRef<HTMLInputElement>(null), list = useRef<HTMLDivElement>(null);
  useEffect(() => { if (!open) setQuery(''); }, [open]);
  const submit = async () => {
    const number = Number(query);
    if (!jump || !Number.isSafeInteger(number) || number < 1 || number > jump.maximum) return;
    if (await jump.onJump(number)) onOpenChange(false);
  };
  const row = (season: SeasonMenuRow, first: boolean) => <button key={season.id} ref={first ? firstRow : undefined} type="button" role="option" aria-selected={season.id === selectedId}
    className={styles.seasonRow} onClick={() => onSelect(season.id)}>
    <span className={styles.seasonName}><b>{season.label}</b>{season.years !== undefined && <small>{season.years}</small>}</span>
    <span className={styles.seasonFacts}>{season.count !== undefined && <span>{season.count}</span>}
      {season.watched ? <span className={styles.seasonWatched}><Icon name="i-check" size={14} />{watchedLabel}</span>
        : season.progress !== undefined && season.progress > 0 ? <span className={styles.seasonProgress} aria-hidden="true"><i style={{width: `${Math.round(100 * season.progress)}%`}} /></span> : null}
    </span>
  </button>;
  const regular = rows.filter(season => !season.special), specials = rows.filter(season => season.special);
  return <Popover open={open} onOpenChange={value => onOpenChange(value)} title={title} trigger={<Chip variant="toolbar" trailingIcon="i-down">{label}</Chip>}
    placement="bottom-start" width={380} className={styles.seasonPopover} initialFocusRef={jump ? jumpField : firstRow}>
    <header className={styles.seasonHeader}><b>{heading}</b>
      {jump && <form className={styles.seasonJump} onSubmit={event => { event.preventDefault(); void submit(); }}>
        <TextField ref={jumpField} variant="compact" leadingIcon="i-search" inputMode="numeric" aria-label={jump.placeholder} placeholder={jump.placeholder}
          value={query} onChange={event => setQuery(event.currentTarget.value.replace(/[^0-9]/g, '').slice(0, 4))} trailing={<span>{jump.range}</span>} />
      </form>}
    </header>
    <div ref={list} role="listbox" aria-label={title} onKeyDown={event => {
      if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return;
      const options = [...(list.current?.querySelectorAll<HTMLButtonElement>('[role="option"]') ?? [])];
      if (!options.length) return;
      const index = options.indexOf(document.activeElement as HTMLButtonElement);
      const next = event.key === 'Home' ? 0 : event.key === 'End' ? options.length - 1 : index < 0 ? 0 : (index + (event.key === 'ArrowDown' ? 1 : -1) + options.length) % options.length;
      event.preventDefault(); options[next]?.focus();
    }}>
      <div className={styles.seasonList}>{regular.map((season, index) => row(season, index === 0))}</div>
      {specials.map(season => <div className={styles.seasonSpecials} key={season.id}>{row(season, false)}</div>)}
    </div>
    {paging && (paging.earlierLabel || paging.laterLabel) && <div className={styles.seasonPaging}>
      {paging.earlierLabel && <button type="button" onClick={paging.onEarlier}>{paging.earlierLabel}</button>}
      {paging.laterLabel && <button type="button" onClick={paging.onLater}>{paging.laterLabel}</button>}
    </div>}
  </Popover>;
}

/** A show's episodes: the kit `EpisodeTile`s as a list (picture beside the words; from 600 the synopsis beside the
 * picture under the title, tablets.md SC-5) or as tiles in rows of `columns` (desktop-detail f5, f6). Each tile takes
 * `episodeRowClass`; an episode opened in place is `EpisodeListOpened`, after the last tile of its row. Moved from the
 * show page (`show.module.css` `.tiles`, `.episodeRow`, `.expanded`). */
export function EpisodeList({layout, columns = 4, children}: Readonly<{layout: 'list' | 'grid'; columns?: number; children: ReactNode}>) {
  return <div className={styles.tiles} data-layout={layout} style={{'--episode-columns': columns} as CSSProperties}>{children}</div>;
}
export const episodeRowClass = styles.episodeRow;
export function EpisodeListOpened({children}: {children: ReactNode}) {
  return <div className={styles.expanded}>{children}</div>;
}
/** Previous or Next episode beside an episode opened in place (desktop-detail f5, phone-detail d2 frame 4). */
export function EpisodeNeighbour({direction, label, title, detail, artwork, onPress}: Readonly<{direction: 'previous' | 'next'; label: string; title: string; detail?: string; artwork?: ArtworkSource; onPress: () => void}>) {
  return <button type="button" className={styles.episodeNav} onClick={onPress}>
    <span className={styles.episodeNavArt}><Artwork source={artwork} alt="" /></span>
    <span className={styles.episodeNavCopy}><small>{direction === 'previous' && <Icon name="i-left" size={14} />}{label}{direction === 'next' && <Icon name="i-right" size={14} />}</small><b>{title}</b>{detail !== undefined && <small>{detail}</small>}</span>
  </button>;
}

/** A heading over a block of the tab panel or the side: About, Cast and crew, Films in order, Chapters. `note` is the
 * grey line after it ("By release date"), `trailing` its control (See all, a sort). */
export function PanelHeading({title, count, note, trailing}: Readonly<{title: string; count?: string; note?: string; trailing?: ReactNode}>) {
  return <div className={styles.heading}><h2>{title}{count !== undefined && <small>{count}</small>}</h2>{note !== undefined && <span className={styles.note}>{note}</span>}{trailing && <span className={styles.headingEnd}>{trailing}</span>}</div>;
}

/** A value in the About table that opens something (a genre, a person, a studio): underlined, as drawn. */
export type AboutValue = Readonly<{text: string; onPress?: () => void}>;
/** The About facts (desktop-detail f3, f4, f8; phone-detail d1 frame 2; desktop-listen album, artist and book): a label
 * and a value per row; a value with `onPress` is a link. On a phone the table sits on a raised card. */
export function AboutTable({rows}: Readonly<{rows: readonly Readonly<{label: string; values: readonly AboutValue[]}>[]}>) {
  return <dl className={styles.about}>{rows.map(row => <div key={row.label}><dt>{row.label}</dt><dd>{row.values.map((value, index) => <span key={index}>{index > 0 && ', '}
    {value.onPress ? <button type="button" className={styles.link} onClick={value.onPress}>{value.text}</button> : value.text}</span>)}</dd></div>)}</dl>;
}

export type CastPerson = Readonly<{id: string; name: string; role?: string; portrait?: ArtworkSource; onPress?: () => void}>;
/** Cast and crew as circles with the name and role under each (desktop-detail f3, phone-detail d1 frame 2, foldable f2):
 * initials over a wash when there is no photo. The cast, then the crew under a rule. */
export function CastRow({groups}: Readonly<{groups: readonly (readonly CastPerson[])[]}>) {
  return <div className={styles.cast}>{groups.filter(group => group.length > 0).map((group, index) => <div key={index} className={styles.castGroup}>{group.map(person =>
    <button key={person.id} type="button" className={styles.person} disabled={!person.onPress} onClick={person.onPress}>
      <PersonCircle covers={person.portrait ? [person.portrait] : []} initials={nameInitials(person.name)} alt="" /><b>{person.name}</b>{person.role !== undefined && <small>{person.role}</small>}
    </button>)}</div>)}</div>;
}

/** The audio and subtitles for the next play, one quiet row under the synopsis on a phone (phone-detail d1 frame 1):
 * "English 7.1 · Automatic", opening one sheet. */
export function TrackPicker({audio, subtitles, label, onPress}: Readonly<{audio: string; subtitles: string; label: string; onPress: () => void}>) {
  return <button type="button" className={styles.picker} aria-label={label} onClick={onPress}>
    <span><Icon name="i-vol" size={16} />{audio}</span><span><Icon name="i-subs" size={16} />{subtitles}</span><Icon name="i-updown" size={16} />
  </button>;
}

/** One numbered row of a list in order (desktop-detail f8 "Films in order", phone-detail d3 frame 8): the number, the
 * poster, the title with its facts and synopsis, and the row's state and action at the end. */
export function NumberedRow({number, artwork, title, facts, synopsis, status, action, progress}: Readonly<{
  number: number; artwork?: ArtworkSource; title: string; facts?: ReactNode; synopsis?: string;
  /** "Watched", "Up next", "Not started", with its icon. */
  status?: Readonly<{label: string; icon?: IconName; accent?: boolean}>;
  action?: ReactNode;
  progress?: number;
}>) {
  return <div className={styles.numbered}>
    <span className={styles.number} aria-hidden="true">{number}</span>
    <span className={styles.numberedArt}><Artwork source={artwork} alt="" />{progress !== undefined && <i style={{width: `${progress * 100}%`}} />}</span>
    <div className={styles.numberedCopy}><b>{title}</b>{facts}{synopsis !== undefined && <p>{synopsis}</p>}</div>
    {(status || action) && <div className={styles.numberedEnd}>{status && <span data-accent={status.accent || undefined}>{status.icon && <Icon name={status.icon} size={14} />}{status.label}</span>}{action}</div>}
  </div>;
}

/** The owner's card in the side (desktop-detail f8 "This collection", desktop-listen "Files"): a title, the "Owner
 * only" mark, a sentence and its actions. */
export function OwnerCard({title, mark, body, actions}: Readonly<{title: string; mark: string; body: string; actions?: readonly Readonly<{icon: IconName; label: string; onPress: () => void}>[]}>) {
  return <section className={styles.owner}><header><b>{title}</b><span>{mark}</span></header><p>{body}</p>
    {actions && actions.length > 0 && <div>{actions.map(action => <Button key={action.label} variant="text" size="small" icon={action.icon} onClick={action.onPress}>{action.label}</Button>)}</div>}
  </section>;
}

/** A block of the tab panel or the side that is not a row of cards (the season bar and episodes, About, the cast, a
 * list in order), at the page's gutter. A row of cards is the kit `Shelf`, which places itself. */
export function PanelBlock({label, children}: Readonly<{label?: string; children: ReactNode}>) {
  return <section className={styles.block} aria-label={label}>{children}</section>;
}

/** A paragraph in the panel or the side: `body` is a book's or a collection's About text (rule 6 intro), `note` the
 * grey line under a list (desktop-detail f7 "Only titles on this server are listed"). */
export function PanelText({tone = 'body', children}: Readonly<{tone?: 'body' | 'note'; children: ReactNode}>) {
  return <p className={styles.text} data-tone={tone}>{children}</p>;
}

/** "Play a different version" (detail-phone 9b, desktop-detail 9b): one row of two lines per version, in File info's
 * order: the edition or the picture summary, then the rest. The whole row is the button and plays that version at once;
 * hovered or focused it lightens and shows a play mark. On a phone the rows sit on one raised card with hairlines. */
export function VersionRows({rows, onChoose}: Readonly<{rows: readonly Readonly<{id: string; heading: string; detail: string}>[]; onChoose: (id: string) => void}>) {
  return <div className={styles.versions}>{rows.map(row => <button key={row.id} type="button" className={styles.version} onClick={() => onChoose(row.id)}>
    <span><b>{row.heading}</b><small>{row.detail}</small></span><Icon name="i-play" size={16} /></button>)}</div>;
}

/** The owner's Episode display (desktop-detail f4): how a show's episodes are grouped, numbered and ordered, for everyone
 * on the server. Its heading and the "For everyone" mark, a line on what it does, one choice per row, and how an
 * episode will read. It commits as you choose (R9 `choice`). */
export function EpisodeDisplay({title, mark, intro, rows, preview}: Readonly<{title: string; mark: string; intro: string;
  rows: readonly Readonly<{label: string; items: readonly Readonly<{value: string; label: string}>[]; value: string; onValueChange: (value: string) => void}>[];
  /** "Reads as" and the episode as it will read ("S1 E3", "Long, Long Time"). */
  preview: Readonly<{label: string; code: string; title: string}>}>) {
  return <div className={styles.display}>
    <header><b>{title}</b><span>{mark}</span></header><p>{intro}</p>
    {rows.map(row => <div key={row.label} className={styles.displayRow}><span>{row.label}</span><Segmented aria-label={row.label} items={row.items} value={row.value} onValueChange={row.onValueChange} /></div>)}
    <p className={styles.readsAs}>{preview.label} <b>{preview.code}</b> {preview.title}</p>
  </div>;
}
