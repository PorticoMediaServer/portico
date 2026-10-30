import {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore, type RefObject} from 'react';
import {defaultI18n} from '@i18n';
import type {ReadingPreferences} from '@portico/client-core';
import {createWindowedCollection, type WindowedCollection} from '@core/collections/index.ts';
import {WindowedGrid} from '../../kit/layout/WindowedGrid';
import {readerErrorCode, type ReadingController, type ReaderCrease, type ReaderErrorCode, type ReaderFit, type ReaderFixedViewport, type ReaderFormat, type ReaderLocation, type ReaderViewport, type ReaderZoom, type ReaderZoomState} from '@portico/reader-core';
import {Menu, MenuItem, MenuSeparator} from '../../kit/overlays';
import {Icon} from '../../kit/Icon';
import {ReaderZoomMap} from '../../kit/reader/ReaderZoomMap';
import type {ReaderChapterMark} from './ReaderChrome';
import {loadReadPagePreview, type ReadPagePreview} from './preview';
import type {ReaderMountAuthority} from './mount';
import {fixedPageWindow} from './fixed-page-window';
import styles from './fixed-page.module.css';

const {t} = defaultI18n;

type Fixed = Readonly<{controller: ReadingController; authority: ReaderMountAuthority; format: ReaderFormat;
  reportError: (code: ReaderErrorCode) => void}>;

const fitLabel = (fit: ReaderFit): string => t(fit === 'page' ? 'next.reader.fitPage' : fit === 'width' ? 'next.reader.fitWidth'
  : fit === 'height' ? 'next.reader.fitHeight' : 'next.reader.fitSpread');

/** The observed fit or scale; nothing is claimed before the engine reports one. */
export function fixedPageZoomLabel(zoom: ReaderZoomState | null): string {
  if (!zoom) return t('next.reader.fit');
  return zoom.fit ? fitLabel(zoom.fit) : defaultI18n.percent(zoom.scale);
}

/** phone-read 9: fixed-page controls share the existing chrome's visibility. */
export function FixedPageFooter({controller, authority, reportError, pages, chapters, onPages, onContents, onFit}: Fixed & Readonly<{
  pages?: string; chapters: readonly ReaderChapterMark[];
  onPages: () => void; onContents: () => void; onFit: (anchor: HTMLButtonElement) => void;
}>) {
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot);
  const progression = snapshot.location?.progression;
  const ready = snapshot.status === 'ready';
  const seek = (value: number) => {
    if (!ready || !authority.isCurrent() || controller.getSnapshot() !== snapshot || !Number.isFinite(value) || value < 0 || value > 1) return;
    void controller.navigate({kind: 'progression', value}).catch(error => {if (authority.isCurrent()) reportError(readerErrorCode(error));});
  };
  return <div className={styles.footer}>
    <div className={styles.footerLabels}><span>{snapshot.location?.chapter}</span><span>{pages}</span></div>
    {progression !== undefined && Number.isFinite(progression) && progression >= 0 && progression <= 1 && <div className={styles.footerProgress}>
      <span className={styles.footerTrack} aria-hidden="true"><i className={styles.footerFill} style={{width: `${progression * 100}%`}}/>
        {chapters.map(mark => <span key={mark.id}><i className={styles.footerMark} style={{left: `${mark.start * 100}%`}}/>
          {mark.current && mark.end !== undefined && mark.end > mark.start && <i className={styles.footerChapter} style={{left: `${mark.start * 100}%`, width: `${(mark.end - mark.start) * 100}%`}}/>}</span>)}
        <i className={styles.footerThumb} style={{left: `${progression * 100}%`}}/></span>
      <input type="range" min={0} max={1} step={.0001} value={progression} disabled={!ready} aria-label={t('next.reader.position')} aria-valuetext={pages}
        onChange={event => seek(event.currentTarget.valueAsNumber)}/>
    </div>}
    <div className={styles.footerTools}>
      <button type="button" disabled={!ready || !snapshot.metadata?.pageCount} onClick={onPages}><Icon name="i-thumbs" size={17}/>{t('next.reader.pagesStrip')}</button>
      <button type="button" disabled={!ready || !snapshot.toc.length} onClick={onContents}><Icon name="i-chapters" size={17}/>{t('next.reader.contents')}</button>
      <button type="button" disabled={!ready || !snapshot.zoom} onClick={event => onFit(event.currentTarget)}><Icon name="i-page" size={17}/>{fixedPageZoomLabel(snapshot.zoom)}<Icon name="i-down" size={14}/></button>
    </div>
  </div>;
}

