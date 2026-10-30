import {homeLayout} from './layout';
import {Fragment, useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore, type CSSProperties, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import {heroSpec} from '@design';
import {HomeService, type HomeSnapshot} from '@core/home-service.ts';
import type {HomeChannel, HomeDocument, HomeReason, HomeRow, HomeSeeAll, HomeSpotlight} from '@core/home.ts';
import type {ContentEntry} from '@core/library-content.ts';
import type {GuideChannel} from '@core/channel-guide.ts';
import {dependsOn, type FreshDocument} from '@core/freshness-hub.ts';
import {serverTextLabel} from '@core/presentation/server-text.ts';
import {homeChannelSelection, homeEntryCapabilities} from '@core/home-selection.ts';
import {homeCollectionCaption} from '@core/presentation/home-spotlight.ts';
import {channelMonograms, programmeFactParts, type TimeEnvironment} from '@portico/client-core/kit-model';
import {useFreshness, useRecovering} from '../../platform/freshness';
import {Artwork, AmbientArt, GeneratedArt} from '../../kit/artwork';
import {Hero, HeroFacts, type HeroCandidate, type LayoutPlatform} from '../../kit/layout';
import {Button} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import {CollectionBanner, LiveCard, PosterCard, RankedCard, SquareCard, WideCard, type CardSelection} from '../../kit/cards';
import {HomeSkeleton, StateLayout} from '../../kit/states';
import type {ShellArtwork} from '../../shell/artwork';
import {useShellWidth} from '../../shell/viewport';
import {currentHomeLive, heroArtworkTreatment, homeCardModel, homeLiveDeadline, homeLiveDelay, homePlaybackTarget, homeSpotlights, HOME_LIVE_MIN_MS, type HomeLiveSchedule} from './model';
import {PagedShelf} from '../browse/PagedShelf';
import {cardScale, type ScreenAppearance} from '../browse/appearance';
import styles from './home.module.css';
import type {HomeCustomizationBindings} from './customization';
import {HomeCustomizeEntry} from './Customize';
import type {ReadingContinueBindings} from './reading-continue';
import type {MusicMixesBinding} from '../browse/music-mixes-binding';
import {MusicMixesRow} from '../browse/MusicMixesRow';

export type HomeSelection = Readonly<{
  /** The canonical selection owner supplies its real bar and controls. */
  toolbar: ReactNode;
  card: (row: HomeRow, entry: ContentEntry) => CardSelection | undefined;
  channel: (row: HomeRow, channel: HomeChannel) => CardSelection | undefined;
  collection: (row: HomeRow) => CardSelection | undefined;
  registerWindow: (row: HomeRow, sectionOrder: number, first: number, last: number) => (() => void) | undefined;
  registerSpotlight: (spotlight: HomeSpotlight) => (() => void) | undefined;
  moreAvailable: (entry: ContentEntry) => boolean;
  moreExpanded: (row: HomeRow, entry: ContentEntry) => boolean;
}>;
export type HomeEffects = Readonly<{
  play: (entry: ContentEntry, target: ContentEntry) => Promise<void>;
  playChannel: (channel: GuideChannel) => Promise<void>;
  openEntry: (entry: ContentEntry) => void;
  /** Where opening an entry goes, as a link address; undefined when it is not a page (a radio seed). */
  entryHref?: (entry: ContentEntry) => string | undefined;
  openSeeAll: (target: HomeSeeAll) => void;
  openMore: (row: HomeRow, entry: ContentEntry, anchor: HTMLElement) => void;
  setMyList: (spotlight: HomeSpotlight) => Promise<void>;
  reportError: (error: unknown) => void;
  openHome?: () => void;
}>;
export type HomeBindings = Readonly<{/** One quiet line above the rows, such as libraries that did not load. */notice?: ReactNode; service: HomeService; displayDocument?: FreshDocument; artwork: ShellArtwork; effects: HomeEffects; selection: HomeSelection; time: TimeEnvironment; reducedMotion: boolean; appearance?: ScreenAppearance; serverName?: string; customization?: HomeCustomizationBindings; readingContinue?: ReadingContinueBindings; musicMixes?: MusicMixesBinding; musicMixesMore?: ReactNode}>;
type HomePresentation = Pick<HomeBindings, 'artwork' | 'effects' | 'selection' | 'time' | 'reducedMotion' | 'appearance'>;
const t = defaultI18n.t;
const pageSubscribe = (listener: () => void) => { document.addEventListener('visibilitychange', listener); return () => document.removeEventListener('visibilitychange', listener); };
/** The frame draws a long hero title at the long lockup size; no shorter drawn rule decides it (mock.html#home). */
const longHeroTitle = (title: string) => [...title.trim()].length > 20;

type HeroArtworkSlide = Readonly<{title: string; artwork: Readonly<{backdropUrl?: string; posterUrl?: string}>}>;
type HeroArtworkContext = Readonly<{index: number; visible: boolean; onReady?: () => void; onError?: () => void}>;
/**
 * The drawn hero uses only the artwork the entry actually has: a backdrop, a poster only
 * (mock.html#extras poster treatment) or no artwork (the generated title letter).
 */
export function HomeHeroArtwork({slide, context, platform, artwork, appearance, heroPixels, variant = 'hero'}: {slide: HeroArtworkSlide; context: HeroArtworkContext; platform: LayoutPlatform; artwork: ShellArtwork; appearance?: ScreenAppearance; heroPixels: number; variant?: 'hero' | 'panel'}) {
  const backdrop = slide.artwork.backdropUrl, poster = slide.artwork.posterUrl;
  const treatment = heroArtworkTreatment(slide.artwork, appearance?.showBackdrops !== false, variant);
  // No artwork: the hero is transparent over the window's standard backdrop.
  if (treatment === 'colour') return null;
  if (treatment === 'generated') return <GeneratedArt glyph={Array.from(slide.title.trim())[0] ?? ''}/>;
  if (treatment === 'poster') return <div className={styles.heroPoster}>
    <span className={styles.heroPosterWash}><Artwork source={artwork.foreground(poster, heroPixels, 2 / 3, {group: 'home-hero-poster', order: context.index, priority: 'high'})} alt="" visible={context.visible} onReady={context.onReady} onError={context.onError}/></span>
    <span className={styles.heroPosterShade}/>
    <span className={styles.heroPosterArt}><Artwork source={artwork.foreground(poster, 740 * devicePixelRatio, 2 / 3, {group: 'home-hero-poster-art', order: context.index, priority: 'high', large: true})} alt="" visible={context.visible}/></span>
  </div>;
  if (treatment === 'panel') return <span className={styles.heroPanelBackdrop}><Artwork source={artwork.foreground(backdrop, heroPixels, 16 / 9, {group: 'home-hero-panel', order: context.index})} alt="" visible={context.visible} onReady={context.onReady} onError={context.onError}/></span>;
  return <AmbientArt className={platform === 'desktop' ? styles.heroAmbient : undefined} source={artwork.ambient(backdrop, {group: 'home-hero', order: context.index, priority: 'high'})}
    foreground={artwork.foreground(backdrop, heroPixels, 16 / 9, {group: 'home-hero', order: context.index, priority: 'high', large: true})}
    treatment={platform === 'desktop' ? 'hero' : 'phone'} visible={context.visible} onReady={context.onReady} onError={context.onError} />;
}

function useVisible() {
  const ref = useRef<HTMLDivElement>(null), [intersects, setIntersects] = useState(false);
  const foreground = useSyncExternalStore(pageSubscribe, () => document.visibilityState !== 'hidden', () => false);
  useEffect(() => {
    const target = ref.current; if (!target) return;
    const observer = new IntersectionObserver(entries => setIntersects(entries.some(entry => entry.isIntersecting)));
    observer.observe(target); return () => observer.disconnect();
  }, []);
  return {ref, visible: intersects && foreground};
}

export function HomeScreen(bindings: HomeBindings) {
  const service = bindings.service, {hub} = useFreshness(), display = bindings.displayDocument;
  if (!display) throw new Error('A shell Home display registration is required.');
  useEffect(() => {
    display.setOpen(true);
    if (service.getSnapshot().phase === 'idle') void service.load().catch(bindings.effects.reportError);
    return () => display.setOpen(false);
  }, [display, service, bindings.effects.reportError]);
  const actual = useSyncExternalStore(service.subscribe, service.getSnapshot), recovering = useRecovering(display);
  const snapshot: HomeSnapshot = recovering && actual.phase === 'error' ? {...actual, phase: 'loading', error: null} : actual;
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined;
    const schedule = () => {
      if (timer !== undefined) clearTimeout(timer); timer = undefined;
      if (globalThis.document.hidden || !hub.isActive()) return;
      timer = setTimeout(() => {timer = undefined; if (hub.isActive()) hub.note(dependsOn.clock('home-time')); schedule();}, 60000);
    };
    schedule(); globalThis.document.addEventListener('visibilitychange', schedule);
    return () => {if (timer !== undefined) clearTimeout(timer); globalThis.document.removeEventListener('visibilitychange', schedule);};
  }, [hub]);
  const refreshLive = useCallback(() => display.invalidate(), [display]);
  const [now, setNow] = useState(Date.now);
  const document = snapshot.document;
  const liveSchedule = useRef<HomeLiveSchedule>({streak: 0}), liveReadAt = useRef(0);
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined;
    const refresh = () => { liveReadAt.current = Date.now(); refreshLive(); };
    const update = () => {
      if (timer) clearTimeout(timer);
      if (globalThis.document.hidden) return;
      const time = Date.now(); setNow(time);
      // An expired live row is withdrawn at once (currentHomeLive) and re-read, but at most once
      // per HOME_LIVE_MIN_MS: a server whose expiry is past or barely ahead never loops Home.
      const expired = document?.rows.some(row => row.live && !currentHomeLive(row.live, time));
      if (expired && time - liveReadAt.current >= HOME_LIVE_MIN_MS) refresh();
      const deadline = document && (homeLiveDeadline(document.rows, time) ?? (expired ? time : undefined));
      if (deadline !== undefined) {
        const next = homeLiveDelay(deadline, time, liveSchedule.current);
        liveSchedule.current = next.schedule;
        timer = setTimeout(() => { setNow(Date.now()); refresh(); }, next.delayMs);
      }
    };
    update(); globalThis.document.addEventListener('visibilitychange', update);
    window.addEventListener('resize', update);
    return () => { if (timer) clearTimeout(timer); globalThis.document.removeEventListener('visibilitychange', update); window.removeEventListener('resize', update); };
  }, [document, refreshLive]);
  const width = useShellWidth(), [started] = useState(Date.now), [elapsed, setElapsed] = useState(0);
  useEffect(() => {
    if (snapshot.document) return;
    const timers = [300, 8000].map(delay => setTimeout(() => setElapsed(Date.now() - started), Math.max(0, delay - (Date.now() - started))));
    return () => timers.forEach(clearTimeout);
  }, [snapshot.document, started]);
  if (!document && snapshot.phase === 'error') return <StateLayout className={styles.home} title={t('next.states.pageFailed')} body={t('next.states.pageFailedBody')} icon="i-warn" tone="warning" contentStyle={{top: width >= 1024 ? 470 : 250}}
    primary={{id: 'retry', icon: width >= 1024 ? 'i-retry' : undefined, label: t('next.states.tryAgain'), onPress: () => { void service.retry().catch(bindings.effects.reportError); }}}
    secondary={{id: 'home', label: t('next.states.goHome'), onPress: bindings.effects.openHome ?? (() => {void service.retry().catch(bindings.effects.reportError);})}}/>;
  if (!document) return <HomeSkeleton elapsedMs={elapsed} window={{width, height: window.innerHeight, scrollTop: window.scrollY}}
    slowNotice={bindings.serverName ? <div className={styles.slowNotice} role="status"><span className={styles.waitingDot}/><span><b>{t('next.states.stillLoading')}</b>{' '}{t('next.states.slowConnection', {server: bindings.serverName})}</span></div> : undefined}/>;
  return <HomeView {...bindings} document={document} nowMs={now} refreshLive={refreshLive} onMore={rowId => { void service.more(rowId).catch(bindings.effects.reportError); }} />;
}

