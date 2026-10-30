import {useCallback, useMemo, useSyncExternalStore, type ReactNode} from 'react';
import {readerErrorCode, type ReaderController, type ReaderSnapshot} from '@portico/reader-core';
import {ReaderChrome, type ReaderChromeProps} from './ReaderChrome';
import {ReaderSurface, type ReaderSurfaceProps} from './ReaderSurface';
import styles from '../../kit/reader/surface.module.css';

export type ReaderChromePresentation = Omit<ReaderChromeProps,
  'title' | 'subtitle' | 'progression' | 'pageTurnsEnabled' | 'onBack' | 'onTurn' | 'onSeek' | 'subscribeSurfaceInput' | 'readActivity' | 'children' | 'fixedPage' | 'comic' | 'textSelectionActive'>;
export type ReadReaderProps<P extends object> = ReaderSurfaceProps<P> & Readonly<{
  /** Source/profile data is formatted by the real localized adapter, never a fixture here. */
  presentation: (snapshot: ReaderSnapshot<P>) => ReaderChromePresentation;
  /** Approved shared loading/error states; raw engine messages are never displayed. */
  state?: (snapshot: ReaderSnapshot<P>) => ReactNode;
  onBack: () => void;
}>;

/** The page arrows sit 40-92 px from each edge (reader.module.css); text starts at the mock-up's 120 px inset.
 * A coarse pointer never draws them. */
const turnClearance = (shown: boolean) => shown && !(typeof matchMedia === 'function' && matchMedia('(pointer: coarse)').matches) ? 120 : undefined;
/** The checked main controller is the single state and navigation owner for all formats. */
export function ReadReader<P extends object>({controller, source, format, initialLocation, metadata, signal, authority, reportError, presentation, state, onBack, crease}: ReadReaderProps<P>) {
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot);
  const chrome = presentation(snapshot);
  const readActivity = useMemo(() => {
    const readers = new Set<() => void>();
    const current = (value: ReaderSnapshot<P>) => authority.isCurrent() && !signal?.aborted && value.status === 'ready'
      && value.sourceId === source.id && value.sourceRevision === source.revision;
    return {
      subscribe(listener: () => void) {
        let active = true;
        const read = () => {const value = controller.getSnapshot(); if (active && current(value) && !value.selection) listener();};
        readers.add(read);
        return () => {active = false; readers.delete(read);};
      },
      scroll() {for (const read of readers) read();},
      turned(before: ReaderSnapshot<P>, next: ReaderSnapshot<P>) {
        const from = before.location, to = next.location;
        // Only a completed original page command with changed real coordinates
        // counts. Resume, reflow, outline enrichment and refused/no-op turns do not.
        if (current(before) && current(next) && from && to && from.format === to.format
          && (from.progression !== to.progression || (from.format === 'epub' && to.format === 'epub' ? from.cfi !== to.cfi
            : from.format !== 'epub' && to.format !== 'epub' && from.page !== to.page))) {
          for (const read of readers) read();
        }
      },
    };
  }, [authority, controller, signal, source.id, source.revision]);
  const navigate = useCallback((target: Parameters<ReaderController<P>['navigate']>[0]) => {
    const active = controller.getSnapshot();
    if (!authority.isCurrent() || signal?.aborted || active.status !== 'ready' || active.sourceId !== source.id || active.sourceRevision !== source.revision) return;
    void controller.navigate(target).then(() => {
      if (target.kind === 'next' || target.kind === 'previous') readActivity.turned(active, controller.getSnapshot());
    }).catch(error => { const active = controller.getSnapshot();if (authority.isCurrent() && !signal?.aborted && active.sourceId === source.id && active.sourceRevision === source.revision) reportError(readerErrorCode(error)); });
  }, [authority, controller, reportError, signal, source.id, source.revision, readActivity]);
  const turn = useCallback((kind: 'next' | 'previous') => {
    if (controller.getSnapshot().canTurnAtFit) navigate({kind});
  }, [controller, navigate]);
  const seek = useCallback((value: number) => navigate({kind: 'progression', value}), [navigate]);
  const currentPublication = !signal?.aborted && snapshot.sourceId === source.id && snapshot.sourceRevision === source.revision;
  const publicationReady = currentPublication && snapshot.status === 'ready';
  return <ReaderChrome {...chrome} title={metadata ? metadata.title : snapshot.metadata?.title} subtitle={metadata ? metadata.author : snapshot.metadata?.author}
    progression={snapshot.location?.progression} pageTurnsEnabled={publicationReady && snapshot.canTurnAtFit}
    fixedPage={format !== 'epub'} comic={format === 'cbz' || format === 'cbr'} holdOpen={chrome.holdOpen || !publicationReady || snapshot.selection !== null}
    textSelectionActive={snapshot.selection !== null}
    subscribeSurfaceInput={controller.subscribeSurfaceInput} readActivity={readActivity} onTurn={turn} onSeek={seek} onBack={onBack}>
    <ReaderSurface controller={controller} source={source} format={format} initialLocation={initialLocation} metadata={metadata} signal={signal} authority={authority} reportError={reportError} crease={crease} turnClearance={turnClearance(chrome.showPageArrows)} />
    {snapshot.status !== 'ready' && state && <div className={styles.state}>{state(snapshot)}</div>}
  </ReaderChrome>;
}
