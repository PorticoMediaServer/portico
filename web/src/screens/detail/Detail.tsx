import {t, locale, environment as env} from './text';
import {useId, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode, type RefObject} from 'react';
import type {DetailProjection} from '@core/detail.ts';
import type {ContentEntry} from '@core/library-content.ts';
import type {DownloadOptionsView} from '@core/downloads.ts';
import type {BrowserDownloadTarget} from '../../platform/browser-download';
import type {EntryActionTarget} from '../../shell/entry-actions';
import {DownloadNotice} from './DownloadNotice';
import type {PlaybackOptions} from '@core/playback-v1/types.ts';
import {detailActions} from '@core/presentation/detail-actions.ts';
import {refreshMetadataPath} from '@core/presentation/entry-actions.ts';
import {titleRecommendationRows, type ItemRecommendations} from '@core/recommendations.ts';
import {listeningSimilarPager, recommendationRowPager, type ListeningSimilarRead, type RelationRowOwner} from '@core/relation-rows.ts';
import {relatedRowPager, type RelatedMovieRow} from '@core/related-movies.ts';
import type {ListeningViewerScope} from '@core/listening.ts';
import {useRelationRow} from './relation-row';
import {serverTextLabel} from '@core/presentation/server-text.ts';
import {serviceI18n, serviceText} from '@core/presentation/service-text.ts';
import {titleFactParts} from '@core/presentation/kit-model';
import {preferenceValue} from '@core/preferences.ts';
import {AmbientArt, Artwork, PersonCircle, type AmbientArtProps} from '../../kit/artwork';
import {Button, ResumeButton, Select, Tabs} from '../../kit/controls';
import {Lockup, Shelf} from '../../kit/layout';
import {CollectionBanner, PosterCard, SquareCard} from '../../kit/cards';
import {Menu, MenuGroup, MenuItem, MenuSeparator, Dialog, ToastHost, type ToastData} from '../../kit/overlays';
import {StateView} from '../../kit/states';
import {Icon} from '../../kit/Icon';
import {useShellWidth} from '../../shell/viewport';
import {entryCard, titlePageFacts, calendarDate, instantDate} from './model';
import type {DetailBindings} from './bindings';
import styles from './detail.module.css';
import {dependsOn, titlesWithPersonalState} from '@core/freshness-hub.ts';
import {useFreshDocument} from '../../platform/freshness';
import {TitleProse, TitleFactsLine} from '../../kit/title/TitleParts';
import {TitlePeople, CreditLine, CreditNames} from './People';
import {useTitleArtworkClaim, useTitleCopyEntrance} from './title-artwork-motion';
import type {SourceArtClaim} from '../../kit/cards/source-art-transition';
import {useLayoutMotion} from '../../kit/layout/motion';
import motionStyles from './title-hero-motion.module.css';
import {FileManagement} from './FileManagement';
import {copyLink} from '../../shell/copy-link';
import {TitleFileInfo, type FileInfoManageContext} from './TitleFileInfo';
import {TitleTabs} from './TitleTabs';
import {ScreenProblem} from './ScreenProblem';
import {runScreenCommand} from './context';
import {DetailLoading} from './Loading';
import {GenreNames} from './GenreNames';
import {StartRadioMenuItem} from './StartRadio';
import {PagedShelf} from '../browse/PagedShelf';
export {DetailLoading} from './Loading';

export type DetailScreenProps = {binding: DetailBindings; itemId: string; libraryId?: string; kind?: string; artworkClaim?: SourceArtClaim};
export function DetailScreen({binding, itemId, libraryId, kind, artworkClaim}: DetailScreenProps) {
  const [failure, setFailure] = useState<unknown>(), [attempt, setAttempt] = useState(0);
  useEffect(() => { const request = new AbortController(); setFailure(undefined); void binding.select({itemId, libraryId, kind}, request.signal).catch(error => { if (!request.signal.aborted) setFailure(error); }); return () => { request.abort(); binding.cancel(); }; }, [binding, itemId, libraryId, kind, attempt]);
  return failure ? <ScreenProblem context={binding.context} error={failure} retry={() => setAttempt(value => value + 1)} /> : <TitleBody binding={binding} artworkClaim={artworkClaim} />;
}

