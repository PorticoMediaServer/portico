import {useCallback, useEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent, type ReactNode} from 'react';
import {channelUnavailableMessage} from '@core/presentation/live-availability.ts';
import {defaultI18n} from '@i18n';
import {layoutRow, legacyChannel, type GuideCell, type GuideChannelRow, type GuideProgram} from '@core/guide/index.ts';
import {rulerTicks, type RulerTick} from '@core/guide/time.ts';
import {programmeFactParts, type ChannelMarkModel, type FactPart} from '@portico/client-core/kit-model';
import {ChannelMark, nameInitials, type ArtworkSource} from '../../kit/artwork';
import {Button, Chip, Segmented, Tabs} from '../../kit/controls';
import type {SelectOption} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import {Menu, type MenuEntry} from '../../kit/overlays';
import {Skeleton, StateView} from '../../kit/states';
import {LiveFrame} from '../../kit/frames';
import {PickChip} from './PickChip';
import {liveListPrograms} from './program-window';
import {useGuideSwipe} from './guide-swipe';
import {useChannelMenuGestures} from './channel-menu';
import {guideListRowHeight, guideVisibleRows} from './guide-window';
import type {RowWitness} from './row-witness';
import {useWindowHeight, useWindowWidth} from '../../kit/theme';
import styles from './live.module.css';

const t = defaultI18n.t;
/** The drawn guide insets each programme cell by 2px inside its true span. */
export const GUIDE_CELL_INSET = 2;
const FLAG_PARTS = new Set(['live', 'new', 'premiere']);
const FLAG_KINDS: Readonly<Record<string, 'live' | 'new' | 'premiere'>> = {live: 'live', new: 'new', premiere: 'premiere'};

export type GuideCellFacts = Readonly<{flags: readonly FactPart[]; detail: readonly FactPart[]}>;

/**
 * A programme cell shows its status flags, then either the episode it is airing or, for a
 * programme with no episode facts, the airing time (desktop-live.html#live-guide cells).
 */
export function guideCellFacts(parts: readonly FactPart[]): GuideCellFacts {
  const flags = parts.filter(part => FLAG_PARTS.has(part.id));
  const detail = parts.filter(part => part.id === 'episode-label' || part.id === 'programme-subtitle');
  return {flags, detail: detail.length ? detail : parts.filter(part => part.id === 'time-range')};
}

/** How far through an airing the guide draws its progress bar; absent for every other state. */
export function guideCellProgress(cell: Pick<GuideCell, 'state' | 'start' | 'end'>, now: number): number | undefined {
  if (cell.state !== 'now' || !(cell.end > cell.start)) return undefined;
  return Math.min(1, Math.max(0, (now - cell.start) / (cell.end - cell.start)));
}

export {guideVisibleRows} from './guide-window';

export type GuideRowData = Readonly<{
  /** Position in the complete filtered order; rows are placed by it. */
  index: number;
  channel?: GuideChannelRow;
  /** The source store's own membership witness for this exact row. */
  witness?: RowWitness;
  programs: readonly GuideProgram[];
  /** The source answered this row's programmes completely. */
  complete: boolean;
  failed?: boolean;
}>;

export type GuideScreenProps = Readonly<{
  title: string;
  /** A channel logo's artwork at the mark's size; absent draws the marks alone. */
  channelLogo?: (url: string | undefined) => ArtworkSource | undefined;
  switcher?: Readonly<{value: string; options: readonly SelectOption[]; onChange: (value: string) => void}>;
  summary?: Readonly<{name: string; channels?: number; through?: string}>;
  conflicts?: number;
  onTabChange?(value: 'recordings'): void;
  chips: readonly Readonly<{id: string; label: string; count?: number; favorites?: boolean; selected: boolean; onSelect: () => void}>[];
  onNow?: ReactNode;
  /** A page-level problem (channel page failed) shown above the guide. */
  problem?: ReactNode;
  /** Guide-wide notices from the server's own state (a stale guide, Live TV unavailable here). */
  notice?: ReactNode;
  /** Replaces the guide grid when the filtered view has nothing to draw. */
  empty?: ReactNode;
  day: Readonly<{label: string; value: string; options: readonly SelectOption[]; onChange: (value: string) => void}>;
  sort: Readonly<{value: string; options: readonly SelectOption[]; onChange: (value: string) => void}>;
  view: 'guide' | 'list';
  time: Readonly<{earlier: string; now: string; later: string; onEarlier: () => void; onNow: () => void; onLater: () => void}>;
  onViewChange(view: 'guide' | 'list'): void;
  span: Readonly<{start: number; end: number; now: number; scale: number; column: number; rowHeight: number; timezone: string}>;
  rows: readonly GuideRowData[];
  total: number;
  selected?: Readonly<{channelKey?: string; channelId: string; programmeId?: string}>;
  onSelect(channel: GuideChannelRow, programme?: GuideProgram, witness?: RowWitness): void;
  onPlay?(channel: GuideChannelRow, witness?: RowWitness): void;
  onRecord?(channel: GuideChannelRow, programme?: GuideProgram, witness?: RowWitness): void;
  /** A drawn row's channel menu (desktop-live List view, tablet-play 6). */
  channelMenu?(channel: GuideChannelRow, witness?: RowWitness): readonly MenuEntry[];
  onVisibleWindow?(first: number, last: number): void;
  onRetry?(): void;
}>;