/**
 * Fixed pages (PDF, CBZ) draw the file's own pages in the reader's footer. A bounded
 * visible window is rendered from the current document, so a 300-page PDF never opens
 * every page to build it, and no second archive or preview cache lives here.
 */
export function FixedPageStrip({controller, authority, reportError, format}: Fixed) {
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot);
  const count = snapshot.metadata?.pageCount, location = snapshot.location;
  const current = location && location.format !== 'epub' ? location.page : undefined;
  if (format === 'epub' || !count || count < 2 || current === undefined || snapshot.status !== 'ready') return null;
  const first = Math.max(0, Math.min(current - 5, count - 11)), last = Math.min(count, first + 11);
  return <div className={styles.strip} role="group" aria-label={t('next.reader.pagesStrip')}>
    {Array.from({length: last - first}, (_, offset) => first + offset).map(index => <PageThumb key={index} controller={controller} authority={authority} reportError={reportError}
      format={format} index={index} count={count} current={index === current} pageLabel={t('next.reader.pageLabel', {page: defaultI18n.number(index + 1)})}
      onSelect={() => {
        const location: ReaderLocation = {format: format === 'pdf' ? 'pdf' : 'cbz', page: index, progression: count === 1 ? 0 : index / (count - 1)};
        void controller.navigate({kind: 'location', location}).catch(error => {
          if (authority.isCurrent()) reportError(readerErrorCode(error));
        });
      }}/>)}
  </div>;
}

function PageThumb({controller, authority, reportError, format, index, count, current, pageLabel, large = false, signal, onSelect}: Fixed & Readonly<{
  index: number; count: number; current: boolean; pageLabel: string; large?: boolean; signal?: AbortSignal; onSelect: () => void;
}>) {
  return <button type="button" className={large ? `${styles.page} ${styles.large}` : styles.page} aria-current={current ? 'page' : undefined}
    aria-label={count > 0 ? t('next.reader.pageOf', {page: defaultI18n.number(index + 1), count: defaultI18n.number(count)}) : pageLabel}
    data-current={current || undefined} onClick={onSelect}>
    <PageImage controller={controller} authority={authority} reportError={reportError} format={format} index={index} large={large} signal={signal}/>
    <span className={styles.number} aria-hidden="true">{defaultI18n.number(index + 1)}</span>
  </button>;
}

function PageImage({controller, authority, reportError, format, index, large = false, signal}: Fixed & Readonly<{
  index: number; large?: boolean; signal?: AbortSignal;
}>) {
  const [preview, setPreview] = useState<ReadPagePreview>();
  useEffect(() => {
    const life = new AbortController();
    let loaded: ReadPagePreview | undefined, alive = true;
    void loadReadPagePreview({controller, index, width: large ? 180 : 96, authority, signal: signal ? AbortSignal.any([life.signal, signal]) : life.signal}).then(value => {
      if (!alive || signal?.aborted) {value.dispose(); return;}
      loaded = value; setPreview(value);
    }).catch(error => {if (alive && authority.isCurrent() && !life.signal.aborted && !signal?.aborted) reportError(readerErrorCode(error));});
    return () => {alive = false; life.abort(); loaded?.dispose();};
    // A preview belongs to one page of one authorized document.
  }, [controller, index, authority, reportError, large, signal]);
  return preview ? <img src={preview.url} alt="" width={preview.width} height={preview.height} loading="lazy" decoding="async"/> : <span className={styles.blank} aria-hidden="true">{format.toUpperCase()}</span>;
}

/** The zoom map reuses the authorized page preview of the page in view. */
export function FixedPageZoom({controller, authority, reportError, format, viewport, presentation}: Fixed & Readonly<{
  viewport: ReaderFixedViewport; presentation: 'phone' | 'tablet' | 'desktop';
}>) {
  return <ReaderZoomMap viewport={viewport} presentation={presentation} percent={defaultI18n.percent(viewport.scale)}
    page={<PageImage controller={controller} authority={authority} reportError={reportError} format={format} index={viewport.page} large/>}/>;
}