/** Also mounted under the retained phone/tablet Watch surface. It owns no route or video. */
export function TitleBody({binding, compact = false, artworkClaim}: {binding: DetailBindings; compact?: boolean; artworkClaim?: SourceArtClaim}) {
  const tabPanelId = useId();
  const snapshot = useSyncExternalStore(binding.service.subscribe, binding.service.getSnapshot, binding.service.getSnapshot);
  useFreshDocument(() => [dependsOn.libraries, ...titlesWithPersonalState(binding.service.getSnapshot().target?.libraryId), dependsOn.event('stream.resync')], refresh => binding.service.revalidate(refresh), () => binding.service.getSnapshot().phase === 'ready');
  const preferences = useSyncExternalStore(binding.context.preferences.subscribe, binding.context.preferences.getSnapshot, binding.context.preferences.getSnapshot);
  const data = snapshot.data, {tab} = useSyncExternalStore(binding.view.subscribe, binding.view.getSnapshot, binding.view.getSnapshot);
  const recommendations = useSyncExternalStore(binding.recommendations.subscribe, binding.recommendations.getSnapshot, binding.recommendations.getSnapshot);
  useEffect(() => { if (data?.item.kind === 'movie') void binding.readRecommendations(data); }, [binding, data]);
  // music-plus f7 (MC-9): an album's More like this is the server's own bounded ranking, read
  // for this captured page and item; it ranks by sound once the album is analyzed.
  const [similar, setSimilar] = useState<Readonly<{data: DetailProjection} & Awaited<ReturnType<DetailBindings['readSimilar']>>>>();
  useEffect(() => {
    if (!data || data.item.kind !== 'album') { setSimilar(undefined); return; }
    const request = new AbortController();
    setSimilar(undefined);
    void binding.readSimilar(data, request.signal).then(read => { if (!request.signal.aborted) setSimilar({data, ...read}); }, () => undefined);
    return () => request.abort();
  }, [binding, data]);
  // Keep the viewport hook unconditional while the detail request is loading.
  const standalonePhone = useShellWidth() < 600;
  if (!data) return snapshot.phase === 'error' ? <ScreenProblem context={binding.context} error={snapshot.error} subject={binding.subject()} retry={() => { void binding.service.retry(); }} /> : <DetailLoading key={snapshot.target?.itemId} />;
  const tabs = [{value: 'related', label: t('next.controls.moreLikeThis')}, {value: 'details', label: t('next.controls.details')}, ...(data.extras?.some(extra => extra.items.length) ? [{value: 'extras', label: t('next.controls.extras')}] : [])];
  return <article className={styles.page} data-compact={compact || undefined} data-title-id={data.scope.itemId}>
    {standalonePhone && !compact && <div className={styles.phoneTopBar}>{binding.context.effects.back && <Button variant="icon" icon="i-left" iconOnly aria-label={t('next.states.back')} onClick={() => runScreenCommand(binding.context, () => { binding.current(data); binding.context.effects.back?.(); })} />}</div>}
    {!compact && <TitleHero binding={binding} data={data} artworkClaim={artworkClaim} showBackdrops={preferenceValue<boolean>(preferences, 'appearance.showBackdrops')} />}
    {compact && <div className={styles.compactHeader}><h1>{data.item.title}</h1><TitleActions binding={binding} data={data} /></div>}
    <TitleTabs title={data.item.title} compact={compact} onBack={binding.context.effects.back ? () => runScreenCommand(binding.context, () => { binding.current(data); binding.context.effects.back?.(); }) : undefined} actions={!compact && <TitlePinnedActions binding={binding} data={data} />}><Tabs externalPanelId={tabPanelId} className={styles.tabStrip} aria-label={t('next.controls.details')} value={tab} onValueChange={value => runScreenCommand(binding.context, () => { binding.current(data); if (value === 'related' || value === 'details' || value === 'extras') binding.view.tab(value); })} items={tabs} /></TitleTabs>
    {snapshot.phase === 'error' && <ScreenFailure retry={() => { void binding.service.retry(); }} />}
    {snapshot.mutationError && <ScreenFailure retry={() => { void binding.service.retryMutation(); }} />}
    <div key={tab} id={tabPanelId} role="tabpanel" aria-labelledby={`${tabPanelId}-tab-${tab}`} tabIndex={0} className={tab === 'details' ? styles.tabPanel : `${styles.tabPanel} ${styles.rowsPanel}`}>{tab === 'details' ? <TitleDetails binding={binding} data={data} /> : tab === 'extras' ? data.extras?.map(extra => <TitleRow key={extra.type} heading={extra.label} entries={extra.items} binding={binding} />)
      : recommendations.data ? titleRecommendationRows(recommendations.data).map(row => <TitleRecommendationRow key={row.id} row={row} owner={{kind: 'item', id: data.item.id}} binding={binding} subtitleVisible={!compact} guard={() => binding.current(data)} />)
        : similar?.data === data ? (similar.value.entries.length > 0 && <SimilarTitleRow first={similar.value} scope={similar.scope} heading={t('music.moreLikeThis')} subtitle={t(similar.value.bySound ? 'music.moreLikeThisBySound' : 'music.moreLikeThisHint')} binding={binding} guard={() => binding.current(data)} />)
        : data.related?.rows.map(row => <RelatedTitleRow key={row.id} row={row} data={data} binding={binding} guard={() => binding.current(data)} />)}</div>
  </article>;
}