function CellButton({cell, channel, witness, now, timezone, selected, onSelect}: Readonly<{
  cell: GuideCell; channel: GuideChannelRow; witness?: RowWitness; now: number; timezone: string; selected: boolean;
  onSelect: (channel: GuideChannelRow, programme: GuideProgram, witness?: RowWitness) => void;
}>) {
  const programme = cell.program!;
  const parts = programmeFactParts({startMs: programme.start, endMs: programme.end,
    episode: programme.episode ? {mode: 'seasonal', season: programme.episode.season, episode: programme.episode.number} : undefined,
    subtitle: programme.subtitle, categories: programme.categories, year: programme.year, rating: programme.rating, flags: programme.flags,
    recording: programme.recording?.state === 'recording'},
  {t, locale: defaultI18n.region.formatLocale, timeZone: timezone});
  const facts = guideCellFacts(parts), progress = guideCellProgress(cell, now), recording = programme.recording?.state;
  const label = t('guide.cellTime', {start: defaultI18n.time(programme.start), end: defaultI18n.time(programme.end)});
  return <button type="button" className={styles.guideCell} data-kind="program" data-program-id={programme.id} data-program-start={String(programme.start)} data-state={cell.state} data-tier={cell.tier} data-selected={selected || undefined}
    style={{left: cell.x + GUIDE_CELL_INSET, width: Math.max(0, cell.width - GUIDE_CELL_INSET)}}
    aria-label={[channel.number, channel.name, programme.title, label, recording === 'recording' ? t('next.kit.recording') : recording === 'scheduled' ? t('guide.recordingScheduled') : undefined].filter(Boolean).join(', ')} aria-current={selected || undefined} onClick={() => onSelect(channel, programme, witness)}>
    <span className={styles.guideCellTitle}>
      {cell.clippedStart && <Icon className={styles.guideClip} name="i-left" size={13}/>}
      {recording === 'recording' && <i className={styles.guideRecordDot} data-pulse title={t('next.kit.recording')}/>}
      {recording === 'scheduled' && <i className={styles.guideRecordDot} title={t('guide.recordingScheduled')}/>}
      <span className={styles.guideCellName}>{programme.title}</span>
    </span>
    {cell.tier !== 'sliver' && (recording === 'recording' || facts.flags.length > 0 || facts.detail.length > 0) &&
      <span className={styles.guideCellDetail}>
        {recording === 'recording' && <span className={styles.guideRecording}>{t('next.kit.recording')}</span>}
        {facts.flags.map(part => <span key={part.id} className={styles.guideFlag} data-flag={FLAG_KINDS[part.id]}>{part.text}</span>)}
        {facts.detail.map(part => <span key={part.id}>{part.text}</span>)}
      </span>}
    {progress !== undefined && <span className={styles.guideProgress} aria-hidden="true"><i style={{width: `${(progress * 100).toFixed(3)}%`}}/></span>}
  </button>;
}

function GuideChannelCell({channel, list, phone, landscape = false, logo, children}: Readonly<{channel?: GuideChannelRow; list: boolean; phone: boolean; landscape?: boolean;
  /** The channel's own logo, drawn over its mark once it loads. */ logo?: (url: string | undefined) => ArtworkSource | undefined; children?: ReactNode}>) {
  if (!channel) return <div className={styles.guideChannel}><Skeleton width={phone ? 36 : 180} height={34}/></div>;
  // A channel with no published mark still draws one: its initials on its tint (desktop-live: "NN"), never an empty square.
  const mark: ChannelMarkModel = channel.mark ? {sourceId: channel.sourceId ?? '', id: channel.id, text: channel.mark.text, seed: channel.mark.seed}
    : {sourceId: channel.sourceId ?? '', id: channel.id, text: nameInitials(channel.name) || channel.number, seed: `${channel.sourceId ?? ''}:${channel.id}`};
  const size = list && phone ? 46 : landscape ? 32 : 40;
  return <div className={list ? styles.guideListChannel : styles.guideChannel}>
    <ChannelMark className={styles.guideChannelMark} mark={mark} source={logo?.(channel.logoUrl)} size={size}/>
    <span className={styles.guideChannelText}>
      <b>{channel.name}{channel.favorite && <Icon className={styles.guideFavorite} name="i-star-f" size={13}/>}</b>
      <small>{channel.number}{list && channel.group && <span>{channel.group}</span>}</small>
    </span>
    {children}
  </div>;
}