type Spotlight = Omit<ReturnType<typeof homeSpotlights>[number], 'reason'> & HeroCandidate & {causalReason: HomeReason; source: HomeSpotlight};
export function HomeView({service, customization, document, nowMs, onMore, refreshLive, artwork, effects, selection, time, reducedMotion, appearance, musicMixes, musicMixesMore, notice}: HomeBindings & {document: HomeDocument; nowMs: number; onMore: (rowId: string) => void; refreshLive: () => void}) {
  const width = useShellWidth(), layout = homeLayout(width), {platform, sizeClass} = layout;
  const heroPixels = Math.max(width, heroSpec.backdropWidth[platform === 'desktop' || sizeClass === 'medium' ? 'web' : 'phone']) * devicePixelRatio;
  const hero = useVisible(), env = useMemo(() => ({t, locale: time.locale}), [time.locale]);
  const candidates: Spotlight[] = useMemo(() => homeSpotlights(document, env).map((slide, index) => ({...slide, long: longHeroTitle(slide.title), source: document.spotlight[index], causalReason: slide.reason, reason: serverTextLabel(defaultI18n, slide.reason.text)})), [document, env]);
  useEffect(() => {
    if (!hero.visible) return;
    const cleanups = document.spotlight.map(selection.registerSpotlight);
    return () => { for (const cleanup of cleanups) cleanup?.(); };
  }, [document.spotlight, hero.visible, selection]);
  const rows = document.rows.filter(row => !row.platforms || row.platforms.includes('web'));
  const mixesAfter = rows.findIndex(row => row.id === 'listen_next');
  const mixes = musicMixes ? <MusicMixesRow binding={musicMixes} artwork={artwork} more={musicMixesMore} sizeClass={sizeClass}
    reducedMotion={appearance?.reducedMotion ?? reducedMotion} reportError={effects.reportError}/> : null;
  const play = (entry: ContentEntry, resume: ContentEntry) => {
    const target = homePlaybackTarget(entry, resume);
    if (target) void effects.play(entry, resume).catch(effects.reportError);
  };
  return <div className={styles.home} data-size-class={sizeClass} data-home-document={document.viewerFence}>
    {candidates.length > 0 && <div ref={hero.ref}><Hero<Spotlight> candidates={candidates} platform={platform} sizeClass={sizeClass} visible={hero.visible} reducedMotion={appearance?.reducedMotion ?? reducedMotion}
      renderReason={slide => slide.reason}
      titleLink={slide => { const href = effects.entryHref?.(slide.entry); return href ? {href, open: () => effects.openEntry(slide.entry)} : undefined; }}
      renderMetadata={slide => <HeroFacts parts={slide.facts} platform={platform} sizeClass={sizeClass} />}
      renderArtwork={(slide, context) => <HomeHeroArtwork slide={slide} context={context} platform={platform} artwork={artwork} appearance={appearance} heroPixels={heroPixels} />}
      renderThumbnail={(slide, context) => <Artwork source={artwork.foreground(slide.artwork.backdropUrl ?? slide.artwork.posterUrl, 132 * devicePixelRatio, 16 / 9, {group: 'home-thumbnails', order: context.index})} alt="" visible={context.visible} />}
      renderActions={slide => <>
        {slide.primary && homePlaybackTarget(slide.entry, slide.primary.entry) && <Button variant="primary" size="hero" phone={platform !== 'desktop'} icon="i-play" onClick={() => play(slide.entry, slide.primary!.entry)}>{t(slide.primary.label.key, slide.primary.label.params)}</Button>}
        {homeEntryCapabilities(slide.entry, {platform: 'web', playbackAvailable: true, queueAvailable: true}).watchlisted !== undefined && <Button variant="glass" size="hero" phone={platform !== 'desktop'} icon={slide.entry.watchlisted ? 'i-check' : 'i-plus'} aria-pressed={slide.entry.watchlisted === true}
          aria-label={t(slide.entry.watchlisted ? 'next.home.removeMyList' : 'next.home.addMyList')}
          onClick={() => { void effects.setMyList(slide.source).catch(effects.reportError); }}>{t('next.home.myList')}</Button>}
      </>} /></div>}
    {notice}
    {selection.toolbar}
    <div className={styles.rows} data-with-hero={candidates.length > 0}>
      {rows.map((row, index) => <Fragment key={row.id}><HomeRowView focusScope={service} row={row} rowIndex={index} platform={platform} layout={layout} nowMs={nowMs} refreshLive={refreshLive} onMore={() => onMore(row.id)}
        artwork={artwork} effects={effects} selection={selection} time={time} appearance={appearance} reducedMotion={appearance?.reducedMotion ?? reducedMotion} />{index === mixesAfter && mixes}</Fragment>)}
      {mixesAfter < 0 && mixes}
      {customization && <HomeCustomizeEntry service={service} binding={customization}/>}
    </div>
  </div>;
}