function TitleHero({binding, data, artworkClaim, showBackdrops}: {binding: DetailBindings; data: DetailProjection; artworkClaim?: SourceArtClaim; showBackdrops: boolean}) {
  const width = useShellWidth(), phone = width < 1024, {item, facts, metadata} = data, art = binding.context.artwork;
  const [copyNode, setCopyNode] = useState<HTMLDivElement | null>(null);
  const {reduced} = useLayoutMotion();
  // The route owner supplies the one claim; nothing is taken during render.
  const foreground = art.foreground(item.backdropUrl ?? item.posterUrl, phone ? 800 : 1920, 16 / 9);

  useTitleCopyEntrance(copyNode, item.id, reduced);
  const directed = metadata.credits.filter(credit => credit.role.toLowerCase() === 'director');
  const starring = metadata.credits.filter(credit => credit.department.toLowerCase() === 'acting' || credit.role.toLowerCase() === 'cast').slice(0, 4);
  const parts = titlePageFacts({year: item.year, durationSeconds: item.duration, genres: metadata.genres.map(genre => genre.name), contentRating: facts?.contentRating}, env);
  const genres = <GenreNames names={metadata.genres.map(value => value.name)} onOpen={binding.context.effects.openBrowse ? genre => runScreenCommand(binding.context, () => binding.browseGenre(data, genre)) : undefined} />;
  if (phone) {
    const phoneParts = [...parts.filter(part => part.id === 'year' || part.id === 'genres'), ...parts.filter(part => part.id !== 'year' && part.id !== 'genres')];
    return <header className={styles.phoneHero}>
      <div className={styles.phonePicture}>
        {showBackdrops && <TitleArtwork key="phone" artworkClaim={artworkClaim} titleKey={item.id} reduced={reduced} className={styles.phoneHeroArt} treatment="phone" source={art.ambient(item.backdropUrl ?? item.posterUrl)} foreground={foreground} position="22% 30%" />}
        <div className={styles.phoneCopy} ref={setCopyNode}><Lockup title={item.title} long platform="phone-web" /><TitleFactsLine parts={phoneParts} genres={genres} /></div>
      </div>
      <TitleActions binding={binding} data={data} />
      {item.overview && <TitleProse variant="hero" className={styles.phoneSynopsis} markup={item.overview}/>}
    </header>;
  }
  return <header className={styles.hero}>
    {showBackdrops && <TitleArtwork key="desktop" artworkClaim={artworkClaim} titleKey={item.id} reduced={reduced} className={styles.heroArt} treatment={phone ? 'phone' : 'title'} source={art.ambient(item.backdropUrl ?? item.posterUrl)} foreground={foreground} />}
    <div className={styles.copy} ref={setCopyNode}>
      {data.collections?.[0] && <CollectionLink collection={data.collections[0]} onOpen={() => runScreenCommand(binding.context, () => { binding.current(data); binding.context.effects.openEntry({id: data.collections![0].id, libraryId: data.scope.libraryId, kind: 'collection', title: data.collections![0].name, navigation: {view: 'collection', entityId: data.collections![0].id}}); })} />}
      <Lockup title={item.title} platform={phone ? 'phone-web' : 'desktop'} />
      <TitleFactsLine parts={parts} genres={genres} />
      {facts?.tagline && <p className={styles.tagline}>{facts.tagline}</p>}
      {item.overview && <TitleProse variant="hero" className={styles.blurb} markup={item.overview}/>}
      {(directed.length > 0 || starring.length > 0) && <div className={styles.credits}>{directed.length > 0 && <CreditLine message="title.directedBy" credits={directed} binding={binding} data={data} />}{starring.length > 0 && <CreditLine message="title.starring" credits={starring} binding={binding} data={data} />}</div>}
      <TitleActions binding={binding} data={data} />
    </div>
  </header>;
}

/** The wrapper commits before the foreground resource starts. The callback
 * captured by Artwork's original lease therefore owns this exact physical node,
 * including a phone/desktop replacement while a decode is pending. */
function TitleArtwork({artworkClaim, titleKey, reduced, ...props}: AmbientArtProps & {artworkClaim?: SourceArtClaim; titleKey: string; reduced: boolean}) {
  const [node, setNode] = useState<HTMLDivElement | null>(null);
  const ready = useTitleArtworkClaim(node, artworkClaim, titleKey, reduced), foreground = props.foreground;
  return <div className={motionStyles.art} ref={setNode}>{node && <AmbientArt {...props} onReady={source => {
    if (foreground && source.key === foreground.key && source.scheduler === foreground.scheduler) ready(node);
  }}/>}</div>;
}

/** The hero's collection link. Its total is every visible member the server counted, not
    only films, so the count keeps that real total and names no member kind: the catalogue's
    neutral collection noun already used for a collection's own entry count. */
export function CollectionLink({collection, onOpen}: {collection: NonNullable<DetailProjection['collections']>[number]; onOpen: () => void}) {
  return <button className={styles.collection} type="button" onClick={onOpen}><Icon name="i-layers" size={18} />{collection.name}<span className={styles.collectionCount}>{t('lib.count', {count: collection.memberCount, pivot: 'other'})}</span></button>;
}

export function TitleActions({binding, data, small = false, topResult = false}: {binding: DetailBindings; data: DetailProjection; small?: boolean; topResult?: boolean}) {
  const phone = useShellWidth() < 1024 && !topResult;
  const model = detailActions(data, {platform: 'web'}), primary = model.primary;
  const primaryAction = data.actions.find(action => action.enabled && action.playback && action.id !== 'start_over');
  const restart = data.actions.find(action => action.id === 'start_over' && action.enabled && action.playback);
  const run = (task: Promise<unknown>) => { void task.catch(binding.context.effects.reportError); };
  const size = small ? 'compact' as const : 'hero' as const;
  // The drawn caption is the title's own long duration ("1 h 26 min left"), not the compact one.
  // An unknown length has no time left to state and no progress line to draw; never a zero.
  const length = data.item.duration ?? 0, fraction = length > 0 ? data.personal.progressSeconds / length : 0;
  const left = length > 0 ? titleFactParts({durationSeconds: Math.max(0, length - data.personal.progressSeconds)}, env).find(part => part.id === 'duration')?.text : undefined;
  const remaining = primary?.id === 'resume' && left ? serviceText('duration.remaining', {duration: left}) : primary?.caption;
  return <><div className={styles.actions} data-phone-title={phone && !small || undefined}>
    {primary && primaryAction && (primary.id === 'resume' && !phone ? <ResumeButton size={size} progress={fraction} remaining={remaining} onClick={() => run(binding.play(data, primaryAction.id))}>{t('next.controls.resume')}</ResumeButton>
      : <Button variant="primary" icon="i-play" size={size} onClick={() => run(binding.play(data, primaryAction.id))}>{t(primary.id === 'resume' ? 'next.controls.resume' : 'next.controls.play')}</Button>)}
    {!small && <>
      {!phone && !topResult && restart && primary?.id === 'resume' && <Button icon="i-retry" size={size} onClick={() => run(binding.play(data, restart.id))}>{t('next.controls.startOver')}</Button>}
      {data.actions.some(action => action.id === 'watchlist' && action.enabled) && <Button icon={data.personal.watchlisted ? 'i-check' : 'i-plus'} size={size} aria-pressed={data.personal.watchlisted} onClick={() => run(binding.mutate(data, {action: 'watchlist', value: !data.personal.watchlisted}))}>{t('next.controls.myList')}</Button>}
      {!phone && !topResult && data.actions.some(action => action.id === 'watched' && action.enabled) && <Button icon="i-check" iconOnly aria-label={t(data.personal.watched ? 'title.markUnwatched' : 'title.markWatched')} aria-pressed={data.personal.watched} size={size} onClick={() => run(binding.mutate(data, {action: 'watched', value: !data.personal.watched}))} />}
      <TitleMore binding={binding} data={data} />
    </>}
  </div>{phone && !small && primary?.id === 'resume' && <div className={styles.phoneResume}>
    <div className={styles.phoneProgress} role="progressbar" aria-label={t('next.layout.progress')} aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(Math.min(1, Math.max(0, fraction)) * 100)}><span style={{transform: `scaleX(${Math.min(1, Math.max(0, fraction))})`}} /></div>
    <div className={styles.phoneRemaining}>{remaining}{restart && <Button variant="text" icon="i-retry" size="small" onClick={() => run(binding.play(data, restart.id))}>{t('next.controls.startOver')}</Button>}</div>
  </div>}</>;
}

