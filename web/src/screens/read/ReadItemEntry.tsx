import {useReadingFavorite} from './useReadingFavorite';
import {ReadSavedAction} from './ReadSavedAction';
import {ReadMarkAction} from './ReadMarkAction';
import {useCallback, useEffect, useLayoutEffect, useRef, useState, type CSSProperties} from 'react';
import {useLocation} from '@tanstack/react-router';
import {defaultI18n} from '@i18n';
import {generatedCoverTint} from '@design';
import {ApiError} from '@core/index.ts';
import {rereadReadingSource} from '@core/reading-reread.ts';
import {validateRepair} from '@core/metadata-repair.ts';
import {encodedRoutePath} from '../../../../packages/contracts/src/routes.ts';
import {createReadingPublication, type ReaderContentsTarget} from '@portico/reader-core';
import {stageContentsTarget, retireContentsTarget} from './contents-handoff';
import {ReaderContents, type ReaderContentsEntry} from '../../kit/reader/ReaderPanel';
import type {ReadingManifest, ReadingProgress, ReadingSession} from '@portico/client-core';
import {saveReadingFile} from './save-file';
import {useProtectedWebScreenContext} from '../../route-registry-view';
import {router} from '../../router';
import {Icon} from '../../kit/Icon';
import {Button, ResumeButton} from '../../kit/controls';
import {Dialog, Menu, MenuGroup, MenuItem} from '../../kit/overlays';
import {usePhone} from '../../kit/overlays/shared';
import {runScreenCommand} from '../detail/context';
import {Skeleton, StateView} from '../../kit/states';
import {ReadBookFacts, ReadBookHero, ReadBookSections} from '../../kit/reader/ReadBookHero';
import {readBookTitleStyle} from '../../kit/reader/book-title';
import {ServerMark} from '../../kit/shell';
import {useLibraryScreenContext} from '../browse/screen-context';
import {ReadMetadata} from './ReadMetadata';
import {createReadBindings} from './bindings';
import {readDestination, type ReadItemEligibility} from './navigation';
import {readBackCommit, readRetireAttempt, readRouteEntry, readVersionCanChoose, readVersionRecovery, type ReadRouteEntry, type ReadVersion, type ReadVersionSource} from './read-version-choice';
import styles from './read-item.module.css';

