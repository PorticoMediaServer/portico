import {useCallback, useEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import {layoutRow, legacyChannel, type GuideCell, type GuideChannelRow, type GuideProgram} from '@core/guide/index.ts';
import {rulerTicks} from '@core/guide/time.ts';
import {programmeFactParts, type ChannelMarkModel, type FactPart} from '@portico/client-core/kit-model';
import {ChannelMark} from '../../kit/artwork';
import {Button, Chip, Segmented, Select, Tabs} from '../../kit/controls';
import type {SelectOption} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import {Menu, type MenuEntry} from '../../kit/overlays';
import {Skeleton, StateView} from '../../kit/states';
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
  switcher?: Readonly<{value: string; options: readonly SelectOption[]; onChange: (value: string) => void}>;
  summary?: Readonly<{name: string; channels?: number; through?: string}>;
  conflicts?: number;
  onTabChange?(value: 'recordings'): void;
  chips: readonly Readonly<{id: string; label: string; count?: number; favorites?: boolean; selected: boolean; onSelect: () => void}>[];
  onNow?: ReactNode;
  /** A page-level problem (channel page failed) shown above the guide. */
  problem?: ReactNode;
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
  selected?: Readonly<{channelId: string; programmeId?: string}>;
  onSelect(channel: GuideChannelRow, programme?: GuideProgram, witness?: RowWitness): void;
  onPlay?(channel: GuideChannelRow, witness?: RowWitness): void;
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
  return <button type="button" className={styles.guideCell} data-kind="program" data-state={cell.state} data-tier={cell.tier} data-selected={selected || undefined}
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

function GuideChannelCell({channel, list, phone, landscape = false, children}: Readonly<{channel?: GuideChannelRow; list: boolean; phone: boolean; landscape?: boolean; children?: ReactNode}>) {
  if (!channel) return <div className={styles.guideChannel}><Skeleton width={phone ? 36 : 180} height={34}/></div>;
  const mark: ChannelMarkModel | undefined = channel.mark ? {sourceId: channel.sourceId ?? '', id: channel.id, text: channel.mark.text, seed: channel.mark.seed} : undefined;
  return <div className={list ? styles.guideListChannel : styles.guideChannel}>
    {mark ? <ChannelMark className={styles.guideChannelMark} mark={mark} size={list && phone ? 46 : landscape ? 32 : 40}/> : <span className={`${styles.guideChannelMark} ${styles.guideChannelMarkEmpty}`} aria-hidden="true"/>}
    <span className={styles.guideChannelText}>
      <b>{channel.name}{channel.favorite && <Icon className={styles.guideFavorite} name="i-star-f" size={13}/>}</b>
      <small>{channel.number}{list && channel.group && <span>{channel.group}</span>}</small>
    </span>
    {children}
  </div>;
}


const channelKey = (channel: GuideChannelRow) => JSON.stringify([channel.kind, channel.sourceId ?? '', channel.id]);

/** The row's channel menu. Desktop and tablet anchor it to the row's More button, shown on
 * hover and focus; the phone has no button and opens the same menu as a sheet. */
function ChannelMenu({channel, items, open, onOpenChange, button, className}: Readonly<{
  channel: GuideChannelRow; items: readonly MenuEntry[]; open: boolean; onOpenChange(open: boolean): void; button: boolean; className?: string;
}>) {
  return <Menu open={open} onOpenChange={value => onOpenChange(value)} title={channel.name} items={items}
    trigger={button ? <Button className={className} variant="ghost" size="small" iconOnly icon="i-more" aria-haspopup="menu" data-channel-menu=""
      aria-label={t('channels.moreFor', {channel: channel.name})}/> : undefined}/>;
}

/** A guide or list row. A long press, a right-click or the keyboard's menu key on the row opens
 * its channel menu; the release that ends a hold does not also press what is under it. */
function GuideRowFrame({channel, onMenu, className, style, children}: Readonly<{
  channel?: GuideChannelRow; onMenu?: () => void; className: string; style: CSSProperties; children: ReactNode;
}>) {
  const gestures = useChannelMenuGestures(() => onMenu?.());
  return <div className={className} style={style} data-guide-row={channel?.id} data-hidden={channel && legacyChannel(channel)?.serverHidden || undefined}
    role={channel ? 'group' : undefined} aria-label={channel ? [channel.number, channel.name].filter(Boolean).join(', ') : undefined}
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

export function GuideScreen({title, switcher, summary, conflicts, onTabChange, chips, onNow, problem, empty, day, sort, view, time, onViewChange,
  span, rows, total, selected, onSelect, onPlay, channelMenu, onVisibleWindow, onRetry}: GuideScreenProps) {
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

  const move = useCallback((from: EventTarget | null, offset: number) => {
    const cells = [...(from as HTMLElement | null)?.parentElement?.querySelectorAll<HTMLButtonElement>('button') ?? []];
    const index = cells.indexOf(from as HTMLButtonElement);
    cells[Math.max(0, Math.min(cells.length - 1, index + offset))]?.focus();
  }, []);
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const cell = (event.target as HTMLElement).closest<HTMLButtonElement>('button[data-kind]');
    if (!cell) return;
    if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') {event.preventDefault();move(cell, event.key === 'ArrowLeft' ? -1 : 1);return;}
    const item = rows.find(item => item.channel?.id === cell.closest<HTMLElement>('[data-guide-row]')?.dataset.guideRow);
    if ((event.key === 'w' || event.key === 'W') && item?.channel && onPlay) {event.preventDefault();onPlay(item.channel, item.witness);return;}
    if (event.key === 'n' || event.key === 'N') {event.preventDefault();time.onNow();}
  };

  return <section className={styles.guideScreen} data-guide-screen data-phone-landscape={landscape || undefined}>
    <div className={styles.guideChrome}>
    <header className={styles.guideHeader} data-guide-header>
      <h1>{title}</h1>
      {compactHeader && <Segmented className={styles.guidePhoneView} variant="view" aria-label={t('channels.view')} value={view} onValueChange={value => {if (value === 'guide' || value === 'list') onViewChange(value);}}
        items={[{value: 'list', label: t('channels.viewList'), icon: 'i-list'}, {value: 'guide', label: t('channels.viewGuide'), icon: 'i-timeline'}]}/>}
      {compactHeader && onTabChange && <Button className={styles.guideRecordings} variant="icon" iconOnly icon="i-rec" aria-label={t('recordings.title')} onClick={() => onTabChange('recordings')}/>}
      {!compactHeader && switcher && <Select className={styles.guideSwitcher} aria-label={t('channels.source')} value={switcher.value} options={switcher.options} onValueChange={switcher.onChange}/>}
      {summary && <p className={styles.guideSource}>
        <strong>{summary.name}</strong>
        {summary.channels !== undefined && <span>{t('next.kit.channels', {count: summary.channels})}</span>}
        {summary.through && <span>{summary.through}</span>}
      </p>}
    </header>
    <Tabs className={styles.guideTabs} data-guide-tabs listClassName={styles.guideTabList} value="guide" aria-label={t('next.home.shell.live')}
      onValueChange={value => {if (value === 'recordings') onTabChange?.(value);}}
      items={[{value: 'guide', label: t('channels.title')},
        {value: 'recordings', label: <>{t('recordings.title')}{conflicts !== undefined && <span className={styles.guideTabNote}>{t('next.live.conflicts', {count: conflicts})}</span>}</>}]}/>
    <div className={styles.guideChips} data-guide-chips role="group" aria-label={t('channels.filters')}>
      {compactHeader && switcher && <Select className={styles.guideSourceChip} aria-label={t('channels.source')} value={switcher.value} options={switcher.options} onValueChange={switcher.onChange}/>}
      {chips.map(chip => <Chip className={styles.guideChip} key={chip.id} selected={chip.selected} count={chip.count} icon={chip.favorites ? 'i-star' : undefined} onClick={chip.onSelect}>{chip.label}</Chip>)}
    </div>
    {view === 'guide' && onNow}
    <div className={styles.guideTime} data-guide-time>
      {view === 'list'
        ? <p className={styles.guideListCount}><span>{t('next.kit.channels', {count: total})}</span><span>{t('guide.onNowAt', {time: defaultI18n.time(now)})}</span></p>
        : <>
          <Select className={styles.guideDay} aria-label={day.label} value={day.value} options={day.options} onValueChange={day.onChange}/>
          <div className={styles.guideStepper} role="group" aria-label={t('guide.twoHours')}>
            <button type="button" className={styles.guideStep} aria-label={time.earlier} onClick={time.onEarlier}><Icon name="i-left" size={18}/>{t('guide.twoHours')}</button>
            <button type="button" className={`${styles.guideStep} ${styles.guideNow}`} onClick={time.onNow}>{time.now}</button>
            <button type="button" className={styles.guideStep} aria-label={time.later} onClick={time.onLater}>{t('guide.twoHours')}<Icon name="i-right" size={18}/></button>
          </div>
        </>}
      <div className={styles.guideRight}>
        <div className={styles.guideSortField}><span className={styles.guideSortPrefix}>{t('channels.sort')}</span>
          <Select className={styles.guideSort} aria-label={t('channels.sort')} value={sort.value} options={sort.options} onValueChange={sort.onChange}/>
        </div>
        {!compactHeader && <Segmented className={styles.guideView} variant="view" aria-label={t('channels.view')} value={view} onValueChange={value => {if (value === 'guide' || value === 'list') onViewChange(value);}}
          items={[{value: 'list', label: t('channels.viewList'), icon: 'i-list'},
            {value: 'guide', label: t('channels.viewGuide'), icon: 'i-timeline'}]}/>}
      </div>
    </div>
    </div>
    {problem && <div className={styles.guideProblem}>{problem}</div>}
    {empty ?? (view === 'guide'
      ? <div {...swipe} className={styles.guide} data-guide-grid role="group" aria-label={t('guide.label')} style={{'--guide-column': `${column}px`, '--guide-row': `${rowHeight}px`} as CSSProperties}>
        <div className={styles.guideRuler}>
          <div className={styles.guideCorner}>{landscape ? <Select className={styles.guideDay} aria-label={day.label} value={day.value} options={day.options} onValueChange={day.onChange}/> : phone ? <button type="button" className={styles.guideCornerNow} onClick={time.onNow}>{time.now}</button> : t('guide.channelColumn')}</div>
          <div className={styles.guideLane} data-now-outside={nowX === undefined || undefined}>
            {ticks.map(tick => <span key={tick.atMs} className={styles.guideTick} data-kind={tick.kind} style={{left: tick.x}}>{defaultI18n.time(tick.atMs)}</span>)}
            {nowX !== undefined && <span className={styles.guideNowPill} style={{left: nowX}}>{defaultI18n.time(now)}</span>}
            {nowX === undefined && <button type="button" className={styles.guideNowJump} data-edge={now < start ? 'start' : 'end'} onClick={time.onNow}>
              <Icon name={now < start ? 'i-left' : 'i-right'} size={16}/>{time.now}
            </button>}
          </div>
        </div>
        <div className={styles.guideBody} ref={body}>
          {nowX !== undefined && <div className={styles.guidePastShade} style={{width: nowX}} aria-hidden="true"/>}
          <div className={styles.guideRows} style={{height: Math.max(0, total) * rowHeight}} onKeyDown={onKeyDown}>
            {rows.map(row => <GuideRowFrame className={styles.guideRow} key={row.channel?.id ?? row.index} style={{top: row.index * rowHeight}} channel={row.channel}
              onMenu={row.channel && channelMenu ? () => setMenuFor(channelKey(row.channel!)) : undefined}>
              <GuideChannelCell channel={row.channel} list={false} phone={phone} landscape={landscape}>
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
                            selected={selected?.channelId === row.channel!.id && (selected.programmeId === undefined || selected.programmeId === cell.program?.id)} onSelect={onSelect}/>)}
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
        {rows.map(row => <GuideRowFrame className={styles.guideListRow} key={row.channel?.id ?? row.index} style={{top: row.index * bodyRowHeight, height: bodyRowHeight}} channel={row.channel}
          onMenu={row.channel && channelMenu ? () => setMenuFor(channelKey(row.channel!)) : undefined}>
          <GuideChannelCell channel={row.channel} list phone={phone}>
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
    {view === 'guide' && <p className={styles.guideKbd} data-guide-kbd>{t('guide.keyboardHint')}</p>}
  </section>;
}