export const channelKey = (channel: GuideChannelRow) => JSON.stringify([channel.kind, channel.sourceId ?? '', channel.id]);

/** Format a ruler tick in the guide's timezone: local midnight ticks display the new day cleanly. */
export function guideTickLabel(tick: RulerTick, timezone: string): string {
  if (tick.kind === 'day') return new Intl.DateTimeFormat(defaultI18n.region.formatLocale, {weekday: 'short', month: 'short', day: 'numeric', timeZone: timezone}).format(tick.atMs);
  return new Intl.DateTimeFormat(defaultI18n.region.formatLocale, {hour: 'numeric', minute: '2-digit', timeZone: timezone}).format(tick.atMs);
}

function cellBounds(cell: HTMLElement): Readonly<{left: number; right: number; mid: number}> {
  const styleLeft = Number.parseFloat(cell.style.left);
  const styleWidth = Number.parseFloat(cell.style.width);
  if (Number.isFinite(styleLeft) && Number.isFinite(styleWidth)) {
    return {left: styleLeft, right: styleLeft + styleWidth, mid: styleLeft + styleWidth / 2};
  }
  const rect = cell.getBoundingClientRect();
  return {left: rect.left, right: rect.right, mid: rect.left + rect.width / 2};
}

/** Find the closest programme cell in the next non-empty channel row above or below `cell`. */
export function findVerticalGuideCell(cell: HTMLElement, direction: -1 | 1): HTMLButtonElement | undefined {
  const currentRow = cell.closest<HTMLElement>('[data-guide-row]');
  const container = currentRow?.parentElement;
  if (!currentRow || !container) return undefined;
  const rowElements = [...container.querySelectorAll<HTMLElement>('[data-guide-row]')];
  const rowIndex = rowElements.indexOf(currentRow);
  if (rowIndex < 0) return undefined;
  const targetMid = cellBounds(cell).mid;
  for (let index = rowIndex + direction; index >= 0 && index < rowElements.length; index += direction) {
    const candidates = [...rowElements[index]!.querySelectorAll<HTMLButtonElement>('button[data-kind]')];
    if (!candidates.length) continue;
    let best = candidates[0]!;
    let bestEdgeDistance = Number.POSITIVE_INFINITY;
    let bestMidDistance = Number.POSITIVE_INFINITY;
    for (const candidate of candidates) {
      const bounds = cellBounds(candidate);
      const edgeDistance = targetMid < bounds.left ? bounds.left - targetMid : targetMid > bounds.right ? targetMid - bounds.right : 0;
      const midDistance = Math.abs(bounds.mid - targetMid);
      if (edgeDistance < bestEdgeDistance || (edgeDistance === bestEdgeDistance && midDistance < bestMidDistance)) {
        best = candidate;
        bestEdgeDistance = edgeDistance;
        bestMidDistance = midDistance;
      }
    }
    return best;
  }
  return undefined;
}

export type GuideKeyEvent = Readonly<{
  key: string;
  ctrlKey: boolean;
  metaKey: boolean;
  altKey: boolean;
  target: EventTarget | null;
  preventDefault(): void;
}>;

export type GuideKeyHandlers = Readonly<{
  rows: readonly GuideRowData[];
  onPlay?: (channel: GuideChannelRow, witness?: RowWitness) => void;
  onRecord?: (channel: GuideChannelRow, programme?: GuideProgram, witness?: RowWitness) => void;
  onSelect: (channel: GuideChannelRow, programme?: GuideProgram, witness?: RowWitness) => void;
  onNow: () => void;
}>;