type HomeRowProps = HomePresentation & {focusScope: object; row: HomeRow; rowIndex: number; platform: LayoutPlatform; layout: ReturnType<typeof homeLayout>; nowMs: number; onMore: () => void; refreshLive: () => void};
function HomeRowView(props: HomeRowProps) {
  const {row, rowIndex, selection, refreshLive} = props;
  const interest = useVisible(), [range, setRange] = useState<readonly [number, number]>(props.row.collection ? [0, 0] : [-1, -1]);
  const onRange = useCallback((first: number, last: number) => setRange(before => before[0] === first && before[1] === last ? before : [first, last]), []);
  // Intersection can run after a suspended deadline timer. Use the actual clock
  // before both registration and rendering, even when the supplied tick is old.
  const nowMs = Date.now(), liveCurrent = !row.live || !!currentHomeLive(row.live, nowMs);
  useEffect(() => {
    if (!interest.visible) return;
    if (!liveCurrent) { refreshLive(); return; }
    return selection.registerWindow(row, rowIndex, range[0], range[1]);
  }, [interest.visible, liveCurrent, refreshLive, selection, row, rowIndex, range]);
  return <div ref={interest.ref} className={styles.rowSlot} data-home-row={props.row.id}><HomeRowContents {...props} nowMs={props.row.live ? nowMs : props.nowMs} onRange={onRange} /></div>;
}
function HomeRowContents({row, rowIndex, platform, layout, nowMs, onMore, onRange, focusScope, ...bindings}: HomeRowProps & {onRange: (first: number, last: number) => void}) {
  const env = {t, locale: bindings.time.locale};
  const phone = platform !== 'desktop';
  // The On now row's drawn trailing link is "Guide", not "See all", and it stays visible
  // beside the heading instead of waiting for the row's hover (mock.html#home).
  const guide = !phone && row.seeAll?.type === 'guide';
  const header = {heading: serverTextLabel(defaultI18n, row.titleText, row.title),
    subtitle: !phone || ['on_now', 'listen_next', 'continue_listening'].includes(row.id) ? serverTextLabel(defaultI18n, row.subtitleText) : undefined, platform, sizeClass: layout.sizeClass,
    actions: guide ? <button type="button" className={styles.rowGuide} onClick={() => bindings.effects.openSeeAll(row.seeAll!)}>{t('next.controls.guide')}<Icon name="i-right" size={18} /></button> : undefined,
    onSeeAll: row.seeAll && !guide && (!phone || !['continue', 'continue_books', 'community_watching'].includes(row.id)) ? () => bindings.effects.openSeeAll(row.seeAll!) : undefined};
  const wide = row.artworkShape === 'landscape', square = row.artworkShape === 'square';
  const ranked = row.id === 'community_watching' && !!row.period;
  const scale = cardScale(bindings.appearance);
  // The ranked slot is the number plus the poster less the drawn overlap:
  // 240px on desktop and 170px on phone (92+170-22 / 70+116-16), matching the frame slot.
  const cardWidth = (ranked ? platform === 'desktop' ? 240 : 170 : wide ? layout.wide : square ? layout.square : layout.poster) * scale;
  const live = currentHomeLive(row.live, nowMs);
  if (row.live) {
    if (!live) return null;
    const marks = channelMonograms({kind: 'authoritative', channels: live.channels.map(({channel}) => ({sourceId: channel.sourceId, id: channel.id, mark: channel.mark}))});
    const liveWidth = (platform === 'desktop' ? 316 : 244) * scale, liveHeight = (platform === 'desktop' ? 178 : 140) * scale;
    return <PagedShelf {...header} focusScope={focusScope} items={live.channels} hasMore={!!row.nextCursor} continuation={row.nextCursor} onMore={onMore} cardWidth={liveWidth} artworkRatio={liveWidth / liveHeight} getKey={value => JSON.stringify([value.channel.sourceId, value.channel.id])} reducedMotion={bindings.reducedMotion}
      onVisibleRange={onRange}
      renderItem={(item, context) => <VisibleLive item={item} mark={marks.get(item.channel.sourceId)!.get(item.channel.id)!} nowMs={nowMs} order={rowIndex * 1000 + context.index} row={row} {...bindings} />} />;
  }
  if (row.collection) {
    return <VisibleCollection row={row} rowIndex={rowIndex} {...bindings} />;
  }
  if (!row.entries.length) return null;
  return <PagedShelf {...header} focusScope={focusScope} items={row.entries} hasMore={!!row.nextCursor} continuation={row.nextCursor} onMore={onMore} cardWidth={cardWidth} artworkRatio={ranked ? cardWidth / ((platform === 'desktop' ? 255 : 174) * scale) : wide ? 16 / 9 : square ? 1 : 2 / 3} getKey={entry => entry.id} reducedMotion={bindings.reducedMotion} onVisibleRange={onRange}
    renderItem={(entry, context) => <VisibleCard entry={entry} row={row} rowIndex={rowIndex} index={context.index} platform={platform} layout={layout} {...bindings} />} />;
}

