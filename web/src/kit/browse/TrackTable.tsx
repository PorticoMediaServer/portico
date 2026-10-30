import {useCallback, useEffect, useRef, type CSSProperties, type HTMLAttributes, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import type {CardTextModel, FactPart} from '@core/presentation/kit-model';
import {Button} from '../controls';
import {Icon} from '../Icon';
import type {BrowsePlatform} from './types';
import {trackWindow, type TrackWindow} from './model';
import styles from './browse.module.css';

const t = defaultI18n.t;
export type TrackRowData = Readonly<{
  id: string; text: CardTextModel; number?: number; plays?: number; addedAt?: string; duration?: FactPart;
  artwork?: ReactNode; favorite?: boolean; playing?: boolean; done?: boolean; current?: boolean; progress?: number;
  remaining?: FactPart; playable?: boolean;
}>;
export type TrackColumnId = 'number' | 'title' | 'artist' | 'album' | 'plays' | 'duration' | 'added' | 'actions';
export type TrackColumn = Readonly<{id: TrackColumnId; label?: ReactNode; width?: string; sort?: string}>;
type TrackRowAttributes = HTMLAttributes<HTMLDivElement>;
export type TrackActions = Readonly<{
  onPlay: (id: string) => void; onMore: (id: string, anchor?: HTMLButtonElement) => void; onFavorite?: (id: string, favorite: boolean) => void;
  onAddPlaylist?: (id: string) => void; renderSelection?: (row: TrackRowData) => ReactNode;
  getRowProps?: (row: TrackRowData) => TrackRowAttributes;
}>;
export type TrackTableProps = TrackActions & Readonly<{
  rows: readonly TrackRowData[]; variant: 'album' | 'top-songs' | 'songs' | 'chapters'; label: string;
  platform?: BrowsePlatform; columns?: readonly TrackColumn[]; sort?: Readonly<{field: string; direction: 'asc' | 'desc'}>;
  onSort?: (field: string) => void; startIndex?: number; totalCount?: number; height?: number; onWindowChange?: (window: TrackWindow) => void;
  className?: string; discNumber?: number;
}>;
const columnKeys = {number: 'next.browse.number', title: 'next.browse.title', artist: 'next.browse.artist', album: 'next.browse.album', plays: 'next.browse.plays', duration: 'next.browse.duration', added: 'next.browse.added'} as const;
const defaultColumns: Readonly<Record<TrackTableProps['variant'], readonly TrackColumn[]>> = {
  album: [{id: 'number', width: '44px'}, {id: 'title', width: 'minmax(0,1fr)'}, {id: 'plays', width: '90px'}, {id: 'duration', width: '80px'}, {id: 'actions', width: '84px'}],
  'top-songs': [{id: 'number', width: '44px'}, {id: 'title', width: 'minmax(0,1fr)'}, {id: 'plays', width: '90px'}, {id: 'duration', width: '80px'}, {id: 'actions', width: '84px'}],
  songs: [{id: 'number', width: '44px'}, {id: 'title', width: 'minmax(0,2.3fr)', sort: 'title'}, {id: 'artist', width: 'minmax(0,1.25fr)', sort: 'artist'}, {id: 'album', width: 'minmax(0,1.7fr)', sort: 'album'}, {id: 'duration', width: '86px', sort: 'duration'}, {id: 'plays', width: '64px', sort: 'playCount'}, {id: 'added', width: '124px', sort: 'added'}, {id: 'actions', width: '84px'}],
  chapters: [{id: 'number', width: '44px'}, {id: 'title', width: 'minmax(0,1fr)'}, {id: 'duration', width: '90px'}, {id: 'actions', width: '60px'}],
};
function rowDuration(row: TrackRowData): string | undefined { return row.duration?.text ?? row.text.detail.find(part => part.id === 'duration')?.text; }
function Levels() { return <span className={styles.levels} role="img" aria-label={t('next.browse.playing')}><i/><i/><i/><i/></span>; }
function Parts({parts}: Readonly<{parts: readonly FactPart[]}>) { return <>{parts.map(part => <span key={part.id}>{part.text}</span>)}</>; }
function RowActions({row, onMore, onFavorite, onAddPlaylist, compact = false}: Pick<TrackActions, 'onMore' | 'onFavorite' | 'onAddPlaylist'> & Readonly<{row: TrackRowData; compact?: boolean}>) {
  return <div className={styles.trackActions}>
    {onFavorite && !compact && <Button variant="icon" size="xs" icon={row.favorite ? 'i-heart-f' : 'i-heart'} iconOnly aria-pressed={!!row.favorite}
      className={styles.favoriteAction} aria-label={t(row.favorite ? 'next.browse.unfavorite' : 'next.browse.favorite', {title: row.text.title})} onClick={() => onFavorite(row.id, !row.favorite)}/>}
    {onAddPlaylist && !compact && <Button variant="icon" size="xs" icon="i-listadd" iconOnly aria-label={t('next.browse.addPlaylist', {title: row.text.title})} onClick={() => onAddPlaylist(row.id)}/>}
    <Button variant="icon" size="xs" icon="i-more" iconOnly aria-label={t('next.browse.moreFor', {title: row.text.title})} onClick={event => onMore(row.id, event.currentTarget)}/>
  </div>;
}
export function TrackTable({rows, variant, label, platform = 'desktop', columns: suppliedColumns, sort, onSort, startIndex = 0, totalCount = rows.length, height, onWindowChange, className, discNumber, ...actions}: TrackTableProps) {
  const columns = suppliedColumns ?? (platform === 'phone' ? [{id: 'number', width: '22px'}, {id: 'title', width: 'minmax(0,1fr)'}, {id: 'duration', width: '52px'}, {id: 'actions', width: '32px'}] as const : defaultColumns[variant]);
  const viewport = useRef<HTMLDivElement>(null), lastWindow = useRef<TrackWindow | null>(null);
  const rowHeight = platform === 'phone' ? 56 : variant === 'songs' ? 56 : 60;
  const windowed = height !== undefined;
  if (!Number.isInteger(startIndex) || !Number.isInteger(totalCount) || startIndex < 0 || totalCount < startIndex + rows.length || windowed && !onWindowChange || variant === 'songs' && !windowed || variant === 'chapters' && windowed) throw new Error('Invalid controlled track window');
  const style = {'--track-columns': columns.map(column => column.width ?? 'minmax(0,1fr)').join(' ')} as CSSProperties;
  const emitWindow = useCallback(() => {
    if (!viewport.current || height === undefined || !onWindowChange) return;
    const next = trackWindow(viewport.current.scrollTop, height, rowHeight, totalCount);
    if (next.start !== lastWindow.current?.start || next.end !== lastWindow.current?.end) { lastWindow.current = next; onWindowChange(next); }
  }, [height, onWindowChange, rowHeight, totalCount]);
  useEffect(emitWindow, [emitWindow]);
  const cell = (row: TrackRowData, column: TrackColumn) => {
    switch (column.id) {
      case 'number': return <span className={styles.trackNumber}>{actions.renderSelection && <span className={styles.trackSelection}>{actions.renderSelection(row)}</span>}
        <button type="button" className={styles.trackPlay} disabled={row.playable === false} onClick={() => actions.onPlay(row.id)} aria-label={t('next.browse.play', {title: row.text.title})}>
          {row.playing ? <Levels/> : row.done ? <Icon name="i-check" size={18}/> : <><span className={styles.trackOrdinal}>{row.number === undefined ? undefined : defaultI18n.number(row.number)}</span><Icon name="i-play" size={16} className={styles.ordinalPlay}/></>}
        </button></span>;
      case 'title': return <button type="button" className={styles.trackTitle} disabled={row.playable === false} onClick={() => actions.onPlay(row.id)}>
        {row.artwork && <span className={styles.trackArtwork}>{row.artwork}</span>}<span className={styles.trackText}><b>{row.text.title}</b>
          {variant === 'top-songs' && row.text.caption.some(part => part.id === 'album') && <small><Parts parts={row.text.caption.filter(part => part.id === 'album')}/></small>}
          {row.current && row.progress !== undefined && <span className={styles.chapterTimeline}><span className={styles.chapterProgress} aria-hidden="true"><i style={{width: `${Math.max(0, Math.min(1, row.progress)) * 100}%`}}/></span>{row.remaining && <small>{row.remaining.text}</small>}</span>}
        </span></button>;
      case 'artist': case 'album': return row.text.caption.find(part => part.id === column.id)?.text;
      case 'plays': return row.plays === undefined ? undefined : defaultI18n.number(row.plays);
      case 'duration': return rowDuration(row);
      case 'added': return row.addedAt ? defaultI18n.date(row.addedAt, 'medium') : undefined;
      case 'actions': return variant === 'chapters' ? <div className={styles.trackActions}><Button variant="icon" size="xs" icon="i-play" iconOnly aria-label={t(row.current ? 'next.browse.resume' : 'next.browse.play', {title: row.text.title})} onClick={() => actions.onPlay(row.id)}/></div> : <RowActions row={row} compact={platform === 'phone'} {...actions}/>;
    }
  };
  return <div className={[styles.trackTable, className].filter(Boolean).join(' ')} data-variant={variant} data-platform={platform} role="table" aria-label={label} aria-rowcount={totalCount + (variant === 'chapters' || platform === 'phone' ? 0 : 1)}>
    {discNumber !== undefined && <h3 className={styles.discHeading}>{t('next.browse.disc', {number: discNumber})}</h3>}
    {variant !== 'chapters' && platform !== 'phone' && <div className={styles.trackHeading} role="row" style={style}>{columns.map(column => <div key={column.id} role="columnheader" data-column={column.id}
      aria-sort={column.sort && sort?.field === column.sort ? sort.direction === 'asc' ? 'ascending' : 'descending' : undefined}>
      {column.sort && onSort ? <button type="button" onClick={() => onSort(column.sort!)}>{column.label ?? (column.id === 'actions' ? undefined : t(columnKeys[column.id]))}{sort?.field === column.sort && <Icon name={sort.direction === 'asc' ? 'i-arrow-up' : 'i-arrow-down'} size={14}/>}</button> : column.label ?? (column.id === 'actions' ? undefined : t(columnKeys[column.id]))}
    </div>)}</div>}
    <div className={windowed ? styles.trackViewport : undefined} style={windowed ? {height} : undefined} ref={viewport} onScroll={emitWindow} role="rowgroup">
      {startIndex > 0 && <div role="presentation" style={{height: startIndex * rowHeight}}/>}
      {rows.map((row, index) => { const rowProps = actions.getRowProps?.(row); return <div {...rowProps} className={[styles.trackRow, rowProps?.className].filter(Boolean).join(' ')} key={row.id} role="row" aria-rowindex={startIndex + index + (variant === 'chapters' || platform === 'phone' ? 1 : 2)} aria-current={row.current || undefined} data-playing={row.playing || undefined} data-favorite={row.favorite || undefined} data-done={row.done || undefined} data-current={row.current || undefined} style={{...rowProps?.style, ...style}}>
        {columns.map(column => <div key={column.id} role="cell" data-column={column.id}>{cell(row, column)}</div>)}
      </div>; })}
      {totalCount > startIndex + rows.length && <div role="presentation" style={{height: (totalCount - startIndex - rows.length) * rowHeight}}/>}
    </div>
  </div>;
}
export type SongRowProps = Omit<HTMLAttributes<HTMLDivElement>, 'onPlay'> & Readonly<{row: TrackRowData; onPlay: (id: string) => void; onMore?: (id: string) => void; selection?: ReactNode; context?: 'list' | 'search'}>;
export function SongRow({row, onPlay, onMore, selection, context = 'list', className, ...props}: SongRowProps) {
  return <div {...props} className={[styles.songRow, className].filter(Boolean).join(' ')} data-playing={row.playing || undefined} data-context={context} data-selectable={!!selection || undefined} data-artwork={!!row.artwork || undefined}>
    {context !== 'search' && <span className={styles.songNumber}>{selection ?? (row.playing ? <Levels/> : row.number === undefined ? undefined : defaultI18n.number(row.number))}</span>}
    <button type="button" className={styles.songMain} onClick={() => onPlay(row.id)}>{row.artwork && <span className={styles.songArtwork}>{row.artwork}{context === 'search' && <Icon name="i-play" size={22} className={styles.songArtworkPlay} />}</span>}
      <span className={styles.songText}><b>{row.text.title}</b>{row.text.caption.length > 0 && <small><Parts parts={row.text.caption}/></small>}</span><span className={styles.songDuration}>{rowDuration(row)}</span></button>
    {context === 'search' && selection && <span className={styles.songSelection}>{selection}</span>}
    {onMore && <Button variant="icon" size="xs" icon="i-more" iconOnly aria-label={t('next.browse.moreFor', {title: row.text.title})} onClick={() => onMore(row.id)}/>}
  </div>;
}