/** Comic pages use their clear-chrome footer, including the renderer's paired spreads. */
export function ComicPageFooter({controller, authority, reportError, format, crease, presentation}: Fixed & Readonly<{
  crease?: ReaderCrease; presentation: 'desktop' | 'tablet' | 'phone';
}>) {
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot);
  const root = useRef<HTMLDivElement>(null), [viewport, setViewport] = useState<ReaderViewport>();
  const count = snapshot.metadata?.pageCount, location = snapshot.location;
  const comicFormat = format === 'cbz' || format === 'cbr' ? format : undefined;
  const direction = snapshot.preferences.comicDirection === 'file' ? snapshot.metadata?.direction ?? 'ltr' : snapshot.preferences.comicDirection;
  useLayoutEffect(() => {
    const surface = root.current?.closest('[data-reader-root]')?.querySelector<HTMLElement>('[data-reader-surface]');
    if (!surface) return;
    const measure = () => {
      const width = surface.clientWidth, height = surface.clientHeight;
      if (width > 0 && height > 0) setViewport(value => value?.width === width && value.height === height ? value : {width, height});
    };
    const observer = new ResizeObserver(measure); observer.observe(surface); measure();
    return () => observer.disconnect();
  }, []);
  const position = snapshot.status === 'ready' && authority.isCurrent() && comicFormat && count !== undefined && Number.isSafeInteger(count) && count > 0
    && location && location.format === comicFormat && Number.isSafeInteger(location.page) && location.page >= 0 && location.page < count
    ? {format: comicFormat, count, page: location.page, progression: location.progression} : undefined;
  const page = position?.page;
  const pageWindow = position && viewport ? fixedPageWindow({format: position.format, viewport: {...viewport, crease}, page: position.page, pageCount: position.count,
    twoPages: snapshot.preferences.twoPages, firstPageAlone: snapshot.preferences.firstPageAlone, direction, fit: snapshot.zoom?.fit ?? snapshot.preferences.comicFit}) : undefined;
  useLayoutEffect(() => {
    root.current?.querySelector<HTMLElement>('[aria-current="page"]')?.scrollIntoView({block: 'nearest', inline: 'center', behavior: 'instant'});
  }, [page, viewport, direction, snapshot.preferences.twoPages, snapshot.preferences.firstPageAlone]);
  const pageNumbers = (pages: readonly number[]) => pages.map(value => defaultI18n.number(value + 1)).join('–');
  const navigate = (page: number) => {
    const current = controller.getSnapshot();
    if (!position || !authority.isCurrent() || current.status !== 'ready' || current.sourceId !== snapshot.sourceId || current.sourceRevision !== snapshot.sourceRevision) return;
    void controller.navigate({kind: 'location', location: {format: position.format, page, progression: position.count === 1 ? 0 : page / (position.count - 1)}}).catch(error => {
      if (authority.isCurrent()) reportError(readerErrorCode(error));
    });
  };
  return <div ref={root} className={styles.comicFooter} data-presentation={presentation}>
    {position && <>
      {pageWindow && <div className={styles.comicStrip} role="group" aria-label={t('next.reader.pagesStrip')} dir={direction === 'rtl' ? 'rtl' : 'ltr'}>
        {pageWindow.groups.map(group => <button key={`${snapshot.sourceId}:${snapshot.sourceRevision}:${group.pages[0]}`} type="button" className={styles.spread}
          aria-current={group.current ? 'page' : undefined} data-current={group.current || undefined}
          aria-label={t('next.reader.pageOf', {page: pageNumbers(group.pages), count: defaultI18n.number(position.count)})} onClick={() => navigate(group.pages[0])}>
          {group.pages.map(index => <PageImage key={index} controller={controller} authority={authority} reportError={reportError} format={format} index={index}/>)}
        </button>)}
      </div>}
      <div className={styles.comicLine}>
        <b>{t('next.reader.pageLabel', {page: pageNumbers(pageWindow?.current.pages ?? [position.page])})}</b>
        <div className={styles.comicProgress} dir={direction === 'rtl' ? 'rtl' : 'ltr'}>
          <span className={styles.comicTrack} aria-hidden="true"><i style={{width: `${position.progression * 100}%`}}/><b style={{insetInlineStart: `${position.progression * 100}%`}}/></span>
          <input type="range" min={0} max={Math.max(0, position.count - 1)} step={1} value={position.page} disabled={position.count < 2}
            aria-label={t('next.reader.position')} aria-valuetext={t('next.reader.pageOf', {page: pageNumbers(pageWindow?.current.pages ?? [position.page]), count: defaultI18n.number(position.count)})}
            onChange={event => {const value = event.currentTarget.valueAsNumber; if (Number.isInteger(value) && value >= 0 && value < position.count) navigate(value);}}/>
        </div>
        <span>{t('next.reader.pageCount', {count: position.count})}</span>
      </div>
    </>}
  </div>;
}

