import {useReadingFavorite} from './useReadingFavorite';
import {useReadSaved} from './ReadSavedAction';
import {useReadMark} from './ReadMarkAction';
import {cloneElement, useCallback, useEffect, useLayoutEffect, useRef, useState, type ReactElement, type Ref} from 'react';
import {useLocation} from '@tanstack/react-router';
import {defaultI18n} from '@i18n';
import {generatedCoverTint} from '@design';
import {readingDuration} from '@core/reading-manifest-facts.ts';
import {createReadingPublication, type ReaderContentsTarget} from '@portico/reader-core';
import {stageContentsTarget, retireContentsTarget} from './contents-handoff';
import {ReaderContents, type ReaderContentsEntry} from '../../kit/reader/ReaderPanel';
import type {ReadingItem, ReadingManifest, ReadingProgress, ReadingSession} from '@portico/client-core';
import {rememberFilterNames} from '@core/filter-value-names.ts';
import {saveReadingFile} from './save-file';
import {loadReadFonts} from './fonts';
import {useProtectedWebScreenContext} from '../../route-registry-view';
import {router} from '../../router';
import {Icon} from '../../kit/Icon';
import {Button} from '../../kit/controls';
import {Dialog, Menu, MenuGroup, MenuItem} from '../../kit/overlays';
import {usePhone} from '../../kit/overlays/shared';
import {runScreenCommand} from '../detail/context';
import {Skeleton} from '../../kit/states';
import {ReadBookFacts, ReadMarksStrip} from '../../kit/reader/ReadBookHero';
import {Shelf} from '../../kit/layout';
import {CoverCard} from '../../kit/cards';
import {CoverHeroLink} from '../../kit/title/CoverHero';
import {TitleFactsLine} from '../../kit/title/TitleParts';
import {AboutTable, PanelBlock, PanelHeading, TitleFrame, type AboutValue, type TitleAction} from '../../kit/frames';
import {GeneratedCover, WholeArtwork} from '../../kit/artwork';
import {useWindowWidth} from '../../kit/theme';
import {ServerMark} from '../../kit/shell';
import {useLibraryScreenContext} from '../browse/screen-context';
import {ReadMetadata} from './ReadMetadata';
import {createReadBindings} from './bindings';
import {readChosenDestination, readDestination, type ReadItemEligibility} from './navigation';
import {authorPredicate} from '@core/presentation/library-tabs.ts';
import {readBackCommit, readRetireAttempt, readRouteEntry, readVersionCanChoose, readVersionRecovery, type ReadRouteEntry, type ReadVersion, type ReadVersionSource} from './read-version-choice';
import {ReadOpenFailure, readFailureKind, readFailureReported, type ReadFailureKind} from './ReadOpenFailure';
import styles from './read-item.module.css';

