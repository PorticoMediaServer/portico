import {useLayoutEffect, useRef, useState} from 'react';
import {Link, useNavigate} from '@tanstack/react-router';
import {defaultI18n} from '@i18n';
import type {Library} from '@core/index.ts';
import {Icon} from '../kit/Icon';
import {Menu} from '../kit/overlays';
import {desktopLibraryLimit} from './model';
import {desktopLibraryCount} from './desktop-library-fit';
import styles from './shell.module.css';
import {DesktopNavigationUnderline} from './DesktopNavigationUnderline';

const t = defaultI18n.t;
export function DesktopNavigation({libraries, liveAvailable, activeLibraryId, moreOpen, onMoreOpenChange}: {
  libraries: readonly Library[]; liveAvailable: boolean; activeLibraryId?: string; moreOpen: boolean; onMoreOpenChange: (open: boolean) => void;
}) {
  const destinations = useRef<HTMLUListElement>(null), measurements = useRef<HTMLLIElement>(null);
  const [visibleCount, setVisibleCount] = useState(desktopLibraryLimit);
  const navigate = useNavigate();
  useLayoutEffect(() => {
    const list = destinations.current, ruler = measurements.current;
    if (!list || !ruler) return;
    let disposed = false;
    const measure = () => {
      if (disposed) return;
      const labels = Array.from(ruler.children, element => element.getBoundingClientRect().width);
      const count = desktopLibraryCount({available: list.getBoundingClientRect().width, home: labels[0], more: labels[1],
        live: liveAvailable ? labels[2] : undefined, libraries: labels.slice(3), gap: Number.parseFloat(getComputedStyle(list).columnGap) || 0});
      setVisibleCount(previous => previous === count ? previous : count);
    };
    const observer = new ResizeObserver(measure);
    observer.observe(list);
    for (const label of ruler.children) observer.observe(label);
    measure();
    void document.fonts.ready.then(measure);
    return () => { disposed = true; observer.disconnect(); };
  }, [libraries, liveAvailable]);
  const visible = libraries.slice(0, visibleCount), overflow = libraries.slice(visibleCount);
  return <ul ref={destinations} className={styles.destinations} data-desktop-destinations>
    <li data-window-no-drag><Link to="/" activeProps={{'aria-current': 'page'}} activeOptions={{exact: true}}>{t('next.home.shell.home')}</Link></li>
    {visible.map(library => <li key={library.id} data-window-no-drag><Link to="/library/$libraryId" params={{libraryId: library.id}} aria-current={activeLibraryId === library.id ? 'page' : undefined}>{library.name}</Link></li>)}
    {overflow.length > 0 && <li data-window-no-drag><Menu className={styles.libraryMenu} title={t('next.home.shell.libraries')} open={moreOpen} onOpenChange={onMoreOpenChange}
      trigger={<button type="button" className={styles.more} data-active={overflow.some(library => library.id === activeLibraryId)}>{t('next.home.shell.more')}<Icon name="i-down" size={14} /></button>}
      items={overflow.map(library => ({id: library.id, label: library.name, selected: library.id === activeLibraryId, onSelect: () => { void navigate({to: '/library/$libraryId', params: {libraryId: library.id}}); }}))} /></li>}
    {liveAvailable && <li data-window-no-drag><Link to="/live" activeProps={{'aria-current': 'page'}}>{t('next.home.shell.live')}</Link></li>}
    {/* Noninteractive intrinsic text measurements never enter navigation or the accessibility tree. */}
    <li ref={measurements} className={styles.destinationMeasurements} aria-hidden="true">
      <span className={styles.destinationMeasure}>{t('next.home.shell.home')}</span>
      <span className={styles.destinationMeasure}>{t('next.home.shell.more')}<Icon name="i-down" size={14} /></span>
      <span className={styles.destinationMeasure}>{t('next.home.shell.live')}</span>
      {libraries.map(library => <span className={styles.destinationMeasure} key={library.id}>{library.name}</span>)}
    </li>
    <DesktopNavigationUnderline list={destinations} />
  </ul>;
}