/** Keyboard navigation and single-key shortcuts for the guide grid. Never intercepts browser/OS modifier combos. */
export function handleGuideGridKeyDown(event: GuideKeyEvent, handlers: GuideKeyHandlers): void {
  if (event.ctrlKey || event.metaKey || event.altKey) return;
  const target = event.target as HTMLElement | null;
  const cell = target?.closest<HTMLButtonElement>('button[data-kind]');
  if (!cell) return;
  if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') {
    event.preventDefault();
    const cells = [...(cell.parentElement?.querySelectorAll<HTMLButtonElement>('button[data-kind]') ?? [])];
    const index = cells.indexOf(cell);
    if (index >= 0) cells[Math.max(0, Math.min(cells.length - 1, index + (event.key === 'ArrowLeft' ? -1 : 1)))]?.focus();
    return;
  }
  if (event.key === 'ArrowUp' || event.key === 'ArrowDown') {
    event.preventDefault();
    findVerticalGuideCell(cell, event.key === 'ArrowUp' ? -1 : 1)?.focus();
    return;
  }
  const rowKey = cell.closest<HTMLElement>('[data-guide-row]')?.dataset.guideRow;
  const item = rowKey ? handlers.rows.find(entry => entry.channel && channelKey(entry.channel) === rowKey) : undefined;
  if ((event.key === 'w' || event.key === 'W') && item?.channel && handlers.onPlay) {
    event.preventDefault();
    handlers.onPlay(item.channel, item.witness);
    return;
  }
  if ((event.key === 'r' || event.key === 'R') && item?.channel) {
    const programId = cell.dataset.programId;
    const rawStart = cell.dataset.programStart;
    const programStart = rawStart !== undefined ? Number(rawStart) : undefined;
    const programme = programId ? item.programs.find(entry => entry.id === programId && (programStart === undefined || entry.start === programStart)) : undefined;
    if (handlers.onRecord) {
      event.preventDefault();
      handlers.onRecord(item.channel, programme, item.witness);
      return;
    }
    if (programme) {
      event.preventDefault();
      handlers.onSelect(item.channel, programme, item.witness);
      return;
    }
  }
  if (event.key === 'n' || event.key === 'N') {
    event.preventDefault();
    handlers.onNow();
  }
}

/** The row's channel menu. Desktop and tablet anchor it to the row's More button, shown on
 * hover and focus; the phone has no button and opens the same menu as a sheet. */
function ChannelMenu({channel, items, open, onOpenChange, button, className}: Readonly<{
  channel: GuideChannelRow; items: readonly MenuEntry[]; open: boolean; onOpenChange(open: boolean): void; button: boolean; className?: string;
}>) {
  // With no ⋯ (a phone row) the menu stands on the channel cell itself. Without an anchor, a window narrowed from
  // desktop to phone width threw: the row dropped its button a render before the menu turned into a sheet.
  const anchor = useRef<HTMLSpanElement>(null);
  return <>{!button && <span ref={anchor} className={styles.guideChannelAnchor} aria-hidden="true"/>}<Menu open={open} onOpenChange={value => onOpenChange(value)} title={channel.name} items={items}
    {...(button ? {} : {anchorRef: anchor})}
    trigger={button ? <Button className={className} variant="ghost" size="small" iconOnly icon="i-more" aria-haspopup="menu" data-channel-menu=""
      aria-label={t('channels.moreFor', {channel: channel.name})}/> : undefined}/></>;
}

/** A guide or list row. A long press, a right-click or the keyboard's menu key on the row opens
 * its channel menu; the release that ends a hold does not also press what is under it. */
function GuideRowFrame({channel, onMenu, className, style, children}: Readonly<{
  channel?: GuideChannelRow; onMenu?: () => void; className: string; style: CSSProperties; children: ReactNode;
}>) {
  const gestures = useChannelMenuGestures(() => onMenu?.());
  // Spec §10: an unavailable channel is dimmed and says why on hover, focus and in its label (LT-4).
  const reason = channel && !channel.tuneAvailable ? channelUnavailableMessage(channel.tuneUnavailableReason ?? '') : undefined;
  return <div className={className} style={style} data-guide-row={channel ? channelKey(channel) : undefined} data-hidden={channel && legacyChannel(channel)?.serverHidden || undefined}
    data-unavailable={channel && !channel.tuneAvailable || undefined} title={reason ? t(reason) : undefined}
    role={channel ? 'group' : undefined} aria-label={channel ? [channel.number, channel.name, reason ? t(reason) : undefined].filter(Boolean).join(', ') : undefined}
    {...(channel && onMenu ? gestures : {})}>
    {children}
  </div>;
}