type Captured = NonNullable<ReturnType<typeof useProtectedWebScreenContext>>;
type ReadAttempt = {readonly context: Captured['context']; readonly entry: typeof router.state.location; readonly sourceId: string | undefined; readonly retry: number; readonly fileLife: AbortController; session?: ReadingSession; saving?: boolean; rereading?: boolean; live: boolean; ready: boolean; target?: (index: number) => ReaderContentsTarget};
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
  const [saving, setSaving] = useState(false), [rereading, setRereading] = useState(false);
  const [attempt, setAttempt] = useState<ReadAttempt>();
  const attemptRef = useRef<ReadAttempt | undefined>(undefined);
  const committed = useRef<ReadVersionSource | undefined>(undefined);
  useLayoutEffect(() => {committed.current = {context: captured.context, sourceId, versions: eligibility.versions};}, [captured.context, sourceId, eligibility.versions]);
  const [detail, setDetail] = useState<Readonly<{attempt?: ReadAttempt; manifest?: ReadingManifest; progress?: ReadingProgress; client?: ReturnType<typeof createReadBindings>['client']; targets?: readonly ReaderContentsTarget[]; failed?: boolean}>>({});
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
  useLayoutEffect(() => {
    const value: ReadAttempt = {context: captured.context, entry: routeLocation, sourceId, retry, fileLife: new AbortController(), live: true, ready: false};
    attemptRef.current = value; setAttempt(value); setSaving(false); setRereading(false); setMore(false); setMenu(false); setFileInfo(false);
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
    })().catch(error => {if (live()) {errorSink.current(error); original.ready = false; original.target = undefined; setDetail({attempt: original, failed: true});} void bindings.close().catch(() => {});});
    return () => {active = false; original.ready = false; original.target = undefined; publication?.dispose(); off(); void bindings.dispose().catch(() => {});};
  }, [attempt, eligibility.itemId, current]);
  // The reading branch owns no shell navigation on a phone, so the page keeps one
  // Back of its own before loading, failure and ready content alike.
  const phoneBack = phone && context.effects.back ? <div className={styles.phoneTopBar} data-read-phone-back>
    <Button className={styles.phoneBack} size="small" variant="icon" icon="i-left" iconOnly aria-label={t('next.states.back')}
      onClick={() => runScreenCommand(context, () => {
        if (!context.isCurrent()) return;
        // The callback's own entry must still be committed and live; then choose Back or Home.
        const destination = readBackCommit(routeEntry, committedEntry.current, readRouteEntry(router.state.location), router.history.canGoBack());
        if (destination === 'back') context.effects.back?.();
        else if (destination === 'home') context.effects.openHome?.();
      })}/>
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
    setSaving(false); setRereading(false);
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
    <StateView variant="page" title={t('next.states.pageFailed')} body={t('next.states.pageFailedBody')}
      primary={{id: 'retry', label: t('next.states.tryAgain'), onPress: () => refresh(attempt)}}/>
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
  const canSaveFile = manifest.canSaveFile === true && (manifest.conversion?.originalSize ?? manifest.size) <= 64 << 20;
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
  /** Reopens this exact source and refreshes only its file-derived facts. The owner
   * must still own the metadata revision they are looking at: the repair document
   * supplies it, and a changed document is reloaded, never replayed. */
  const reread = async () => {
    // The claim belongs to this one opened attempt: a successor source starts its own read,
    // and an old read can neither block it nor clear its state when it finishes.
    if (!canEdit || !manifest || !original || !original.ready || original.rereading || !factsCurrent()) return;
    original.rereading = true; setRereading(true);
    // The error port belongs to the command that was dispatched, not to whatever page is
    // current by the time a later failure arrives.
    const reportError = errorSink.current, signal = original.fileLife.signal;
    const owns = () => current(original) && attemptRef.current === original && original.ready
      && original.session?.manifest === manifest && detail.manifest === manifest && !signal.aborted;
    try {
      if (!owns()) return;
      const raw = await context.api.request<unknown>(encodedRoutePath('get_metadata_kind_id', {kind: 'item', id: encodeURIComponent(eligibility.itemId)}), 'GET', undefined, signal);
      if (!owns()) return;
      const libraryId = (raw as {libraryId?: unknown} | null)?.libraryId;
      if (typeof libraryId !== 'string' || !libraryId || libraryId.length > 256) throw new Error('invalid_metadata_target');
      const repair = validateRepair(raw, context.scope, Object.freeze({kind: 'item', id: eligibility.itemId, libraryId}));
      if (!owns()) return;
      await rereadReadingSource(context.api, {itemId: eligibility.itemId, sourceId: manifest.sourceId, sourceRevision: manifest.revision, metadataRevision: repair.revision}, {signal, isCurrent: owns});
      if (owns()) refresh(original);
    } catch (error) {
      if (owns()) {
        reportError(error);
        // The reviewed metadata moved: reload the page instead of sending the same decision again.
        if (error instanceof ApiError && error.status === 409) refresh(original);
      }
    } finally {
      original.rereading = false;
      // Only this attempt's own render may clear the busy state; a successor keeps its own.
      if (attemptRef.current === original && current(original)) setRereading(false);
    }
  };
  const details = <ReadBookFacts heading={t('next.reader.details')} facts={[
    ...(author ? [{id: 'author', label: t('filter.field.author'), value: author}] : []),
    ...(manifest.metadata.language ? [{id: 'language', label: t('next.selection.language'), value: manifest.metadata.language}] : []),
    ...(metadata.wordCount !== undefined ? [{id: 'words', label: t('next.reader.wordsLabel'), value: defaultI18n.number(metadata.wordCount)}] : []),
    ...(manifest.format === 'cbz' && manifest.pageNames?.length ? [{id: 'pages', label: t('next.reader.pagesStrip'), value: defaultI18n.number(manifest.pageNames.length)}] : []),
  ]}/>;
  return <>{phoneBack}<ReadBookHero cover={context.artwork.foreground(manifest.metadata.coverUrl, 784, 2 / 3)} title={title} titleStyle={readBookTitleStyle(title, !!metadata.openingLines)} author={author ? {name: author} : undefined} series={seriesPresentation} facts={[
      ...(metadata.wordCount !== undefined ? [{id: 'words', text: t('next.reader.wordCount', {count: metadata.wordCount}), role: 'context' as const}] : []),
      ...(metadata.pageCount !== undefined ? [{id: 'pages', text: t('next.reader.pageCount', {count: metadata.pageCount}), role: 'context' as const}] : []),
      ...(metadata.estimatedSeconds !== undefined && metadata.estimatedSeconds > 0 ? [{id: 'estimate', text: t('next.reader.aboutDuration', {duration: defaultI18n.duration(metadata.estimatedSeconds)}), role: 'context' as const}] : []),
    ]} opening={metadata.openingLines ? {label: t('next.reader.openingLines'), text: metadata.openingLines} : undefined}
    ambient={<span className={styles.ambient} style={{'--read-tint-first': tint.first, '--read-tint-second': tint.second} as CSSProperties} aria-hidden="true"/>}
    actions={<>{progression !== undefined && progression > 0 ? <ResumeButton className={styles.primary} icon="i-book" progress={progression} remaining={defaultI18n.percent(progression)} onClick={() => open()}>{t('next.cards.read')}</ResumeButton>
      : <Button className={styles.primary} variant="primary" icon="i-book" onClick={() => open()}>{t('next.reader.read')}</Button>}{detail.client && <ReadSavedAction client={detail.client} itemId={eligibility.itemId} isCurrent={factsCurrent} reportError={error => errorSink.current(error)}/>}{detail.client && <ReadMarkAction client={detail.client} itemId={eligibility.itemId} isCurrent={factsCurrent} reportError={error => errorSink.current(error)}/>}</>}
    under={<><Button ref={editorAnchor} size="small" variant="glass" icon="i-more" iconOnly aria-label={t('next.controls.more')} onClick={() => {if (factsCurrent()) setMore(value => !value);}}/>
      <Menu open={more} onOpenChange={setMore} title={title} anchorRef={editorAnchor}>
        {favorite && <MenuItem icon="i-heart" selected={favorite.selected} disabled={favorite.busy} onSelect={favorite.onSelect}>{favorite.label}</MenuItem>}
        {canSaveFile && <MenuItem disabled={saving} onSelect={() => {void saveFile();}}>{t('next.reader.saveFile')}</MenuItem>}
        {chooseVersion && <MenuItem icon="i-book" onSelect={() => {if (factsCurrent()) {setMore(false); setMenu(true);}}}>{t('next.reader.readDifferentVersion')}</MenuItem>}
        <MenuItem icon="i-info" onSelect={() => {if (factsCurrent()) {setMore(false); setFileInfo(true);}}}>{t('next.overlays.fileInfo')}</MenuItem>
        {canEdit && <MenuGroup ownerOnly label={<span>{t('next.browse.server')}<ServerMark size={22}/></span>}><MenuItem onSelect={() => {if (context.isCurrent()) setEditing(true);}}>{t('entry.editMetadata')}</MenuItem><MenuItem disabled={rereading} onSelect={() => {if (context.isCurrent()) void reread();}}>{t('next.reader.readFileAgain')}</MenuItem></MenuGroup>}
      </Menu></>}/>
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
    <ReadBookSections labels={{contents: t('next.reader.contents'), details: t('next.reader.details')}}
      contents={detail.targets?.length ? <><h2>{t('next.reader.contents')}</h2><ReaderContents entries={detailContents(manifest.contents ?? [], index => open(index))}/></> : undefined} details={details}/></>;
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