/** The drawn More menu's Rate… row. The value is the viewer's own star rating, saved to the title. */
function TitleRating({binding, data, onClose, returnFocusRef}: {binding: DetailBindings; data: DetailProjection; onClose: () => void; returnFocusRef: RefObject<HTMLButtonElement | null>}) {
  const [value, setValue] = useState<number | null>(data.personal.rating ?? null), [busy, setBusy] = useState(false);
  const save = () => {
    setBusy(true);
    void binding.mutate(data, {action: 'rating', value}).then(onClose, error => { setBusy(false); binding.context.effects.reportError(error); });
  };
  return <Dialog open onOpenChange={open => { if (!open && !busy) onClose(); }} title={t('title.rate', {title: data.item.title})} returnFocusRef={returnFocusRef} dismissOnOutsidePress={!busy} dismissOnBack={!busy}
    footer={<><Button onClick={onClose} disabled={busy}>{t('action.close')}</Button><Button variant="primary" onClick={save} disabled={busy}>{t('action.save')}</Button></>}>
    <Select label={t('next.home.yourRating')} aria-label={t('next.home.yourRating')} value={value === null ? 'none' : String(value)} disabled={busy}
      options={[{value: 'none', label: t('next.home.noRating')}, ...Array.from({length: 11}, (_, stars) => ({value: String(stars), label: t('next.home.ratingValue', {value: stars})}))]}
      onValueChange={next => setValue(next === 'none' ? null : Number(next))} />
  </Dialog>;
}

/** The pinned bar's own pair, as drawn once the title scrolls away: Play/Resume and My List. */
function TitlePinnedActions({binding, data}: {binding: DetailBindings; data: DetailProjection}) {
  const primary = detailActions(data, {platform: 'web'}).primary;
  const action = data.actions.find(value => value.enabled && value.playback && value.id !== 'start_over');
  const watchlist = data.actions.find(value => value.id === 'watchlist' && value.enabled);
  const run = (task: Promise<unknown>) => { void task.catch(binding.context.effects.reportError); };
  return <div className={styles.actions}>
    {primary && action && <Button variant="primary" icon="i-play" size="small" onClick={() => run(binding.play(data, action.id))}>{t(primary.id === 'resume' ? 'next.controls.resume' : 'next.controls.play')}</Button>}
    {watchlist && <Button variant="glass" icon={data.personal.watchlisted ? 'i-check' : 'i-plus'} iconOnly size="small" aria-label={t('next.controls.myList')} title={t('next.controls.myList')} aria-pressed={data.personal.watchlisted} onClick={() => run(binding.mutate(data, {action: 'watchlist', value: !data.personal.watchlisted}))} />}
  </div>;
}