/** One drawn row of the list view: the airing on now, or the next/later airing. */
function ProgrammeSummary({programme, channel, now, timezone, current = false, onSelect}: Readonly<{
  programme: GuideProgram; channel: GuideChannelRow; now: number; timezone: string; current?: boolean; onSelect: () => void;
}>) {
  const parts = programmeFactParts({startMs: programme.start, endMs: programme.end,
    episode: programme.episode ? {mode: 'seasonal', season: programme.episode.season, episode: programme.episode.number} : undefined,
    subtitle: programme.subtitle, flags: programme.flags, recording: programme.recording?.state === 'recording'},
  {t, locale: defaultI18n.region.formatLocale, timeZone: timezone}, current ? now : undefined);
  const facts = guideCellFacts(parts), remaining = parts.find(part => part.id === 'remaining');
  const detail = current ? facts.detail : facts.detail.filter(part => part.id !== 'time-range');
  const progress = current && programme.end > programme.start ? Math.min(1, Math.max(0, (now - programme.start) / (programme.end - programme.start))) : undefined;
  return <>
    {!current && <span className={styles.guideListAt}>{defaultI18n.time(programme.start)}</span>}
    <b><button type="button" className={styles.guideListTitle} onClick={onSelect}
      aria-label={[channel.number, channel.name, programme.title, ...parts.map(part => part.text), programme.recording?.state === 'scheduled' ? t('guide.recordingScheduled') : undefined].filter(Boolean).join(', ')}>
      {programme.recording?.state === 'recording' && <i className={styles.guideRecordDot} data-pulse title={t('next.kit.recording')}/>}
      <span className={styles.guideCellName}>{programme.title}</span>
    </button></b>
    <small>
      {facts.flags.map(part => <span key={part.id} className={styles.guideFlag} data-flag={FLAG_KINDS[part.id]}>{part.text}</span>)}
      {detail.map(part => <span key={part.id}>{part.text}</span>)}
    </small>
    {current && remaining && <span className={styles.guideListProgress}>
      <span className={styles.guideListTrack} aria-hidden="true"><i style={{width: `${((progress ?? 0) * 100).toFixed(3)}%`}}/></span>
      <span>{remaining.text}</span>
    </span>}
  </>;
}

/** The portrait List frame keeps status above the title and the next airing below progress. */
function PhoneListProgramme({channel, current, next, now, timezone, onSelect}: Readonly<{
  channel: GuideChannelRow; current?: GuideProgram; next?: GuideProgram; now: number; timezone: string;
  onSelect(programme: GuideProgram): void;
}>) {
  const parts = current ? programmeFactParts({startMs: current.start, endMs: current.end,
    episode: current.episode ? {mode: 'seasonal', season: current.episode.season, episode: current.episode.number} : undefined,
    subtitle: current.subtitle, flags: current.flags, recording: current.recording?.state === 'recording'},
  {t, locale: defaultI18n.region.formatLocale, timeZone: timezone}, now) : [];
  const facts = guideCellFacts(parts), remaining = parts.find(part => part.id === 'remaining');
  const detail = facts.detail.filter(part => part.id !== 'time-range'), selectable = current ?? next;
  const progress = current && current.end > current.start ? Math.min(1, Math.max(0, (now - current.start) / (current.end - current.start))) : undefined;
  return <div className={styles.phoneListProgramme}>
    <div className={styles.phoneListStatus}>
      {selectable ? <button type="button" className={styles.guideListChannelName} onClick={() => onSelect(selectable)}>{channel.name}</button>
        : <span className={styles.guideListChannelName}>{channel.name}</span>}
      {channel.favorite && <Icon className={styles.guideFavorite} name="i-star-f" size={12}/>}
      {current?.recording?.state === 'recording' && <span className={styles.guideRecording}>{t('next.kit.recording')}</span>}
      {facts.flags.map(part => <span key={part.id} className={styles.guideFlag} data-flag={FLAG_KINDS[part.id]}>{part.text}</span>)}
      {remaining && <span className={styles.phoneListRemaining}>{remaining.text}</span>}
    </div>
    {current ? <button type="button" className={styles.phoneListTitle} onClick={() => onSelect(current)}>{current.title}</button>
      : <b className={styles.phoneListTitle}>{t(channel.guide === 'none' ? 'guide.noGuide' : 'guide.noInformation')}</b>}
    {!!detail.length && <div className={styles.phoneListFacts}>{detail.map(part => <span key={part.id}>{part.text}</span>)}</div>}
    {channel.guide === 'none' && !current && <div className={styles.phoneListFacts}>{t('guide.noGuideBody')}</div>}
    {progress !== undefined && <span className={styles.phoneListTrack} aria-hidden="true"><i style={{width: `${progress * 100}%`}}/></span>}
    {next && <button type="button" className={styles.phoneListNext} onClick={() => onSelect(next)}>{t('guide.next', {title: next.title, time: defaultI18n.time(next.start)})}</button>}
  </div>;
}