type Captured = NonNullable<ReturnType<typeof useProtectedWebScreenContext>>;
type ReadAttempt = {readonly context: Captured['context']; readonly entry: typeof router.state.location; readonly sourceId: string | undefined; readonly retry: number; readonly fileLife: AbortController; session?: ReadingSession; saving?: boolean; live: boolean; ready: boolean; target?: (index: number) => ReaderContentsTarget};
const {t} = defaultI18n;
/** The primary Item registration supplies a genuine Reading resource result. */
export function ReadItemEntry({captured: provided, handoff, itemId, eligibility}: {captured?: Captured; handoff?: Captured['context']; itemId: string; eligibility: ReadItemEligibility}) {
  const original = useProtectedWebScreenContext(), captured = provided ?? original;
  return captured && eligibility.itemId === itemId ? <BoundReadItem key={itemId} captured={captured} handoff={handoff ?? captured.context} eligibility={eligibility}/> : null;
}
function BoundReadItem({captured, handoff, eligibility}: {captured: Captured; handoff: Captured['context']; eligibility: ReadItemEligibility}) {
  const context = useLibraryScreenContext(captured);
  const phone = usePhone();
  // The entry this render was produced on, committed by layout. A reused component with
  // a new __TSR_key renders new callbacks; callbacks from the old entry refuse.
  const routeLocation = useLocation();
  const routeEntry = readRouteEntry(routeLocation);
  const committedEntry = useRef<ReadRouteEntry | undefined>(undefined);
  useLayoutEffect(() => {committedEntry.current = readRouteEntry(routeLocation);}, [routeLocation]);
  const [editing, setEditing] = useState(false), [more, setMore] = useState(false);
  const editorAnchor = useRef<HTMLButtonElement>(null);
  const errorSink = useRef(captured.binding.home.effects.reportError); errorSink.current = captured.binding.home.effects.reportError;
  const [sourceId, setSourceId] = useState(() => eligibility.versions.find(version => version.inventoryAvailable && version.sourceId === eligibility.chosenSourceId)?.sourceId);
  const [menu, setMenu] = useState(false), [fileInfo, setFileInfo] = useState(false), [retry, setRetry] = useState(0);
  const [saving, setSaving] = useState(false);
  const [attempt, setAttempt] = useState<ReadAttempt>();
  const attemptRef = useRef<ReadAttempt | undefined>(undefined);
  const committed = useRef<ReadVersionSource | undefined>(undefined);
  useLayoutEffect(() => {committed.current = {context: captured.context, sourceId, versions: eligibility.versions};}, [captured.context, sourceId, eligibility.versions]);
  /** What the page adds once the book is open: the reader's own marks and the author's other titles. */
  const [extras, setExtras] = useState<Readonly<{attempt?: ReadAttempt; marks?: Readonly<{highlights: number; bookmarks: number}>; moreBy?: readonly ReadingItem[]}>>({});
  const [detail, setDetail] = useState<Readonly<{attempt?: ReadAttempt; manifest?: ReadingManifest; progress?: ReadingProgress; client?: ReturnType<typeof createReadBindings>['client']; targets?: readonly ReaderContentsTarget[]; failed?: boolean; failure?: ReadFailureKind}>>({});
  const current = useCallback((value: ReadAttempt | undefined): value is ReadAttempt => {
    if (!value) return false;
    const exact = () => value.live && attemptRef.current === value && value.entry === router.state.location
      && value.context === captured.context && value.sourceId === sourceId && value.retry === retry;
    // The captured context may reenter; it cannot revive a retired/replaced local attempt.
    return exact() && value.context.isCurrent() && exact();
  }, [captured.context, sourceId, retry]);
  const retire = (value: ReadAttempt) => readRetireAttempt(value,
    () => {if (attemptRef.current === value) attemptRef.current = undefined;}, () => value.fileLife.abort());
  const refresh = (value: ReadAttempt | undefined) => {if (!current(value)) return; retire(value); setRetry(count => count + 1);};
  const favorite = useReadingFavorite({
    client: current(detail.attempt) && detail.attempt.ready && detail.manifest?.sourceId === sourceId ? detail.client : undefined,
    itemId: eligibility.itemId,
    isCurrent: () => current(detail.attempt) && detail.attempt.ready && detail.manifest?.sourceId === sourceId,
    reportError: error => errorSink.current(error),
  });
  const actionsLive = () => current(detail.attempt) && detail.attempt === attemptRef.current && detail.attempt.ready && detail.manifest?.sourceId === sourceId;
  const actionsClient = current(detail.attempt) && detail.attempt.ready && detail.manifest?.sourceId === sourceId ? detail.client : undefined;
  const myList = useReadSaved({client: actionsClient, itemId: eligibility.itemId, isCurrent: actionsLive, reportError: error => errorSink.current(error)});
  const readMark = useReadMark({client: actionsClient, itemId: eligibility.itemId, isCurrent: actionsLive, reportError: error => errorSink.current(error)});
  const width = useWindowWidth(), [section, setSection] = useState('contents');
  useLayoutEffect(() => {
    const value: ReadAttempt = {context: captured.context, entry: routeLocation, sourceId, retry, fileLife: new AbortController(), live: true, ready: false};
    attemptRef.current = value; setAttempt(value); setSaving(false); setMore(false); setMenu(false); setFileInfo(false);
    return () => retire(value);
  }, [captured.context, eligibility.itemId, routeLocation, sourceId, retry]);
  const chooserAnchor = useRef<HTMLButtonElement>(null);
  const [chooserMenu, setChooserMenu] = useState(false);
  useEffect(() => {
    const original = attempt, originalCurrent = current;
    if (!originalCurrent(original)) return;
    const bindings = createReadBindings(original.context);
    let active = true;
    const live = () => active && originalCurrent(original) && bindings.authority.isCurrent();
    let publication: Awaited<ReturnType<typeof createReadingPublication>> | undefined;
    const off = bindings.authority.subscribe(() => {if (active && attemptRef.current === original) {original.fileLife.abort(); original.ready = false; original.target = undefined; setDetail({attempt: original, failed: true});}});
    const requestedSource = original.sourceId;
    if (!requestedSource) setDetail({attempt: original, failed: true});
    else void (async () => {
      const session = await bindings.open({itemId: eligibility.itemId, sourceId: requestedSource});
      if (!live()) return;
      const progress = await session.progress();
      if (!live()) return;
      publication = await createReadingPublication(session);
      if (!live()) {publication.dispose(); return;}
      original.ready = true;
      original.session = session;
      original.target = index => {if (!live() || !original.ready) throw new Error('source_changed'); return publication!.contentsTarget(index);};
      setDetail({attempt: original, manifest: session.manifest, progress, client: bindings.client,
        targets: session.manifest.format === 'epub' ? (session.manifest.contents ?? []).map((_, index) => publication!.contentsTarget(index)) : []});
      // Both are extras: a failure leaves the page as it is, with no strip and no row.
      const add = (value: NonNullable<typeof extras>) => {if (live()) setExtras(before => ({...(before.attempt === original ? before : {}), ...value, attempt: original}));};
      void countMarks(session, original.fileLife.signal).then(marks => add({marks}), () => {});
      const {libraryId, authorId} = session.manifest.metadata;
      if (libraryId && authorId) void bindings.client.byAuthor(libraryId, authorId, {limit: 13}, original.fileLife.signal)
        .then(items => add({moreBy: items.filter(item => item.id !== eligibility.itemId).slice(0, 12)}), () => {});
    })().catch(error => {if (live()) {const failure = readFailureKind(error); if (readFailureReported(failure)) errorSink.current(error); original.ready = false; original.target = undefined; setDetail({attempt: original, failed: true, failure});} void bindings.close().catch(() => {});});
    return () => {active = false; original.ready = false; original.target = undefined; publication?.dispose(); off(); void bindings.dispose().catch(() => {});};
  }, [attempt, eligibility.itemId, current]);
  /** The first lines are set in the reader's serif (desktop-read 3), which the app loads only for
   * a book; the page loads it while it shows them, and lets it go when it leaves. */
  const showsOpening = !!(detail.attempt === attempt && detail.manifest?.metadata.openingLines);
  useEffect(() => {
    if (!showsOpening) return;
    const life = new AbortController();
    void loadReadFonts({document, signal: life.signal}).catch(() => {});
    return () => life.abort();
  }, [showsOpening]);
  const goBack = () => runScreenCommand(context, () => {
    if (!context.isCurrent()) return;
    // The callback's own entry must still be committed and live; then choose Back or Home.
    const destination = readBackCommit(routeEntry, committedEntry.current, readRouteEntry(router.state.location), router.history.canGoBack());
    if (destination === 'back') context.effects.back?.();
    else if (destination === 'home') context.effects.openHome?.();
  });
  // The reading branch owns no shell navigation on a phone, so the page keeps one
  // Back of its own before loading, failure and ready content alike.
  const phoneBack = phone && context.effects.back ? <div className={styles.phoneTopBar} data-read-phone-back>
    <Button className={styles.phoneBack} size="small" variant="icon" icon="i-left" iconOnly aria-label={t('next.states.back')}
      onClick={goBack}/>
  </div> : null;
  const version = eligibility.versions.find(candidate => candidate.sourceId === sourceId);
  /** An explicit version recovery is permitted while the original attempt is failed or not
   * ready; factsCurrent() is deliberately not the gate because it can never become true then.
   * Pre-retire admission requires the original route/context/source/list and the real live
   * attempt; post-retire admission requires only that unchanged witness and no successor,
   * because retirement necessarily clears the attempt's liveness and ref. */
  const pickVersion = (candidate: ReadVersion, renderedAttempt: ReadAttempt | undefined, renderedSourceId: string | undefined) => {
    if (!renderedAttempt) return;
    const admitted = readVersionRecovery({
      liveRoute: context.isCurrent(),
      route: routeEntry,
      committedRoute: committedEntry.current,
      currentRoute: readRouteEntry(router.state.location),
      rendered: {context: captured.context, sourceId: renderedSourceId, versions: eligibility.versions, attempt: renderedAttempt},
      committed: committed.current,
      currentAttempt: attemptRef.current,
      attemptLive: renderedAttempt.live && !renderedAttempt.fileLife.signal.aborted,
      candidate,
      retire: attempt => {if (attempt === renderedAttempt) retire(renderedAttempt);},
      afterRetire: () => ({liveRoute: context.isCurrent(), committedRoute: committedEntry.current,
        currentRoute: readRouteEntry(router.state.location), successor: attemptRef.current, committed: committed.current}),
    });
    if (!admitted) return;
    setDetail({});
    setSaving(false);
    setMenu(false); setChooserMenu(false);
    setSourceId(candidate.sourceId);
  };
  const versionItems = eligibility.versions.map(candidate => <MenuItem key={candidate.sourceId} selected={candidate.sourceId === sourceId} disabled={!candidate.inventoryAvailable}
    onSelect={() => pickVersion(candidate, attempt, sourceId)} description={defaultI18n.bytes(candidate.size)}>{candidate.format.toUpperCase()}</MenuItem>);
  const versionChooser = readVersionCanChoose(eligibility.versions) ? <div className={styles.versionRow}>
    <Button ref={chooserAnchor} size="small" variant="glass" onClick={() => setChooserMenu(value => !value)} aria-label={t('next.reader.version')}>
      {version?.format.toUpperCase() ?? t('next.reader.version')}<Icon name="i-down" size={14}/></Button>
    <Menu open={chooserMenu} onOpenChange={setChooserMenu} title={t('next.reader.version')} anchorRef={chooserAnchor}>
      {versionItems}
    </Menu>
  </div> : null;
  if (current(attempt) && detail.attempt === attempt && detail.failed) return <>
    {phoneBack}
    <ReadOpenFailure kind={detail.failure ?? 'failed'} onRetry={() => refresh(attempt)}/>
    {versionChooser}
  </>;
  const manifest = current(attempt) && detail.attempt === attempt ? detail.manifest : undefined;
  if (!manifest || !version || manifest.sourceId !== sourceId) return <>{phoneBack}<Skeleton quiet height={480}/>
    {versionChooser && <div className={styles.versionRow}>
      <Button ref={chooserAnchor} size="small" variant="glass" icon="i-more" iconOnly aria-label={t('next.controls.more')} onClick={() => setChooserMenu(value => !value)}/>
      <Menu open={chooserMenu} onOpenChange={setChooserMenu} title={t('next.reader.version')} anchorRef={chooserAnchor}>{versionItems}</Menu>
    </div>}</>;
  const title = manifest.metadata.title ?? '', author = manifest.metadata.author;
  const metadata = manifest.metadata, series = metadata.series;
  const original = detail.attempt;
  const factsCurrent = () => current(original) && original === attempt && original.ready && detail.manifest === manifest;
  const canSaveFile = manifest.canSaveFile === true;
  const saveFile = async () => {
    const session = original?.session;
    if (!original || !session || original.saving || original.fileLife.signal.aborted || !factsCurrent()
      || session.manifest !== manifest || !canSaveFile) return;
    original.saving = true; setSaving(true);
    const live = () => factsCurrent() && original.session === session && !original.fileLife.signal.aborted;
    try {await saveReadingFile(session, original.fileLife.signal, live);}
    catch (error) {if (live()) errorSink.current(error);}
    finally {original.saving = false; if (current(original)) setSaving(false);}
  };
  const seriesPresentation = series ? {label: series.number === undefined ? series.name : t('next.reader.seriesPosition', {series: series.name, number: defaultI18n.number(series.number)}), onOpen: () => {
    if (!factsCurrent()) return;
    const publicationType = metadata.publicationType?.value;
    void router.navigate({to: '/library/$libraryId', params: {libraryId: series.libraryId}, search: {view: 'series', entityId: series.id,
      ...(publicationType === 'magazine' ? {pivot: 'magazines'} : publicationType === 'newspaper' ? {pivot: 'newspapers'} : {})}}).catch(error => {if (factsCurrent()) errorSink.current(error);});
  }} : undefined;
  const tint = generatedCoverTint(title);
  const libraryId = metadata.libraryId, authorId = metadata.authorId;
  /** The author's titles in this library: its grid filtered to them, as the Authors tab opens it. */
  const openAuthor = author && libraryId && authorId ? () => {
    if (!factsCurrent()) return;
    rememberFilterNames([{value: authorId, label: author}]);
    void router.navigate({to: '/library/$libraryId', params: {libraryId}, search: {view: 'browse', query: authorPredicate(authorId)}}).catch(error => {if (factsCurrent()) errorSink.current(error);});
  } : undefined;
  const added = extras.attempt === original ? extras : {};
  const marks = added.marks, marksText = !marks || !marks.highlights && !marks.bookmarks ? undefined
    : marks.highlights && marks.bookmarks ? t('next.reader.marksBoth', {highlights: t('next.reader.highlightCount', {count: marks.highlights}), bookmarks: t('next.reader.bookmarkCount', {count: marks.bookmarks})})
    : t('next.reader.marksOne', {marks: marks.highlights ? t('next.reader.highlightCount', {count: marks.highlights}) : t('next.reader.bookmarkCount', {count: marks.bookmarks})});
  const cardWidth = phone ? 108 : 190;
  const saved = detail.progress?.progress;
  const progression = saved && !saved.stale && saved.sourceId === manifest.sourceId && saved.sourceRevision === manifest.revision && saved.location.format === manifest.format ? saved.location.progression : undefined;
  const open = (index?: number) => {
    if (!factsCurrent()) return;
    let target: ReaderContentsTarget | undefined;
    try {target = index === undefined ? undefined : original?.target?.(index);}
    catch (error) {errorSink.current(error); return;}
    if (index !== undefined && !target) return;
    stageContentsTarget(handoff, eligibility.itemId, manifest.sourceId, target);
    void router.navigate(readDestination(eligibility.itemId, manifest.sourceId)).catch(error => {
      retireContentsTarget(handoff, target);
      if (factsCurrent()) errorSink.current(error);
    });
  };
  /** Manual selection is a recovery action, offered only from More or a failed read. */
  const chooseVersion = eligibility.versions.length > 1;
  const viewer = context.identity?.viewer, canEdit = viewer?.authority === 'local' && viewer.role === 'owner';
  // The book on the title frame (desktop-read 3, phone-read 3): the cover, the series as the kicker, the author leading the facts,
  // Read or Continue reading with its played part, My List, Mark as read and More; Contents is the panel and About this book the
  // side. Below 1024 the two are tabs (phone-read 3 frames 8 and 9).
  const about: Readonly<{label: string; values: readonly AboutValue[]}>[] = [
    ...(author ? [{label: t('filter.field.author'), values: [{text: author, ...(openAuthor ? {onPress: openAuthor} : {})}]}] : []),
    ...(manifest.metadata.language ? [{label: t('next.selection.language'), values: [{text: manifest.metadata.language}]}] : []),
    ...(metadata.wordCount !== undefined ? [{label: t('next.reader.wordsLabel'), values: [{text: defaultI18n.number(metadata.wordCount)}]}] : []),
    ...(manifest.format === 'cbz' && manifest.pageNames?.length ? [{label: t('next.reader.pagesStrip'), values: [{text: defaultI18n.number(manifest.pageNames.length)}]}] : []),
  ];
  const tabbed = width < 1024 && !!(detail.targets?.length || marksText) && about.length > 0;
  const aboutBlock = about.length ? <div><PanelHeading title={t('title.aboutThisBook')}/><AboutTable rows={about}/></div> : undefined;
  const contentsBlock = detail.targets?.length || marksText ? <PanelBlock label={t('next.reader.contents')}>
    {detail.targets?.length ? <>{!tabbed && <PanelHeading title={t('next.reader.contents')}/>}<ReaderContents entries={detailContents(manifest.contents ?? [], index => open(index))}/></> : null}
    {marksText && <ReadMarksStrip text={marksText} action={t('next.reader.openInReader')} onOpen={() => open()}/>}
  </PanelBlock> : undefined;
  const tab = section === 'details' ? 'details' : 'contents';
  const periodical = metadata.publicationType?.value === 'magazine' || metadata.publicationType?.value === 'newspaper';
  const actions: TitleAction[] = [
    ...(myList ? [{icon: myList.listed ? 'i-check' as const : 'i-plus' as const, label: myList.label, active: myList.listed, disabled: myList.busy, pinned: true, onPress: myList.press}] : []),
    ...(readMark ? [{icon: 'i-check' as const, label: readMark.label, active: readMark.read, disabled: readMark.busy, wide: true, onPress: readMark.press}] : []),
    {icon: 'i-more', label: t('next.controls.more'), onPress: () => {}, wrap: (button: ReactElement) => <>
      {cloneElement(button as ReactElement<{ref?: Ref<HTMLButtonElement>; onClick?: () => void}>, {ref: editorAnchor, onClick: () => {if (factsCurrent()) setMore(value => !value);}})}
      <Menu open={more} onOpenChange={setMore} title={title} anchorRef={editorAnchor}>
        {/* Below 1024 Mark as read is here, so Read keeps its label's room (phone-read 3). */}
        {readMark && width < 1024 && <MenuItem icon="i-check" selected={readMark.read} disabled={readMark.busy} onSelect={readMark.press}>{readMark.label}</MenuItem>}
        {favorite && <MenuItem icon="i-heart" selected={favorite.selected} disabled={favorite.busy} onSelect={favorite.onSelect}>{favorite.label}</MenuItem>}
        {canSaveFile && <MenuItem disabled={saving} onSelect={() => {void saveFile();}}>{t('next.reader.saveFile')}</MenuItem>}
        {chooseVersion && <MenuItem icon="i-book" onSelect={() => {if (factsCurrent()) {setMore(false); setMenu(true);}}}>{t('next.reader.readDifferentVersion')}</MenuItem>}
        <MenuItem icon="i-info" onSelect={() => {if (factsCurrent()) {setMore(false); setFileInfo(true);}}}>{t('next.overlays.fileInfo')}</MenuItem>
        {canEdit && <MenuGroup ownerOnly label={<span>{t('next.browse.server')}<ServerMark size={22}/></span>}><MenuItem onSelect={() => {if (context.isCurrent()) setEditing(true);}}>{t('entry.editMetadata')}</MenuItem></MenuGroup>}
      </Menu></>},
  ];
  return <><TitleFrame back={phone && context.effects.back ? {label: t('next.states.back'), onPress: goBack} : undefined}
    cover={{shape: 'poster', node: <WholeArtwork source={context.artwork.foreground(manifest.metadata.coverUrl, 784, 2 / 3)} alt={title} fit="cover" slot={periodical ? 3 / 4 : 2 / 3}
      fallback={<GeneratedCover title={title} author={author} glyph={Array.from(title.trim())[0] ?? ''} tint={tint}/>}/>}}
    kicker={seriesPresentation ? {label: seriesPresentation.label, onPress: seriesPresentation.onOpen} : undefined} title={title}
    metadata={<TitleFactsLine lead={author ? <CoverHeroLink person={author} onOpen={openAuthor}>{author}</CoverHeroLink> : undefined} parts={[
      ...(metadata.wordCount !== undefined ? [{id: 'words', text: t('next.reader.wordCount', {count: metadata.wordCount}), role: 'context' as const}] : []),
      ...(metadata.pageCount !== undefined ? [{id: 'pages', text: t('next.reader.pageCount', {count: metadata.pageCount}), role: 'context' as const}] : []),
      ...(metadata.estimatedSeconds !== undefined && metadata.estimatedSeconds > 0 ? [{id: 'estimate', text: t('next.reader.aboutDuration', {duration: defaultI18n.duration(readingDuration(metadata.estimatedSeconds))}), role: 'context' as const}] : []),
    ]}/>}
    primary={progression !== undefined && progression > 0 ? {icon: 'i-book', label: t('next.cards.read'), progress: progression, remaining: defaultI18n.percent(progression), onPress: () => open()}
      : {icon: 'i-book', label: t('next.reader.read'), onPress: () => open()}}
    actions={actions}
    synopsis={metadata.description} opening={!metadata.description && metadata.openingLines ? {label: t('next.reader.firstLines'), text: metadata.openingLines} : undefined}
    tabs={tabbed ? {items: [{value: 'contents', label: t('next.reader.contents')}, {value: 'details', label: t('next.reader.details')}], value: tab, onValueChange: setSection} : undefined}
    aside={tabbed ? undefined : aboutBlock}>
    {tabbed && tab === 'details' ? <PanelBlock label={t('title.aboutThisBook')}>{aboutBlock}</PanelBlock> : contentsBlock}
  </TitleFrame>
    <Dialog open={fileInfo} onOpenChange={setFileInfo} title={t('next.overlays.fileInfo')} returnFocusRef={editorAnchor}>
      {eligibility.versions.map(candidate => <ReadBookFacts key={candidate.sourceId} heading={candidate.format.toUpperCase()} facts={[
        {id: 'format', label: t('next.reader.format'), value: candidate.format.toUpperCase()},
        {id: 'size', label: t('next.reader.fileSize'), value: defaultI18n.bytes(candidate.size)},
      ]}/>)}</Dialog>
    <Dialog open={menu} onOpenChange={setMenu} title={t('next.reader.readDifferentVersion')} returnFocusRef={editorAnchor}>
      {eligibility.versions.map(candidate => <Button key={candidate.sourceId} variant="glass" aria-pressed={candidate.sourceId === sourceId} disabled={!candidate.inventoryAvailable || candidate.sourceId === sourceId}
        onClick={() => {if (factsCurrent()) pickVersion(candidate, attempt, sourceId);}}>{candidate.format.toUpperCase()}<span>{defaultI18n.bytes(candidate.size)}</span></Button>)}
    </Dialog>
    {editing && canEdit && <ReadMetadata context={context} reading={captured.context} sourceId={manifest.sourceId} format={manifest.format} itemId={eligibility.itemId} anchor={editorAnchor} onClose={() => {if (!current(original)) return; setEditing(false); refresh(original);}}/>}
    {author && added.moreBy?.length ? <Shelf className={styles.moreBy} heading={t('home.row.moreByAuthor', {name: author})} platform={phone ? 'phone-web' : 'desktop'} itemWidth={cardWidth}
      items={added.moreBy} getKey={item => item.id} renderItem={item => <CoverCard artwork={context.artwork.foreground(item.coverUrl, cardWidth * 2, 2 / 3)} kind={manifest.format === 'cbz' ? 'comic' : 'book'}
        model={{title: item.title, caption: []}} context="shelf" density="shelf" visible generated={{title: item.title, glyph: Array.from(item.title)[0] ?? '', tint: generatedCoverTint(item.title)}}
        disabled={!item.inventoryAvailable} playLabel={t('next.reader.read')}
        onNavigate={() => {if (context.isCurrent()) void router.navigate({to: '/item/$itemId', params: {itemId: item.id}}).catch(error => errorSink.current(error));}}
        onPlay={item.inventoryAvailable ? () => {if (context.isCurrent()) void router.navigate(readChosenDestination(item.id)).catch(error => errorSink.current(error));} : undefined}/>}/> : null}</>;
}