function VisibleCollection({row, rowIndex, artwork, effects, selection, time, appearance}: HomePresentation & {row: HomeRow; rowIndex: number}) {
  const interest = useVisible(), collection = row.collection!;
  const open = () => effects.openSeeAll({type: 'collection', resourceId: collection.collection.id, libraryId: collection.collection.libraryId});
  return <div ref={interest.ref} className={styles.collection}><CollectionBanner model={{title: collection.collection.name, caption: []}} description={homeCollectionCaption(collection, {t, locale: time.locale})[0]?.text} kindLabel={t('next.home.collection')} position="60% 30%"
    visible={interest.visible} arrivalDurationMs={240}
    selection={selection.collection(row)}
    artwork={appearance?.showBackdrops === false ? undefined : artwork.foreground(collection.backdropUrl, 1312 * devicePixelRatio, 16 / 9, {order: rowIndex * 1000, group: row.id})}
    covers={collection.members.slice(0, 4).flatMap((entry, index) => { const art = artwork.foreground(entry.posterUrl, 50 * devicePixelRatio, 2 / 3, {order: rowIndex * 1000 + index, group: row.id}); return art ? [art] : []; })}
    actionLabel={t('next.home.openCollection')} actionVariant="glass" onAction={open} onNavigate={open} /></div>;
}