function TitleMore({binding, data}: {binding: DetailBindings; data: DetailProjection}) {
  const [open, setOpen] = useState(false), [fileOpen, setFileOpen] = useState(false), [titlesOpen, setTitlesOpen] = useState(false);
  const [manage, setManage] = useState<FileInfoManageContext>(), [rating, setRating] = useState(false), [linkToast, setLinkToast] = useState<ToastData | null>(null);
  const trigger = useRef<HTMLButtonElement>(null), width = useShellWidth(), phone = width < 1024;
  // music-plus: Start radio's admission lease belongs to this exact menu for this binding and
  // page, owned by a layout effect so a replayed setup gets a fresh, live lease.
  type RadioLease = Readonly<{signal: AbortSignal; seedId: string}>;
  const radioActive = useRef<RadioLease | undefined>(undefined);
  const [radioLease, setRadioLease] = useState<RadioLease>();
  useLayoutEffect(() => {
    const life = new AbortController(), lease: RadioLease = Object.freeze({signal: life.signal, seedId: data.item.id});
    radioActive.current = lease;
    setRadioLease(lease);
    // Synchronous retirement: this lease ends here, and a successor is untouched.
    return () => {life.abort();if (radioActive.current === lease) radioActive.current = undefined;setRadioLease(current => current === lease ? undefined : current);};
  }, [binding, data]);
  const versionRead = useRef<AbortController | undefined>(undefined), versionChoice = useRef<AbortController | undefined>(undefined);
  const [versions, setVersions] = useState<Readonly<{data: DetailProjection; options: PlaybackOptions; request: AbortController}>>();
  const downloadAvailable = !!binding.context.effects.download && data.item.available && ['movie', 'episode', 'song', 'audiobook_file'].includes(data.item.kind)
    && detailActions(data, {platform: 'web'}).more.some(group => group.actions.some(action => action.id === 'download'));
  type ActiveDownload = Readonly<{target: BrowserDownloadTarget; data: DetailProjection; request: AbortController; current: () => boolean; port: NonNullable<typeof binding.context.effects.download>}>;
  const downloadRef = useRef<ActiveDownload | undefined>(undefined);
  const [downloadView, setDownloadView] = useState<Readonly<{active: ActiveDownload; options?: DownloadOptionsView; busy: boolean; failed: boolean}>>();
  const closeDownload = () => { downloadRef.current?.request.abort(); downloadRef.current = undefined; setDownloadView(undefined); };
  const loadDownload = async (active: ActiveDownload) => {
    if (!active.current()) return;
    setDownloadView({active, busy: true, failed: false});
    try {
      const options = await active.port.options(active.target, active.request.signal, active.current);
      if (active.current()) setDownloadView({active, options, busy: false, failed: false});
    } catch (error) {
      if (active.current()) { setDownloadView({active, busy: false, failed: true}); binding.context.effects.reportError(error); }
    }
  };
  const openDownload = () => {
    binding.current(data);
    const port = binding.context.effects.download;
    if (!port || !downloadAvailable) return;
    closeDownload();
    const target: BrowserDownloadTarget = Object.freeze({libraryId: data.scope.libraryId, itemId: data.item.id}), request = new AbortController();
    const active: ActiveDownload = {target, data, request, port, current: () => !request.signal.aborted && binding.context.isCurrent()
      && downloadRef.current === active && binding.service.getSnapshot().phase === 'ready' && binding.service.getSnapshot().data === data};
    downloadRef.current = active;
    void loadDownload(active);
  };
  const saveDownload = async (active: ActiveDownload, quality: string) => {
    if (!active.current() || downloadView?.busy || !downloadView?.options?.policy.allowDownloads
      || !downloadView.options.options.some(option => option.quality === quality && option.available)) return;
    setDownloadView(previous => previous?.active === active ? {...previous, busy: true} : previous);
    try {
      await active.port.save(active.target, quality, active.request.signal, active.current);
      // The promise acknowledges browser handoff, never a completed local transfer.
      if (active.current()) closeDownload();
    } catch (error) {
      if (active.current()) { setDownloadView(previous => previous?.active === active ? {...previous, busy: false} : previous); binding.context.effects.reportError(error); }
    }
  };
  useEffect(() => { setDownloadView(undefined); return () => { downloadRef.current?.request.abort(); downloadRef.current = undefined; }; }, [data]);

  useEffect(() => { setOpen(false); setFileOpen(false); setTitlesOpen(false); setManage(undefined); }, [data]);
  useEffect(() => () => {versionRead.current?.abort();versionChoice.current?.abort();}, [data]);
  useEffect(() => {
    if (!open || !binding.context.effects.chooseVersion || !data.actions.some(action => action.enabled && action.playback?.itemId === data.item.id && action.id !== 'start_over')) return;
    const request = new AbortController();versionRead.current = request;setVersions(undefined);
    void binding.readVersions(data, request.signal).then(options => {if (!request.signal.aborted) setVersions({data, options, request});}, error => {
      if (!request.signal.aborted) binding.context.effects.reportError(error);
    });
    return () => {if (versionChoice.current !== request) request.abort();};
  }, [binding, data, open]);
  const chooseVersion = () => {
    if (!versions || versions.data !== data || versions.request.signal.aborted) return;
    versionChoice.current?.abort();versionChoice.current = versions.request;
    void binding.chooseVersion(data, versions.options, versions.request.signal, trigger).catch(binding.context.effects.reportError).finally(() => {
      versions.request.abort();if (versionChoice.current === versions.request) versionChoice.current = undefined;
    });
  };
  const loadFiles = () => { setTitlesOpen(false); setFileOpen(true); };
  const loadTitles = () => { setFileOpen(false); setTitlesOpen(true); };
  const viewer = binding.context.identity?.viewer, owner = viewer?.role === 'owner' && viewer.authority === 'local';
  const offered = (id: string) => data.actions.some(action => action.id === id && action.enabled);
  const refreshPath = owner ? refreshMetadataPath(data.item.kind, data.item.id) : undefined;
  const refresh = () => {
    if (!refreshPath) return;
    void binding.context.api.request(refreshPath, 'POST', {}).then(() => binding.service.revalidate()).catch(binding.context.effects.reportError);
  };
  const differentVersion = versions?.data === data && !versions.request.signal.aborted && versions.options.versions.length > 1;
  const personal = (phone && offered('watched')) || offered('favorite') || offered('rating');
  const kind = data.item.kind;
  const entryTarget: EntryActionTarget | undefined = kind === 'movie' || kind === 'episode' || kind === 'song' || kind === 'audiobook_file'
    ? {libraryId: data.scope.libraryId, itemId: data.item.id, kind, title: data.item.title} : undefined;
  const leaf = !!entryTarget;
  const canEditMetadata = leaf && owner && offered('metadata_edit') && !!binding.context.effects.editMetadata;
  const canDelete = leaf && owner && offered('delete') && !!binding.context.effects.deleteTitle;
  const canAddToPlaylist = leaf && offered('add_to_playlist') && !!binding.context.effects.addToPlaylist;
  const canAddToCollection = leaf && offered('add_to_collection') && !!binding.context.effects.addToCollection;
  const everyoneRows = personal || canAddToPlaylist || canAddToCollection || downloadAvailable;
  const radioSeed = kind === 'song' || kind === 'album' ? Object.freeze({id: data.item.id, title: data.item.title}) : undefined;
  const ownerRows = !!refreshPath || canEditMetadata || canDelete;
  const sheet = width < 600;
  const destination = (effect: typeof binding.context.effects.addToPlaylist) => runScreenCommand(binding.context, () => {
    binding.current(data); if (entryTarget && effect && trigger.current) return effect(entryTarget, trigger.current);
  });
  return <>
    <Menu open={open} onOpenChange={setOpen} title={data.item.title} width={280} trigger={<Button ref={trigger} icon="i-more" iconOnly aria-label={t('next.controls.more')} className={open ? styles.moreOpen : undefined} />}>
      {/* The drawn menu (desktop-detail 1 and 2, phone-detail 9): everyone's rows keep the frame's
          order — personal first, then playlist, collection and Download — before the version and
          File info block and the owner's own section, with Delete last and destructive. The sheet
          draws each block as its own card, so its separators are the gaps the frame shows. */}
      {radioSeed && radioLease && radioLease.seedId === data.item.id && <MenuGroup><StartRadioMenuItem context={binding.context} seed={radioSeed} signal={radioLease.signal}
        current={() => radioActive.current === radioLease && !radioLease.signal.aborted && binding.context.isCurrent() && binding.service.getSnapshot().phase === 'ready' && binding.service.getSnapshot().data === data} /></MenuGroup>}
      {everyoneRows && <MenuGroup>
        {phone && offered('watched') && <MenuItem icon="i-check" selected={data.personal.watched} onSelect={() => { void binding.mutate(data, {action: 'watched', value: !data.personal.watched}).catch(binding.context.effects.reportError); }}>{t(data.personal.watched ? 'title.markUnwatched' : 'title.markWatched')}</MenuItem>}
        {offered('favorite') && <MenuItem icon="i-heart" selected={data.personal.favorite} onSelect={() => { void binding.mutate(data, {action: 'favorite', value: !data.personal.favorite}).catch(binding.context.effects.reportError); }}>{t(data.personal.favorite ? 'entry.removeFromFavorites' : 'entry.addToFavorites')}</MenuItem>}
        {offered('rating') && <MenuItem icon="i-star" onSelect={() => setRating(true)}>{t('entry.rate')}</MenuItem>}
        {canAddToPlaylist && <MenuItem icon="i-listadd" onSelect={() => destination(binding.context.effects.addToPlaylist)}>{t('entry.addToPlaylist')}</MenuItem>}
        {canAddToCollection && <MenuItem icon="i-layers" onSelect={() => destination(binding.context.effects.addToCollection)}>{t('entry.addToCollection')}</MenuItem>}
        {downloadAvailable && <MenuItem icon="i-download" onSelect={() => runScreenCommand(binding.context, openDownload)}>{t('next.detail.downloadAction')}</MenuItem>}
      </MenuGroup>}
      {everyoneRows && !sheet && <MenuSeparator />}
      <MenuGroup>
        {differentVersion && <MenuItem icon="i-film" onSelect={chooseVersion}>{t('next.overlays.differentVersion')}</MenuItem>}
        <MenuItem icon="i-info" onSelect={loadFiles}>{t('next.overlays.fileInfo')}</MenuItem>
        <MenuItem icon="i-stack" onSelect={loadTitles}>{t('next.overlays.discTitles')}</MenuItem>
        {/* O-161: the mock-ups have no share slot; the title's ⋯ menu carries its shareable link. */}
        <MenuItem icon="i-copy" onSelect={() => { void copyLink('item', {itemId: data.item.id}, trigger.current ?? undefined).then(() => setLinkToast({id: crypto.randomUUID(), icon: 'i-check', message: t('next.overlays.linkCopied')}), () => setLinkToast({id: crypto.randomUUID(), icon: 'i-warn', tone: 'warning', message: t('next.overlays.copyLinkFailed')})); }}>{t('next.overlays.copyLink')}</MenuItem>
      </MenuGroup>
      {ownerRows && <MenuGroup label={t(sheet ? 'next.overlays.ownerOnly' : 'next.overlays.owner')} ownerOnly>{canEditMetadata && <MenuItem onSelect={() => destination(binding.context.effects.editMetadata)}>{t('entry.editMetadata')}</MenuItem>}{refreshPath && <MenuItem icon="i-refresh" onSelect={refresh}>{t('entry.refreshMetadata')}</MenuItem>}{canDelete && <MenuItem icon="i-trash" destructive onSelect={() => destination(binding.context.effects.deleteTitle)}>{t('entry.delete')}</MenuItem>}</MenuGroup>}
    </Menu>
    {downloadView && downloadView.active.data === data && (downloadView.options?.policy.allowDownloads
      ? <DownloadNotice open onOpenChange={value => { if (!value) closeDownload(); }} title={data.item.title} artwork={binding.context.artwork.foreground(data.item.posterUrl, 88, 2 / 3)} options={downloadView.options} busy={downloadView.busy}
          onChoose={quality => { void saveDownload(downloadView.active, quality); }} returnFocusRef={trigger} />
      : <Dialog open onOpenChange={value => { if (!value) closeDownload(); }} title={t('next.detail.download')} description={data.item.title} returnFocusRef={trigger}>
          {downloadView.options ? <StateView title={t('web.downloads.offTitle')} body={t('web.downloads.offMember')} />
            : downloadView.failed ? <ScreenFailure retry={() => { void loadDownload(downloadView.active); }} /> : <DetailLoading shape="panel" />}
        </Dialog>)}
    <ToastHost toast={linkToast} onDismiss={() => setLinkToast(null)} />
    {rating && <TitleRating binding={binding} data={data} returnFocusRef={trigger} onClose={() => setRating(false)} />}
    {(fileOpen || titlesOpen) && <TitleFileInfo binding={binding} data={data} surface={fileOpen ? 'file' : 'titles'} phone={phone} returnFocusRef={trigger}
      onClose={() => { setFileOpen(false); setTitlesOpen(false); }} onManage={context => runScreenCommand(binding.context, () => { setFileOpen(false); setManage(context); })} />}
    {manage && <FileManagement binding={binding} data={data} manage={manage} onClose={() => setManage(undefined)} returnFocusRef={trigger} />}
  </>;
}

