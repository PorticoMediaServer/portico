import {useCallback, useEffect, useLayoutEffect, useRef, useState} from 'react';
import {createRoot} from 'react-dom/client';
import {connectReadingHost, ReadingError, validReadingIndexedSearchQuery, type ReadingGuest} from '@portico/client-core';
import {createReadingPublication, createEpubAdapter, createPdfAdapter, createComicAdapter} from '@portico/reader-core';
import {defaultI18n} from '@i18n';
import '../../../kit/tokens.css';
import '../../../kit/fonts.css';
import '../../../bootstrap.css';
import {Skeleton, StateView} from '../../../kit/states';
import {ReaderBook, type ReaderBookState} from '../ReaderBook';
import {bindReadPreferences} from '../preferences';
import {bindReadingProgress} from '../progress';
import {loadReadFonts, type ReadFonts} from '../fonts';
import {readerPdfAdapterOptions} from '../pdf-worker';
import styles from '../entry.module.css';

type NativeWindow = Window & {ReactNativeWebView?: {postMessage: (message: string) => void}; PorticoReader?: {receive: (message: string) => void; dispose: () => void}};
const nativeWindow = window as NativeWindow;
let root: ReturnType<typeof createRoot> | undefined;
const messages = new Set<(message: string) => void>();
// Defined before ready is sent. Native delivery calls this fixed app-owned port;
// it never executes selected-server JS or passes credentials into the document.
nativeWindow.PorticoReader = {receive(message) {for (const listener of [...messages]) listener(message);}, dispose() {connection.dispose(); root?.unmount();}};
const connection = connectReadingHost({
  send(message) {if (!nativeWindow.ReactNativeWebView) throw new Error('reader_host_unavailable'); nativeWindow.ReactNativeWebView.postMessage(message);},
  subscribe(listener) {messages.add(listener); return () => {messages.delete(listener);};},
});

type SearchAllAdmission = {guest: ReadingGuest; book: ReaderBookState; live: boolean;
  search: NonNullable<ReadingGuest['searchAll']>; pending?: AbortController};

/** Optional trusted-host action, owned by this exact visible reader. Hiding or
 * replaying the view retires its command without failing the retained book. */
function useNativeSearchAll(guest: ReadingGuest, book: ReaderBookState | undefined, active: boolean, reportError: () => void) {
  const [published, publish] = useState<SearchAllAdmission>();
  useLayoutEffect(() => {
    if (!book || !active || !guest.searchAll) return;
    const own: SearchAllAdmission = {guest, book, search: guest.searchAll, live: true};
    publish(own);
    return () => {own.live = false;own.pending?.abort();publish(current => current === own ? undefined : current);};
  }, [guest, book, active]);
  const own = published?.live && published.guest === guest && published.book === book && active ? published : undefined;
  if (!own) return undefined;
  const current = () => own.live && own.book.authority.isCurrent() && !own.book.publication.signal.aborted && !own.guest.session.signal.aborted;
  return (query: string) => {
    if (!current() || own.pending || !validReadingIndexedSearchQuery(query)) return;
    const operation = new AbortController();own.pending = operation;
    const signal = AbortSignal.any([operation.signal, own.book.publication.signal, own.guest.session.signal]);
    void own.search(query, signal).catch(error => {
      if (own.pending !== operation || !current() || signal.aborted) return;
      if ((error instanceof ReadingError && error.code === 'cancelled') || (error instanceof DOMException && error.name === 'AbortError')) return;
      reportError();
    }).finally(() => {if (own.pending === operation) own.pending = undefined;});
  };
}

function NativeReading({guest}: {guest: ReadingGuest}) {
  const [book, setBook] = useState<ReaderBookState>(), [failed, setFailed] = useState(false), [active, setActive] = useState(true), [crease, setCrease] = useState(guest.crease);
  const reportError = useCallback(() => setFailed(true), []);
  const progressRef = useRef<ReturnType<typeof bindReadingProgress>>(undefined);
  const onSearchAll = useNativeSearchAll(guest, book, active && !failed, reportError);
  useEffect(() => {
    let alive = true, fonts: ReadFonts | undefined;
    let publication: Awaited<ReturnType<typeof createReadingPublication>> | undefined;
    let reader: ReturnType<typeof bindReadPreferences> | undefined, progress: ReturnType<typeof bindReadingProgress> | undefined;
    const offActive = guest.onActive(setActive), offCrease = guest.onCrease(() => setCrease(guest.crease));
    const authority = {isCurrent: () => alive && !guest.session.signal.aborted,
      subscribe(listener: () => void) {guest.session.signal.addEventListener('abort', listener); return () => guest.session.signal.removeEventListener('abort', listener);}};
    void (async () => {
      publication = await createReadingPublication(guest.session);
      if (publication.format === 'epub') fonts = await loadReadFonts({document, signal: publication.signal});
      if (!authority.isCurrent()) return;
      const adapters = publication.format === 'epub' ? [createEpubAdapter({fonts: fonts!.fonts})] : publication.format === 'pdf'
        ? [createPdfAdapter(readerPdfAdapterOptions)] : [createComicAdapter({format: publication.format, pages: publication.comicPages!})];
      reader = bindReadPreferences({adapters, preferences: guest.preferences, authority, reportError: () => {if (alive) setFailed(true);}});
      progress = bindReadingProgress({controller: reader.controller, session: guest.session, initial: guest.initialProgress, authority, reportError: () => {if (alive) setFailed(true);}});
      progressRef.current = progress;
      setBook({...(guest.session.manifest.canAnnotate === true && guest.session.annotations && guest.session.mutateAnnotation ? {annotations:{manifest:guest.session.manifest,signal:guest.session.signal,annotations:guest.session.annotations,mutateAnnotation:guest.session.mutateAnnotation}} : {}), publication, controller: reader.controller, authority, contentsTarget: guest.contentsTarget, location: publication.resumeLocation(guest.initialProgress) ?? undefined});
    })().catch(() => {if (alive) setFailed(true);});
    return () => {
      alive = false; offActive(); offCrease(); progressRef.current = undefined;
      void (async () => {try {await progress?.stop(); await reader?.dispose();} finally {publication?.dispose(); fonts?.dispose(); guest.dispose();}})();
    };
  }, [guest]);
  if (failed) return <StateView variant="page" title={defaultI18n.t('next.states.pageFailed')} body={defaultI18n.t('next.states.pageFailedBody')}/>;
  return <div className={styles.host} hidden={!active} aria-hidden={!active} inert={!active}>{book
    ? <ReaderBook environment="native" onSearchAll={onSearchAll} saveHighlights={guest.saveHighlights} book={book} active={active} crease={crease} reportError={reportError} onBack={() => {void (async () => {await progressRef.current?.stop(); await guest.back();})().catch(reportError);}} onOpenSettings={() => {void guest.openSettings();}}/>
    : <Skeleton height="100dvh"/>}</div>;
}
function NativeReaderEntry() {
  const [guest, setGuest] = useState<ReadingGuest>(), [failed, setFailed] = useState(false);
  useEffect(() => {let active = true; void connection.ready.then(value => {if (active) setGuest(value);}, () => {if (active) setFailed(true);}); return () => {active = false; connection.dispose();};}, []);
  if (failed) return <StateView variant="page" title={defaultI18n.t('next.states.pageFailed')} body={defaultI18n.t('next.states.pageFailedBody')}/>;
  return guest ? <NativeReading guest={guest}/> : <Skeleton height="100dvh"/>;
}
root = createRoot(document.getElementById('root')!);
root.render(<NativeReaderEntry/>);