/** Preserve the file's hierarchy and original index; no inferred page or progress labels. */
function detailContents(contents: NonNullable<ReadingManifest['contents']>, navigate: (index: number) => void): readonly ReaderContentsEntry[] {
  type Branch = {id: string; label: string; onNavigate: () => void; children: Branch[]};
  const roots: Branch[] = [], ancestors: {depth: number; entry: Branch}[] = [];
  contents.forEach((item, index) => {
    while (ancestors.length && ancestors[ancestors.length - 1].depth >= item.depth) ancestors.pop();
    const entry: Branch = {id: String(index), label: item.title, onNavigate: () => navigate(index), children: []};
    const parent = ancestors[ancestors.length - 1];
    (parent ? parent.entry.children : roots).push(entry);
    ancestors.push({depth: item.depth, entry});
  });
  return roots;
}

/** How many highlights and bookmarks the reader has in this book, every page of them (a reader
 * with more than 20 pages of marks is counted as far as those). */
async function countMarks(session: ReadingSession, signal: AbortSignal) {
  let highlights = 0, bookmarks = 0, cursor = '';
  for (let page = 0; page < 20; page++) {
    const answer = await session.annotations(cursor, undefined, signal);
    for (const entry of answer.entries) if (entry.kind === 'highlight') highlights++; else bookmarks++;
    cursor = answer.nextCursor;
    if (!cursor) break;
  }
  return {highlights, bookmarks};
}
