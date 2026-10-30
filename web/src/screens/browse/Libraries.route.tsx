import {useEffect, useState} from 'react';
import {defaultI18n} from '@i18n';
import {readLibrarySummaries} from '@core/library-summary.ts';
import {defineWebScreen} from '../../route-registry';
import {useProtectedWebScreenContext} from '../../route-registry-view';
import {router} from '../../router';
import {GridSkeleton, StateView} from '../../kit/states';
import {Banner} from '../../kit/overlays';
import {LIBRARY_DIRECTORY_RETRY_MS} from '../../shell/bootstrap';
import {useShellWidth} from '../../shell/viewport';
import {useLoadingClock} from '../detail/Loading';
import {LibrariesScreen} from './Libraries';
import styles from './browse.module.css';

function LibrariesRouteScreen() {
  const captured = useProtectedWebScreenContext(), context = captured?.context, width = useShellWidth(), elapsed = useLoadingClock();
  const [libraries, setLibraries] = useState<Awaited<ReturnType<typeof readLibrarySummaries>>>();
  const [failed, setFailed] = useState(false), [attempt, setAttempt] = useState(0);
  useEffect(() => {
    if (!context) return;
    // A summary that did not answer is read once more after a short wait, then left to Try again.
    const life = new AbortController();setFailed(false);
    let timer: ReturnType<typeof setTimeout> | undefined;
    const read = (automatic: boolean) => void readLibrarySummaries(context.api, life.signal).then(value => {
      if (!life.signal.aborted && context.isCurrent()) {setLibraries(value);setFailed(false);}
    }).catch(() => {
      if (life.signal.aborted || !context.isCurrent()) return;
      if (automatic) timer = setTimeout(() => read(false), LIBRARY_DIRECTORY_RETRY_MS); else setFailed(true);
    });
    read(attempt === 0);
    return () => {life.abort(); if (timer) clearTimeout(timer);};
  }, [context, attempt]);
  if (!captured) return null;
  // The overview's own heading is known before its summaries are; the grid waits in the
  // shared shape instead of leaving the page blank.
  const heading = <h1>{defaultI18n.t('next.browse.libraries')}</h1>;
  const retry = () => setAttempt(value => value + 1);
  // Without summaries, the libraries the shell already knows still show and open, without
  // counts or covers, under one quiet line. Only knowing nothing at all is a page failure.
  const known = captured.context.facts.identity;
  const shown = libraries ?? (failed && !known.librariesUnavailable && known.libraries.length ? known.libraries.map(library => Object.freeze({id: library.id, name: library.name, kind: library.kind,
    summary: Object.freeze({counts: [], coverUrls: [], latestArtwork: '', firstScan: false, empty: false, emptyKnown: false})})) : undefined);
  if (failed && !shown) return <section className={styles.libraries}>{heading}<StateView title={defaultI18n.t('next.states.pageFailed')} body={defaultI18n.t('next.states.pageFailedBody')}
    primary={{id: 'retry', label: defaultI18n.t('next.states.tryAgain'), onPress: retry}}/></section>;
  const notice = failed && !libraries ? <Banner icon="i-warn" action={{label: defaultI18n.t('next.states.tryAgain'), onAction: retry}}>{defaultI18n.t('next.browse.librarySummariesUnavailable')}</Banner> : undefined;
  return shown ? <LibrariesScreen libraries={shown} notice={notice} artwork={captured.context.artwork}
    onOpen={libraryId => {void router.navigate({to: '/library/$libraryId', params: {libraryId}, search: {}}).catch(captured.binding.home.effects.reportError);}}/>
    : <section className={styles.libraries}>{heading}<GridSkeleton elapsedMs={elapsed} window={{width, height: window.innerHeight, scrollTop: window.scrollY}} itemCount={6}/></section>;
}

export default defineWebScreen({id: 'libraries', component: LibrariesRouteScreen});