function VisibleCard({entry, row, rowIndex, index, platform, layout, artwork, effects, selection, time, appearance}: HomePresentation & {entry: ContentEntry; row: HomeRow; rowIndex: number; index: number; platform: LayoutPlatform; layout: ReturnType<typeof homeLayout>}) {
  const interest = useVisible(), wide = row.artworkShape === 'landscape', square = row.artworkShape === 'square';
  const scale = cardScale(appearance), width = (wide ? layout.wide : square ? layout.square : layout.poster) * scale;
  const resume = row.resumes?.find(value => value.entryId === entry.id)?.target ?? entry;
  const target = homePlaybackTarget(entry, resume);
  // A continuing Listen next card draws the bar under its art and the time left where the
  // author/artist line otherwise sits (mock.html#home); both parts come from the shared card text.
  const continued = square && row.id === 'listen_next' ? homeCardModel(resume, true, {t, locale: time.locale}) : undefined;
  const left = continued?.detail.find(part => part.id === 'remaining');
  const model = continued && left ? {...continued, caption: [left], detail: []} : homeCardModel(wide ? resume : entry, wide, {t, locale: time.locale});
  const source = wide ? entry.backdropUrl ?? entry.posterUrl : entry.posterUrl;
  // The drawn ranked card carries the number and the title only (mock.html#home).
  const ranked = row.id === 'community_watching' && !!row.period;
  const props = {model: ranked ? {...model, caption: []} : model, visible: interest.visible, arrivalDurationMs: 240, watched: entry.watched, isNew: entry.new, style: {width: '100%'},
    artwork: artwork.foreground(source, width * devicePixelRatio, wide ? 16 / 9 : square ? 1 : 2 / 3, {order: rowIndex * 1000 + index, group: row.id}),
    fallback: <GeneratedArt glyph={Array.from((entry.displayTitle ?? entry.title).trim())[0] ?? ''} />,
    onNavigate: () => effects.openEntry(entry),
    onPlay: target ? () => { void effects.play(entry, resume).catch(effects.reportError); } : undefined,
    selection: selection.card(row, entry),
    onMore: selection.moreAvailable(entry) ? (anchor: HTMLButtonElement) => effects.openMore(row, entry, anchor) : undefined,
    moreExpanded: selection.moreExpanded(row, entry)};
  return <div ref={interest.ref} className={ranked ? styles.scaledRanked : undefined} style={ranked ? {'--home-card-scale': scale} as CSSProperties : undefined}>{ranked ? <RankedCard {...props} rank={row.start + index + 1} /> : wide ? <WideCard {...props} /> : square ? <SquareCard {...props} progressPlacement={left ? 'below' : 'edge'} variant={entry.kind === 'book' || entry.kind === 'audiobook_file' ? 'audiobook' : 'music'} /> : <PosterCard {...props} />}</div>;
}

