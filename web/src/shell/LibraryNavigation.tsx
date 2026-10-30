import {useCallback, useLayoutEffect, useRef, useState} from 'react';
import {Link} from '@tanstack/react-router';
import {defaultI18n} from '@i18n';
import {libraryNavigationIcon, type Library} from '@core/index.ts';
import {Icon} from '../kit/Icon';
import styles from './shell.module.css';

/** Only entitled library links scroll; the shell keeps all other destinations pinned. */
export function LibraryNavigation({libraries, sidebar, activeLibraryId}: {libraries: readonly Library[]; sidebar: boolean; activeLibraryId?: string}) {
  const track = useRef<HTMLDivElement>(null);
  const [edges, setEdges] = useState({start: false, end: false});
  const measure = useCallback(() => {
    const element = track.current;
    if (!element) return;
    const start = element.scrollTop > 1, end = element.scrollTop + element.clientHeight < element.scrollHeight - 1;
    setEdges(previous => previous.start === start && previous.end === end ? previous : {start, end});
  }, []);
  useLayoutEffect(() => {
    const element = track.current;
    if (!element) return;
    const observer = new ResizeObserver(measure);
    observer.observe(element); measure();
    return () => observer.disconnect();
  }, [libraries, sidebar, measure]);
  return <div className={styles.libraryGroup} data-window-no-drag>
    {sidebar ? <div className={styles.libraryHeading}>{defaultI18n.t('next.home.shell.libraries')}</div> : <div className={styles.railDivider} />}
    <div ref={track} className={styles.railLibraries} data-navigation-libraries data-fade-start={edges.start || undefined} data-fade-end={edges.end || undefined} onScroll={measure}>
      {libraries.map(library => <Link className={styles.railItem} key={library.id} to="/library/$libraryId" params={{libraryId: library.id}} aria-current={library.id === activeLibraryId ? 'page' : undefined}><Icon name={libraryNavigationIcon(library.kind, 'touch')} size={22} /><span data-nav-label>{library.name}</span></Link>)}
    </div>
  </div>;
}