export function GuideScreen({title, channelLogo, switcher, summary, conflicts, onTabChange, chips, onNow, problem, notice, empty, day, sort, view, time, onViewChange,
  span, rows, total, selected, onSelect, onPlay, onRecord, channelMenu, onVisibleWindow, onRetry}: GuideScreenProps) {
  const body = useRef<HTMLDivElement>(null);
  const [menuFor, setMenuFor] = useState<string>();
  const swipe = useGuideSwipe(time.onEarlier, time.onLater);
  const {start, end, now, scale, column, rowHeight, timezone} = span;
  const phone = column <= 60;
  const width = useWindowWidth(), height = useWindowHeight();
  const landscape = width >= 600 && height < 600 && width > height, compactHeader = phone || landscape;
  const bodyRowHeight = view === 'list' ? guideListRowHeight(phone) : rowHeight;
  const ticks = useMemo(() => rulerTicks(start, end, scale, timezone), [start, end, scale, timezone]);
  const nowX = now >= start && now < end ? (now - start) * scale : undefined;

  // The guide body scrolls with the page, so the loaded window follows the page scroll.
  useEffect(() => {
    if (!onVisibleWindow) return;
    const update = () => {
      const element = body.current;
      if (!element) return;
      const {first, last} = guideVisibleRows(element.getBoundingClientRect().top, globalThis.innerHeight, bodyRowHeight, total);
      onVisibleWindow(first, last);
    };
    update();
    globalThis.addEventListener('scroll', update, {passive: true});
    globalThis.addEventListener('resize', update);
    return () => {globalThis.removeEventListener('scroll', update);globalThis.removeEventListener('resize', update);};
  }, [onVisibleWindow, bodyRowHeight, total, view]);

  const onKeyDown = useCallback((event: KeyboardEvent<HTMLDivElement>) => {
    handleGuideGridKeyDown(event, {rows, onPlay, onRecord, onSelect, onNow: time.onNow});
  }, [rows, onPlay, onRecord, onSelect, time.onNow]);

  // desktop-live live-guide, phone-guide g1, final3-phone-07: the guide on the Live TV frame. The frame draws the head (the
  // library header from 600 with Guide | Recordings under it; at phone width the title with List | Guide and Recordings), the
  // channel groups, On now, the time row and the keyboard line; the guide below is the frame's one flexible region.
  const facts = summary && <span className={styles.guideSource}>
    <strong>{summary.name}</strong>
    {summary.channels !== undefined && <span>{t('next.kit.channels', {count: summary.channels})}</span>}
    {summary.through && <span>{summary.through}</span>}
  </span>;
  const guideTabs = <Tabs value="guide" aria-label={t('next.home.shell.live')}
    onValueChange={value => {if (value === 'recordings') onTabChange?.(value);}}
    items={[{value: 'guide', label: t('channels.tabGuide')},
      {value: 'recordings', label: <>{t('recordings.title')}{conflicts !== undefined && conflicts > 0 && <span className={styles.guideTabNote}>{t('next.live.conflicts', {count: conflicts})}</span>}</>}]}/>;
  const phoneActions = <>
    <Segmented variant="view" aria-label={t('channels.view')} value={view} onValueChange={value => {if (value === 'guide' || value === 'list') onViewChange(value);}}
      items={[{value: 'list', label: t('channels.viewList'), icon: 'i-list'}, {value: 'guide', label: t('channels.viewGuide'), icon: 'i-timeline'}]}/>
    {onTabChange && <Button variant="text" iconOnly icon="i-rec-ring" aria-label={t('recordings.title')} onClick={() => onTabChange('recordings')}/>}
  </>;
  const source = switcher && <PickChip label={t('channels.source')} pill={phone} value={switcher.value} options={switcher.options} onChange={switcher.onChange}/>;
  const sortChip = <PickChip label={t('channels.sort')} prefix={t('channels.sort')} quiet={phone} value={sort.value} options={sort.options} onChange={sort.onChange}/>;
  const toolbar = landscape ? sortChip : view === 'list'
    ? <><p className={styles.guideListCount}><span>{t('next.kit.channels', {count: total})}</span><span>{t('guide.onNowAt', {time: defaultI18n.time(now)})}</span></p><span className={styles.guideToolbarGap}/>{sortChip}</>
    : <>
      <PickChip label={day.label} icon="i-cal" quiet={phone} value={day.value} options={day.options} onChange={day.onChange}/>
      {!compactHeader && <>
        <Button size="small" icon="i-left" aria-label={time.earlier} onClick={time.onEarlier}>{t('guide.twoHours')}</Button>
        <Button size="small" onClick={time.onNow}>{time.now}</Button>
        <Button size="small" aria-label={time.later} onClick={time.onLater}>{t('guide.twoHours')}<Icon name="i-right" size={16}/></Button>
      </>}
      <span className={styles.guideToolbarGap}/>
      {sortChip}
      {!compactHeader && <Segmented variant="view" aria-label={t('channels.view')} value={view} onValueChange={value => {if (value === 'guide' || value === 'list') onViewChange(value);}}
        items={[{value: 'guide', label: t('channels.viewGrid'), icon: 'i-timeline'},
          {value: 'list', label: t('channels.viewList'), icon: 'i-list'}]}/>}
    </>;
  return <LiveFrame title={title} switcher={source} facts={facts} actions={compactHeader && phoneActions} tabs={!compactHeader && guideTabs}
    chips={<>{landscape && source}{chips.map(chip => <Chip key={chip.id} variant="search" selected={chip.selected} count={phone ? undefined : chip.count} icon={chip.favorites && !phone ? 'i-star' : undefined} onClick={chip.onSelect}>{chip.label}</Chip>)}</>}
    lead={view === 'guide' ? onNow : undefined} toolbar={toolbar} hints={view === 'guide' && !compactHeader ? t('guide.keyboardHint') : undefined}>
    <section className={styles.guideScreen} data-guide-screen data-phone-landscape={landscape || undefined}>
    {notice && <div className={styles.guideProblem}>{notice}</div>}
    {problem && <div className={styles.guideProblem}>{problem}</div>}
    {empty ?? (view === 'guide'
      ? <div {...swipe} className={styles.guide} data-guide-grid role="group" aria-label={t('guide.label')} style={{'--guide-column': `${column}px`, '--guide-row': `${rowHeight}px`} as CSSProperties}>
        <div className={styles.guideRuler}>
          <div className={styles.guideCorner}>{landscape ? <PickChip label={day.label} value={day.value} options={day.options} onChange={day.onChange}/> : phone ? <button type="button" className={styles.guideCornerNow} onClick={time.onNow}>{time.now}</button> : t('guide.channelColumn')}</div>
          <div className={styles.guideLane} data-now-outside={nowX === undefined || undefined}>
            {ticks.map(tick => <span key={tick.atMs} className={styles.guideTick} data-kind={tick.kind} style={{left: tick.x}}>{guideTickLabel(tick, timezone)}</span>)}
            {nowX !== undefined && <span className={styles.guideNowPill} style={{left: nowX}}>{defaultI18n.time(now)}</span>}
            {nowX === undefined && <button type="button" className={styles.guideNowJump} data-edge={now < start ? 'start' : 'end'} onClick={time.onNow}>
              <Icon name={now < start ? 'i-left' : 'i-right'} size={16}/>{time.now}
            </button>}
          </div>
        </div>
        <div className={styles.guideBody} ref={body}>
          {nowX !== undefined && <div className={styles.guidePastShade} style={{width: nowX}} aria-hidden="true"/>}
          <div className={styles.guideRows} style={{height: Math.max(0, total) * rowHeight}} onKeyDown={onKeyDown}>
            {rows.map(row => <GuideRowFrame className={styles.guideRow} key={row.channel ? channelKey(row.channel) : row.index} style={{top: row.index * rowHeight}} channel={row.channel}
              onMenu={row.channel && channelMenu ? () => setMenuFor(channelKey(row.channel!)) : undefined}>
              <GuideChannelCell channel={row.channel} list={false} phone={phone} landscape={landscape} logo={channelLogo}>
                {row.channel && channelMenu && <ChannelMenu channel={row.channel} items={channelMenu(row.channel, row.witness)} button={!phone} className={styles.guideChannelMore}
                  open={menuFor === channelKey(row.channel)} onOpenChange={open => setMenuFor(open ? channelKey(row.channel!) : undefined)}/>}
              </GuideChannelCell>
              <div className={styles.guideLane}>
                {!row.channel || !row.complete
                  ? row.failed ? <StateView title={t('guide.tileFailed')} primary={{id: 'retry', label: t('next.states.tryAgain'), onPress: () => onRetry?.()}}/>
                    : <Skeleton width="100%" height={rowHeight - 8}/>
                  : <>
                    {nowX !== undefined && <i className={styles.guideLaneNow} style={{left: nowX}} aria-hidden="true"/>}
                    {layoutRow(row.programs, {viewStart: start, viewEnd: end, pxPerMs: scale, now, gapPx: 2, ...(compactHeader ? {tiers: {full: 50, title2: 50, title: 24}} : {})})
                      .map(cell => cell.kind === 'gap'
                        ? <div key={`gap:${cell.start}`} className={styles.guideCell} data-kind="gap" data-tier={cell.tier} data-wide-gap={row.channel!.guide === 'none' || undefined}
                            role="note" aria-label={row.channel!.guide === 'none' ? t('guide.noGuide') : t('guide.noInformation')}
                            style={{left: cell.x + (row.channel!.guide === 'none' ? 0 : GUIDE_CELL_INSET), width: Math.max(0, cell.width - (row.channel!.guide === 'none' ? 0 : GUIDE_CELL_INSET))}}>
                            {cell.tier === 'full' && <><span className={styles.guideCellTitle}>{row.channel!.guide === 'none' ? t('guide.noGuide') : t('guide.noInformation')}</span>
                              {row.channel!.guide === 'none' && <span className={styles.guideCellDetail}>{t('guide.noGuideBody')}</span>}</>}
                          </div>
                        : <CellButton key={`cell:${cell.start}`} cell={cell} channel={row.channel!} witness={row.witness} now={now} timezone={timezone}
                            selected={(selected?.channelKey !== undefined ? selected.channelKey === channelKey(row.channel!) : selected?.channelId === row.channel!.id) && (selected.programmeId === undefined || selected.programmeId === cell.program?.id)} onSelect={onSelect}/>)}
                  </>}
              </div>
            </GuideRowFrame>)}
          </div>
          {nowX !== undefined && <div className={styles.guideNowLine} style={{left: nowX}} aria-hidden="true"/>}
        </div>
      </div>
      : <div className={styles.guideListState} data-guide-list>
        <div className={styles.guideListHead} aria-hidden="true">
          <span>{t('guide.channelColumn')}</span><span>{t('guide.onNow')}</span><span>{t('guide.nextColumn')}</span><span>{t('guide.laterColumn')}</span><span/>
        </div>
        <div className={styles.guideListRows} ref={body} style={{height: Math.max(0, total) * bodyRowHeight}}>
        {rows.map(row => <GuideRowFrame className={styles.guideListRow} key={row.channel ? channelKey(row.channel) : row.index} style={{top: row.index * bodyRowHeight, height: bodyRowHeight}} channel={row.channel}
          onMenu={row.channel && channelMenu ? () => setMenuFor(channelKey(row.channel!)) : undefined}>
          <GuideChannelCell channel={row.channel} list phone={phone} logo={channelLogo}>
            {phone && row.channel && channelMenu && <ChannelMenu channel={row.channel} items={channelMenu(row.channel, row.witness)} button={false}
              open={menuFor === channelKey(row.channel)} onOpenChange={open => setMenuFor(open ? channelKey(row.channel!) : undefined)}/>}
          </GuideChannelCell>
          {!row.channel || !row.complete
            ? <div className={styles.guideListNow}>{row.failed ? <StateView title={t('guide.tileFailed')} primary={{id: 'retry', label: t('next.states.tryAgain'), onPress: () => onRetry?.()}}/> : <Skeleton width="100%" height={rowHeight - 20}/>}</div>
            : (() => {
              const [current, next, later] = liveListPrograms(row.programs, now);
              if (phone) return <PhoneListProgramme channel={row.channel!} current={current} next={next} now={now} timezone={timezone} onSelect={programme => onSelect(row.channel!, programme, row.witness)}/>;
              return <>
                <div className={styles.guideListNow}>{current
                  ? <ProgrammeSummary programme={current} channel={row.channel!} now={now} timezone={timezone} current onSelect={() => onSelect(row.channel!, current, row.witness)}/>
                  : <><b><span className={styles.guideListMuted}>{row.channel?.guide === 'none' ? t('guide.noGuide') : t('guide.noInformation')}</span></b>
                    {row.channel?.guide === 'none'
                      ? <small>{t('guide.noGuideBody')}</small>
                      : liveListPrograms(row.programs, now)[1] ? <small>{t('guide.until', {time: defaultI18n.time(liveListPrograms(row.programs, now)[1]!.start)})}</small> : null}</>}</div>
                <div className={styles.guideListNext}>{next ? <ProgrammeSummary programme={next} channel={row.channel!} now={now} timezone={timezone} onSelect={() => onSelect(row.channel!, next, row.witness)}/> : null}</div>
                <div className={`${styles.guideListNext} ${styles.guideListLater}`}>{later ? <ProgrammeSummary programme={later} channel={row.channel!} now={now} timezone={timezone} onSelect={() => onSelect(row.channel!, later, row.witness)}/> : null}</div>
                <div className={styles.guideListActions} data-menu-open={menuFor === channelKey(row.channel!) || undefined}>
                  {row.channel?.tuneAvailable && onPlay && <Button size="compact" icon="i-play" onClick={() => onPlay(row.channel!, row.witness)}>{t('channels.watch')}</Button>}
                  {channelMenu && <ChannelMenu channel={row.channel!} items={channelMenu(row.channel!, row.witness)} button
                    open={menuFor === channelKey(row.channel!)} onOpenChange={open => setMenuFor(open ? channelKey(row.channel!) : undefined)}/>}
                </div>
              </>;
            })()}
        </GuideRowFrame>)}</div>
      </div>)}
  </section></LiveFrame>;
}