export function TitleDetails({binding, data}: {binding: DetailBindings; data: DetailProjection}) {
  const phone = useShellWidth() < 1024;
  const {item, facts, metadata} = data, collection = data.collections?.[0], {artwork} = binding.context;
  const language = (code: string) => { try { return new Intl.DisplayNames(locale(), {type: 'language'}).of(code) ?? code; } catch { return code; } };
  const number = (value: number) => new Intl.NumberFormat(locale(), {maximumFractionDigits: 1}).format(value);
  const directors = metadata.credits.filter(credit => credit.role.toLowerCase() === 'director'), writers = metadata.credits.filter(credit => /writer|writing|screenplay|teleplay|novel/i.test(credit.role));
  const ratings = metadata.ratings.map(rating => `${rating.provider} ${number(rating.value)}`).join(', ');
  // The phone hero already keeps year, runtime and the content rating in view, so its
  // Details avoids repeating them and puts the drawn credit lines under the people.
  const rows: (readonly [string, ReactNode])[] = [
    [t('title.releaseDate'), calendarDate(facts?.releaseDate, locale())],
    ...(!phone ? [[t('library.column.runtime'), titleFactParts({durationSeconds: item.duration}, env).find(part => part.id === 'duration')?.text] as const] : []),
    [t('title.genres'), metadata.genres.length ? <GenreNames names={metadata.genres.map(value => value.name)} onOpen={binding.context.effects.openBrowse ? genre => runScreenCommand(binding.context, () => binding.browseGenre(data, genre)) : undefined} /> : undefined],
    ...(!phone ? [[t('next.show.rated'), facts?.contentRating] as const] : []),
    [t('title.ratings'), ratings || undefined],
    ...(!phone ? [
      [t('title.directedByLabel'), directors.length ? <CreditNames binding={binding} data={data} credits={directors} /> : undefined] as const,
      [t('title.writtenByLabel'), writers.length ? <CreditNames binding={binding} data={data} credits={writers} /> : undefined] as const,
    ] : []),
    [t('title.studio'), facts?.studio], [t('title.network'), facts?.network], [t('title.country'), facts?.country], [t('title.edition'), facts?.edition],
    ...(!phone ? [
      [t('next.selection.language'), facts?.originalLanguage ? language(facts.originalLanguage) : undefined] as const,
      [t('title.audio'), data.languages.audio.map(language).join(', ')] as const, [t('title.subtitles'), data.languages.subtitles.map(language).join(', ')] as const,
    ] : []),
    [t('title.added'), instantDate(item.addedAt, locale())],
  ];
  return <div className={styles.details}><section><h2 className={styles.sectionHeading}>{t('next.show.about')}</h2><dl>{rows.filter(row => !!row[1]).map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{value}</dd></div>)}</dl></section>
    {metadata.credits.length > 0 && <TitlePeople binding={binding} data={data} />}
    {phone && (directors.length > 0 || writers.length > 0) && <div className={styles.phoneCreditLines}>
      {directors.length > 0 && <div><CreditLine message="title.directedBy" credits={directors} binding={binding} data={data} /></div>}
      {writers.length > 0 && <div><CreditLine message="title.writtenBy" credits={writers} binding={binding} data={data} /></div>}
    </div>}
    {collection && (phone
      ? <section className={styles.phoneCollectionWrap}>
        <button type="button" className={styles.phoneCollection} aria-label={`${collection.name} ${t('next.detail.collection')}`} onClick={() => openCollection(binding, data, collection)}>
          {collection.posterUrls[0] && <span className={styles.phoneCollectionGlow} aria-hidden="true"><Artwork source={artwork.foreground(collection.posterUrls[0], 780, 16 / 9)} alt="" /></span>}
          {collection.posterUrls.length > 0 && <span className={styles.phoneCollectionFan} aria-hidden="true">{collection.posterUrls.slice(0, 2).map((poster, index) => <span key={`${poster}:${index}`}><Artwork source={artwork.foreground(poster, 96, 2 / 3)} alt="" /></span>)}</span>}
          <span className={styles.phoneCollectionCopy}><i>{t('next.detail.collection')}</i><b>{collection.name}</b><small>{t('lib.count', {count: collection.memberCount, pivot: 'other'})}</small></span>
          <Icon name="i-right" size={18} />
        </button>
      </section>
      : <section className={styles.detailBanner} aria-label={t('next.detail.partOfCollection')}>
        <CollectionBanner context="detail" model={{title: collection.name, caption: []}} kindLabel={t('next.detail.partOfCollection')}
          description={collection.yearFrom && collection.yearTo
            ? t('next.detail.collectionSummary', {count: collection.memberCount, from: collection.yearFrom, to: collection.yearTo})
            : t('lib.count', {count: collection.memberCount, pivot: 'other'})}
          actionLabel={t('next.home.openCollection')} actionVariant="glass" position="50% 38%"
          artwork={artwork.foreground(collection.posterUrls[0], 1312 * devicePixelRatio, 16 / 9)}
          covers={collection.posterUrls.slice(0, 4).flatMap(poster => { const art = artwork.foreground(poster, 64 * devicePixelRatio, 2 / 3); return art ? [art] : []; })}
          onAction={() => openCollection(binding, data, collection)} onNavigate={() => openCollection(binding, data, collection)} />
      </section>)}
  </div>;
}