function VisibleLive({item, mark, nowMs, order, row, artwork, effects, time, selection, appearance}: HomePresentation & {item: HomeChannel; mark: Parameters<typeof LiveCard>[0]['channel']['mark']; nowMs: number; order: number; row: HomeRow}) {
  const interest = useVisible(), width = useShellWidth(), {channel} = item;
  const programme = channel.programmes.find(item => Date.parse(item.start) <= nowMs && nowMs < Date.parse(item.end));
  const start = programme && Date.parse(programme.start), end = programme && Date.parse(programme.end);
  const current = () => !!currentHomeLive(row.live, Date.now()) && !!selection.channel(row, item);
  const play = () => { if (current()) void effects.playChannel(channel).catch(effects.reportError); };
  const canPlay = homeChannelSelection(item, {platform: 'web', playbackAvailable: true, queueAvailable: true}).capabilities.play;
  return <div ref={interest.ref}><LiveCard style={{width: '100%', height: (width >= 1024 ? 178 : 140) * cardScale(appearance)}} channel={{sourceId: channel.sourceId, id: channel.id, name: channel.name, number: channel.number, mark}}
    programme={programme ? {title: programme.title, facts: programmeFactParts({startMs: start, endMs: end, subtitle: programme.subtitle, categories: programme.categories, rating: programme.rating?.value, flags: programme.flags, recording: programme.recordingState === 'recording'}, time, nowMs), progress: start !== undefined && end !== undefined && end > start ? (nowMs - start) / (end - start) : undefined, live: programme.flags?.live === true, isNew: programme.flags?.new === true, premiere: programme.flags?.premiere === true} : undefined}
    visible={interest.visible} selection={selection.channel(row, item)} logo={artwork.foreground(channel.logoPath, 34 * devicePixelRatio, 1, {group: row.id, order})} onNavigate={canPlay ? play : () => { if (current()) effects.openSeeAll({type: 'guide', sourceId: channel.sourceId}); }} onPlay={canPlay ? play : undefined} /></div>;
}