/** The actual document page count supplies ordinals; only visible previews mount. */
export function FixedPagesPanel(props: Fixed & Readonly<{onNavigate?: () => void}>) {
  const snapshot = useSyncExternalStore(props.controller.subscribe, props.controller.getSnapshot, props.controller.getSnapshot);
  const count = snapshot.metadata?.pageCount, location = snapshot.location;
  if (props.format === 'epub' || !count || !Number.isSafeInteger(count) || count < 1 || !location || location.format === 'epub'
    || snapshot.status !== 'ready' || !snapshot.sourceId || !snapshot.sourceRevision) return null;
  return <FixedPagesWindow key={JSON.stringify([snapshot.sourceId, snapshot.sourceRevision, count])} {...props}
    count={count} current={location.page} sourceId={snapshot.sourceId} sourceRevision={snapshot.sourceRevision}/>;
}
function FixedPagesWindow({controller, authority, reportError, format, count, current, sourceId, sourceRevision, onNavigate}: Fixed & Readonly<{
  count: number; current: number; sourceId: string; sourceRevision: string; onNavigate?: () => void;
}>) {
  const viewport = useRef<HTMLDivElement>(null);
  const [pageWindow, setPageWindow] = useState<Readonly<{collection: WindowedCollection<number>; life: AbortController}>>();
  const [life, setLife] = useState<AbortController>();
  const lifeRef = useRef<AbortController | undefined>(undefined);
  useLayoutEffect(() => {
    const captured = new AbortController(); lifeRef.current = captured; setLife(captured);
    return () => {captured.abort(); if (lifeRef.current === captured) lifeRef.current = undefined;};
  }, [controller, authority, sourceId, sourceRevision, count]);
  const pages = pageWindow && pageWindow.life === life ? pageWindow.collection : undefined;
  const [height, setHeight] = useState(0);
  const currentSource = () => {
    const snapshot = controller.getSnapshot();
    return authority.isCurrent() && snapshot.status === 'ready' && snapshot.sourceId === sourceId && snapshot.sourceRevision === sourceRevision
      && snapshot.metadata?.pageCount === count;
  };
  useEffect(() => {
    if (!life || lifeRef.current !== life || life.signal.aborted) return;
    const collection = createWindowedCollection<number>({pageSize: 24, maxResidentPages: 4, maxConcurrent: 1, prefetchPages: 0,
    keyOf: index => String(index), async fetchPage(start, size, signal) {
      const snapshot = controller.getSnapshot();
      if (signal.aborted || life.signal.aborted || lifeRef.current !== life || !authority.isCurrent() || snapshot.status !== 'ready' || snapshot.sourceId !== sourceId
        || snapshot.sourceRevision !== sourceRevision || snapshot.metadata?.pageCount !== count) throw new Error('source_changed');
      return {items: Array.from({length: Math.max(0, Math.min(size, count - start))}, (_, offset) => start + offset), total: count};
    },
    });
    setPageWindow({collection, life});
    return () => collection.dispose();
  }, [life, controller, authority, sourceId, sourceRevision, count]);
  useEffect(() => {
    const element = viewport.current; if (!element) return;
    const observer = new ResizeObserver(() => setHeight(element.clientHeight)); observer.observe(element); setHeight(element.clientHeight);
    return () => observer.disconnect();
  }, []);
  const navigate = (index: number) => {
    if (!life || lifeRef.current !== life || life.signal.aborted || !currentSource() || !Number.isSafeInteger(index) || index < 0 || index >= count) return;
    const target: ReaderLocation = {format: format === 'pdf' ? 'pdf' : 'cbz', page: index, progression: count === 1 ? 0 : index / (count - 1)};
    void controller.navigate({kind: 'location', location: target}, life.signal).then(() => {if (lifeRef.current === life && !life.signal.aborted && currentSource()) onNavigate?.();}).catch(error => {
      if (lifeRef.current === life && !life.signal.aborted && currentSource()) reportError(readerErrorCode(error));
    });
  };
  return <div className={styles.panel}>
    <p className={styles.panelCount}>{t('next.reader.pageOf', {page: defaultI18n.number(current + 1), count: defaultI18n.number(count)})}</p>
    <div ref={viewport} className={styles.panelWindow}>{height > 0 && pages && <WindowedGrid initialIndex={current} collection={pages}
      label={t('next.reader.pagesStrip')} height={height} cardWidth={104} rowHeight={156} gap={12} rowGap={12} overscanRows={1}
      renderPlaceholder={() => <span className={styles.blank} aria-hidden="true"/>}
      renderItem={index => <PageThumb controller={controller} authority={authority} reportError={reportError} format={format} index={index} count={count}
        current={index === current} large signal={life?.signal} pageLabel={t('next.reader.pageLabel', {page: defaultI18n.number(index + 1)})} onSelect={() => navigate(index)}/>}/>}</div>
  </div>;
}

