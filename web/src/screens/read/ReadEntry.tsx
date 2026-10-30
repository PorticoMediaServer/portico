import {useGuardedLibraryContext, useLibraryRouteGuard} from '../browse/library-route-guard';
import {forwardFacts} from '../../shell/fact-source';
import {useCallback, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {useLocation} from '@tanstack/react-router';
import {defaultI18n} from '@i18n';
import {ReadingError, type ReadingSession} from '@portico/client-core';
import {createComicAdapter, createEpubAdapter, createPdfAdapter, createReadingPublication,
  type ReadingController, type ReadingPublication, type ReaderLocation, type ReaderContentsTarget} from '@portico/reader-core';
import {useProtectedWebScreenContext} from '../../route-registry-view';
import {router} from '../../router';
import {StateView} from '../../kit/states';
import {Button} from '../../kit/controls';
import {createReadBindings, type ReadBindingContext} from './bindings';
import {bindReadPreferences} from './preferences';
import {bindReadingProgress} from './progress';
import {loadReadFonts, type ReadFonts} from './fonts';
import {readerPdfAdapterOptions} from './pdf-worker';
import type {ReaderMountAuthority} from './mount';
import styles from './entry.module.css';
import {takeContentsTarget, retireContentsTarget} from './contents-handoff';
import {ReaderBook} from './ReaderBook';
import {readSearch} from './route-search';
import {indexedReadingOpeningMarker, indexedReadingOpeningVersion, receiveIndexedReadingTarget, retireIndexedReadingOpening, subscribeIndexedReadingOpenings, type IndexedReadingReceiver} from '../search/reading-open-handoff';

const {t} = defaultI18n;
type Captured = NonNullable<ReturnType<typeof useProtectedWebScreenContext>>;
type OpenBook = Readonly<{session: ReadingSession; publication: ReadingPublication; controller: ReadingController;
  authority: ReaderMountAuthority; location?: ReaderLocation; contentsTarget?: ReaderContentsTarget; annotations?: ReadingSession}>;
type ReadEntryProps = {captured: Captured; itemId: string; sourceId?: string; active?: boolean; onClose?: () => void};
type ReceiverOpening = {context: Captured['context']; itemId: string; sourceId?: string; location: typeof router.state.location;
  carriedLocation?: typeof router.state.location; settingsLocation?: typeof router.state.location;
  key: number; live: boolean; received: IndexedReadingReceiver; current: () => boolean; retire: () => void; reportError: (error: unknown) => void};
const sameHistoryEntry = (left: typeof router.state.location, right: typeof router.state.location) => typeof left.state.__TSR_key === 'string'
  && left.state.__TSR_key === right.state.__TSR_key && left.state.__TSR_index === right.state.__TSR_index && left.href === right.href
  && indexedReadingOpeningMarker(left) === indexedReadingOpeningMarker(right);

/** Real publication session; services start only inside the captured route lifetime. */
export function ReadEntry(props: ReadEntryProps) {
  const {captured, itemId, sourceId, active = true, onClose} = props, location = useLocation({select: value => value});
  const version = useSyncExternalStore(subscribeIndexedReadingOpenings, indexedReadingOpeningVersion, indexedReadingOpeningVersion);
  const [published, publish] = useState<ReceiverOpening>();
  const [navigationRevision, invalidateNavigation] = useState(0);
  const owned = useRef<ReceiverOpening | undefined>(undefined), generation = useRef(0);
  // The opening keeps this commit's shell reporting port; a later binding identity must
  // not rebuild the received reading target.
  const shellEffects = useRef(captured.binding.home.effects);
  useLayoutEffect(() => {shellEffects.current = captured.binding.home.effects;});
  const context = captured.context;
  useLayoutEffect(() => {
    const held = owned.current, same = held?.context === context && held.itemId === itemId && held.sourceId === sourceId;
    if (!active || location.pathname !== '/read/' + encodeURIComponent(itemId) || readSearch(location.search).sourceId !== sourceId) {
      if (held && (!same || location.pathname !== '/settings' || !held.current())) held.retire();
      return;
    }
    if (same && held.live && (held.location === location || held.carriedLocation === location
      || held.carriedLocation && sameHistoryEntry(held.carriedLocation, location))) {held.carriedLocation = location;return;}
    held?.retire();
    const received = receiveIndexedReadingTarget(context, itemId, sourceId, location);
    if (received.kind === 'pending') return;
    const session = context.session(), viewer = {...session.viewer}, report = shellEffects.current.reportError;
    let off = () => {};
    const authority = () => {
      if (!context.isCurrent()) return false;
      const now = context.session();
      return now.sessionFamilyId === session.sessionFamilyId && (['serverId', 'authority', 'accountId', 'profileId', 'role'] as const).every(key => now.viewer[key] === viewer[key]);
    };
    const opening: ReceiverOpening = {context, itemId, sourceId, location, key: ++generation.current, live: true, received,
      retire: () => {opening.live = false;off();}, reportError: error => {if (opening.current()) report(error);}, current: () => {
      if (!opening.live || !context.isCurrent() || received.kind === 'accepted' && !received.current()) return false;
      const actual = router.state.location;
      return authority() && (actual === opening.location || actual === opening.carriedLocation
        || !!opening.carriedLocation && sameHistoryEntry(actual, opening.carriedLocation)
        || !!opening.settingsLocation && sameHistoryEntry(actual, opening.settingsLocation));
    }};
    off = router.subscribe('onBeforeNavigate', event => {
      const from = event.fromLocation;
      const fromReader = from === opening.location || from === opening.carriedLocation
        || !!from && !!opening.carriedLocation && sameHistoryEntry(from, opening.carriedLocation);
      const fromSettings = !!from && !!opening.settingsLocation && sameHistoryEntry(from, opening.settingsLocation);
      if (authority() && (fromReader || fromSettings) && event.toLocation.pathname === '/settings') {opening.settingsLocation = event.toLocation;return;}
      if (authority() && fromSettings && sameHistoryEntry(event.toLocation, opening.location)) {opening.carriedLocation = event.toLocation;return;}
      // A marked Back/Forward or unrelated opening cannot reuse this receiver.
      if (received.kind !== 'ordinary') {
        retireIndexedReadingOpening(context, opening.location);opening.retire();
        queueMicrotask(() => {if (owned.current === opening) invalidateNavigation(value => value + 1);});
      }
    });
    owned.current = opening;publish(opening);
  }, [context, itemId, sourceId, active, location, version, navigationRevision]);
  useLayoutEffect(() => () => {owned.current?.retire();}, []);
  const receiver = published?.live && published.context === context && published.itemId === itemId && published.sourceId === sourceId && published.current()
    && (active ? published.location === location || published.carriedLocation === location || !!published.carriedLocation && sameHistoryEntry(published.carriedLocation, location)
      : location.pathname === '/settings') ? published : undefined;
  const leave = onClose ?? (() => router.history.back());
  return receiver ? <ReadEntryBody key={receiver.key} {...props} receiver={receiver}/>
    : <div className={styles.loading} aria-busy="true"><span className={styles.escape}><Button variant="glass" icon="i-left" iconOnly aria-label={t('next.reader.back')} onClick={leave}/></span><span className={styles.loadingRing} aria-hidden="true"/></div>;
}
function ReadEntryBody({captured, itemId, sourceId, active = true, onClose, receiver}: ReadEntryProps & {receiver: ReceiverOpening}) {
  const [state, setState] = useState<Readonly<{book?: OpenBook; failed?: boolean}>>({});
  const [stagedContents] = useState(() => takeContentsTarget(captured.context, itemId, sourceId));
  const contentsTarget = receiver.received.kind === 'ordinary' ? stagedContents : undefined;
  const [retry, setRetry] = useState(0);
  const reportError = receiver.reportError;
  const guard = useLibraryRouteGuard(receiver.context, receiver, receiver.current, {itemId}, reportError,
    () => receiver.current() && router.state.location.pathname === '/read/' + encodeURIComponent(itemId));
  const readingContext = useGuardedLibraryContext(receiver.context, guard)!;
  const currentReading = useCallback(() => receiver.current() && readingContext.isCurrent(), [receiver, readingContext]);
  useEffect(() => {
    retireContentsTarget(captured.context, stagedContents);
    const original = readingContext;
    const bindingContext: ReadBindingContext = forwardFacts(original, {isCurrent: () => original.isCurrent() && currentReading()});
    const life = new AbortController(), bindings = createReadBindings(bindingContext);
    let alive = true, fonts: ReadFonts | undefined, publication: ReadingPublication | undefined;
    let reader: ReturnType<typeof bindReadPreferences> | undefined, progress: ReturnType<typeof bindReadingProgress> | undefined;
    const offRetired = bindings.authority.subscribe(() => {if (alive) setState({failed: true});});
    let disposal: Promise<void> | undefined;
    const dispose = () => {
      if (disposal) return disposal;
      alive = false; offRetired(); life.abort();
      disposal = (async () => {
        try {await reader?.dispose();}
        finally {
          try {await progress?.stop();}
          finally {
            try {publication?.dispose();}
            finally {try {fonts?.dispose();} finally {await bindings.dispose();}}
          }
        }
      })();
      return disposal;
    };
    setState({});
    void (async () => {
      if (!currentReading()) throw new ReadingError('cancelled');
      if (receiver.received.kind === 'refused' || receiver.received.kind === 'pending') throw new ReadingError('source_changed');
      const listed = await bindings.client.versions(itemId, life.signal), versions = listed.versions.filter(version => version.inventoryAvailable);
      if (!alive || !currentReading()) return;
      // Without an explicit version, open the one the server chose for this reader.
      const selected = versions.find(version => version.sourceId === (sourceId ?? listed.chosenSourceId));
      if (!selected) {
        if (versions.length > 1 && !sourceId) await router.navigate({to: '/item/$itemId', params: {itemId}, replace: true});
        else setState({failed: true});
        return;
      }
      const session = await bindings.open({itemId, sourceId: selected.sourceId});
      if (!alive || !currentReading()) throw new ReadingError('cancelled');
      if (receiver.received.kind === 'accepted') {
        const expected = receiver.received.target, manifest = session.manifest;
        if (manifest.itemId !== expected.itemId || manifest.sourceId !== expected.sourceId || manifest.revision !== expected.sourceRevision)
          throw new ReadingError('source_changed');
      }
      publication = await createReadingPublication(session, life.signal);
      if (!alive || !currentReading()) throw new ReadingError('cancelled');
      if (contentsTarget) {
        const current = publication.contentsTarget(contentsTarget.index);
        if (current.sourceId !== contentsTarget.sourceId || current.sourceRevision !== contentsTarget.sourceRevision) throw new Error('source_changed');
      }
      const saved = await session.progress(life.signal), location = publication.resumeLocation(saved) ?? undefined;
      if (!alive || !currentReading()) throw new ReadingError('cancelled');
      if (publication.format === 'epub') fonts = await loadReadFonts({document, signal: life.signal});
      if (!alive || !currentReading()) return;
      const adapters = publication.format === 'epub' ? [createEpubAdapter({fonts: fonts!.fonts})]
        : publication.format === 'pdf' ? [createPdfAdapter(readerPdfAdapterOptions)] : [createComicAdapter({format: publication.format, pages: publication.comicPages!})];
      reader = bindReadPreferences({adapters, preferences: bindings.preferences, authority: bindings.authority, reportError: code => reportError({code})});
      progress = bindReadingProgress({controller: reader.controller, session, initial: saved, authority: bindings.authority, reportError});
      setState({book: {session, publication, controller: reader.controller, authority: bindings.authority, location, contentsTarget, ...(session.manifest.canAnnotate === true ? {annotations:session} : {})}});
    })().catch(error => {
      // A rejected bootstrap must never leave the reader on its loading skeleton. This attempt is
      // still the live one unless it was retired, so show the failure state; report only while the
      // captured authority still holds, since a retired capture owns no error surface.
      if (alive && !life.signal.aborted && currentReading()) {if (bindings.authority.isCurrent()) reportError(error); setState({failed: true});}
      void dispose().catch(() => {});
    });
    return () => {void dispose().catch(() => {});};
  }, [captured.context, itemId, sourceId, stagedContents, contentsTarget, reportError, retry, receiver, readingContext, currentReading]);
  if (state.book) return <ReaderBook key={state.book.session.manifest.sessionId} book={state.book} active={active} reportError={reportError} onSearchAll={query => {if (!active || !currentReading()) return; void router.navigate({to: '/search/reading', search: {q: query}}).catch(error => {if (currentReading()) reportError(error);});}} onOpenSettings={() => {if (!currentReading()) return;void router.navigate({to: '/settings', search: {scope: 'account', section: 'reading'}}).catch(reportError);}} onBack={() => {if (currentReading()) (onClose ?? (() => router.history.back()))();}}/>;
  /** The drawn way back stays available while a book is opening and when it cannot open. */
  const leave = () => {if (currentReading()) (onClose ?? (() => router.history.back()))();};
  if (state.failed) return <StateView variant="page" title={t('next.states.pageFailed')} body={t('next.states.pageFailedBody')}
    primary={{id: 'retry', label: t('next.states.tryAgain'), onPress: () => {if (currentReading()) setRetry(value => value + 1);}}}
    secondary={{id: 'back', label: t('next.reader.back'), onPress: leave}}/>;
  return <div className={styles.loading} aria-busy="true"><span className={styles.escape}><Button variant="glass" icon="i-left" iconOnly aria-label={t('next.reader.back')} onClick={leave}/></span><span className={styles.loadingRing} aria-hidden="true"/></div>;
}