/** Opens the collection the title belongs to: the drawn hero link and the Details banner share it. */
function openCollection(binding: DetailBindings, data: DetailProjection, collection: NonNullable<DetailProjection['collections']>[number]) {
  return runScreenCommand(binding.context, () => {
    binding.current(data);
    binding.context.effects.openEntry({id: collection.id, libraryId: data.scope.libraryId, kind: 'collection', title: collection.name, navigation: {view: 'collection', entityId: collection.id}});
  });
}

/** A title's or show's recommendation row, read to its end through the owner's row route. */
export function TitleRecommendationRow({row, owner, binding, guard, subtitleVisible = true}: {row: ItemRecommendations['rows'][number]; owner: RelationRowOwner; binding: Pick<DetailBindings, 'context'>; guard?: () => void; subtitleVisible?: boolean}) {
  const i18n = serviceI18n();
  return <PagedTitleRow heading={serverTextLabel(i18n, row.titleText, row.title)} subtitle={subtitleVisible ? serverTextLabel(i18n, row.subtitleText) : undefined} entries={row.entries} binding={binding} guard={guard}
    first={row} pager={api => recommendationRowPager(api, owner, row)} />;
}

/** One of a title's related rows (detail.related), read to its end under the detail's fence. */
function RelatedTitleRow({row, data, binding, guard}: {row: RelatedMovieRow; data: DetailProjection; binding: Pick<DetailBindings, 'context'>; guard?: () => void}) {
  return <PagedTitleRow heading={row.heading} entries={row.entries} binding={binding} guard={guard}
    first={row} pager={api => relatedRowPager(api, {libraryId: data.item.libraryId, itemId: data.item.id, kind: data.item.kind}, row, {viewerFence: data.scope.viewerFence, revision: data.revision})} />;
}