/**
 * Zoom, fit and page pairing for fixed pages. Each choice is a live engine override in
 * this session, exactly like the display panel: closing the book restores the defaults.
 */
export function FixedPageMenus({controller, authority, reportError, format, open, onOpenChange, anchorRef, directionRef, moreRef}: Fixed & Readonly<{
  open: 'zoom' | 'direction' | 'more' | undefined; onOpenChange: (open: 'zoom' | 'direction' | 'more' | undefined) => void;
  anchorRef: RefObject<HTMLElement | null>; directionRef: RefObject<HTMLElement | null>; moreRef: RefObject<HTMLElement | null>;
}>) {
  useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot);
  const snapshot = controller.getSnapshot(), zoom = snapshot.zoom, preferences = snapshot.preferences;
  const failed = (error: unknown) => {if (authority.isCurrent()) reportError(readerErrorCode(error));};
  const current = () => snapshot.status === 'ready' && !!zoom && authority.isCurrent() && controller.getSnapshot() === snapshot;
  const apply = (value: ReaderZoom) => {if (current()) void controller.setZoom(value).catch(failed);};
  const override = (patch: Partial<ReadingPreferences>) => {if (current()) void controller.setOverride(patch).catch(failed);};
  const scale = zoom?.scale ?? 1;
  const fits: readonly ReaderFit[] = format === 'pdf' ? ['page', 'width', 'spread'] : ['page', 'width', 'height'];
  return <>
    <Menu open={open === 'zoom' || open === 'more'} onOpenChange={value => onOpenChange(value ? open : undefined)} title={t(open === 'more' ? 'next.controls.more' : 'next.reader.zoom')} anchorRef={open === 'more' ? moreRef : anchorRef}>
      <MenuItem onSelect={() => apply({kind: 'scale', value: Math.max(0.05, Math.round((scale / 1.25) * 100) / 100)})} disabled={snapshot.status !== 'ready' || !zoom || scale <= 0.05}>{t('next.reader.zoomOut')}</MenuItem>
      <MenuItem onSelect={() => apply({kind: 'scale', value: Math.min(8, Math.round(scale * 1.25 * 100) / 100)})} disabled={snapshot.status !== 'ready' || !zoom || scale >= 8}>{t('next.reader.zoomIn')}</MenuItem>
      <MenuSeparator/>
      {fits.map(fit => <MenuItem key={fit} disabled={snapshot.status !== 'ready' || !zoom} selected={zoom?.fit === fit} onSelect={() => apply({kind: 'fit', value: fit})}>{fitLabel(fit)}</MenuItem>)}
      <MenuSeparator/>
      <MenuItem disabled={snapshot.status !== 'ready' || !zoom} selected={!preferences.twoPages} onSelect={() => override({twoPages: false})}>{t('next.reader.onePage')}</MenuItem>
      <MenuItem disabled={snapshot.status !== 'ready' || !zoom} selected={preferences.twoPages} onSelect={() => override({twoPages: true})}>{t('next.reader.twoPages')}</MenuItem>
    </Menu>
    {format === 'cbz' && <Menu open={open === 'direction'} onOpenChange={value => onOpenChange(value ? 'direction' : undefined)} title={t('next.reader.direction')} anchorRef={directionRef}>
      {(['file', 'ltr', 'rtl'] as const).map(direction => <MenuItem key={direction} selected={preferences.comicDirection === direction}
        onSelect={() => override({comicDirection: direction})}>{t(direction === 'file' ? 'next.reader.directionFile' : direction === 'ltr' ? 'next.reader.directionLtr' : 'next.reader.directionRtl')}</MenuItem>)}
    </Menu>}
  </>;
}