/** A listening title's More like this, read to its end on its first page's basis. */
export function SimilarTitleRow({first, scope, heading, subtitle, binding, guard}: {first: ListeningSimilarRead; scope: ListeningViewerScope; heading: ReactNode; subtitle?: ReactNode; binding: Pick<DetailBindings, 'context'>; guard?: () => void}) {
  return <PagedTitleRow heading={heading} subtitle={subtitle} entries={first.entries} binding={binding} guard={guard} first={first} pager={api => listeningSimilarPager(api, scope, first)} />;
}

/** A TitleRow whose later pages come from its first page's pager (useRelationRow). */
function PagedTitleRow({first, pager, entries, ...row}: Omit<Parameters<typeof TitleRow>[0], 'pagination'> & {first: object; pager: Parameters<typeof useRelationRow>[3]}) {
  const paged = useRelationRow(row.binding.context, first, entries, pager);
  return <TitleRow {...row} entries={paged.entries} pagination={paged.pagination} />;
}

export function TitleRow({heading, subtitle, entries, binding, guard, pagination}: {heading: ReactNode; subtitle?: ReactNode; entries: readonly ContentEntry[]; binding: Pick<DetailBindings, 'context'>; guard?: () => void; pagination?: Readonly<{cursor: string; loading: boolean; onMore: () => void}>}) {
  const width = useShellWidth(), phone = width < 1024;
  const props = {focusScope: binding.context, heading, subtitle: phone ? undefined : subtitle, items: entries, getKey: (entry: ContentEntry) => entry.id, renderItem: (entry: ContentEntry) => {
    const props = {model: entryCard(entry, env), artwork: binding.context.artwork.foreground(entry.posterUrl, 380, 2 / 3), onNavigate: () => runScreenCommand(binding.context, () => { if (binding.context.isCurrent()) { guard?.(); binding.context.effects.openEntry(entry); } }), watched: entry.watched};
    return ['album', 'book'].includes(entry.kind) ? <SquareCard {...props} /> : <PosterCard {...props} />;
  }};
  const square = entries.every(entry => ['album', 'book'].includes(entry.kind));
  return pagination ? <PagedShelf {...props} hasMore={!!pagination.cursor} continuation={pagination.cursor} loading={pagination.loading} onMore={pagination.onMore} cardWidth={width < 600 ? square ? 132 : 118 : 190} artworkRatio={square ? 1 : 2 / 3} /> : <Shelf {...props} />;
}
export function ScreenFailure({retry}: {retry: () => void}) { return <StateView variant="page" title={t('next.states.pageFailed')} body={t('next.states.pageFailedBody')} primary={{id: 'retry', label: t('next.states.tryAgain'), onPress: retry}} />; }
