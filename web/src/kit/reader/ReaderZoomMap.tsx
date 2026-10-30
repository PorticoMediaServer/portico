import {useEffect, useState, type ReactNode} from 'react';
import type {ReaderFixedViewport} from '@portico/reader-core';
import styles from './reader.module.css';

/** phone-read 26 / tablet-read 9: while a fixed page is zoomed past fit, a small
 * map of the page shows the visible part; the zoom level shows briefly on change. */
export function ReaderZoomMap({viewport, page, percent, hint, presentation}: Readonly<{
  viewport: ReaderFixedViewport; page: ReactNode; percent: string; hint?: string;
  presentation: 'phone' | 'tablet' | 'desktop';
}>) {
  const [announced, setAnnounced] = useState<number>();
  useEffect(() => {
    setAnnounced(viewport.scale);
    const timer = setTimeout(() => setAnnounced(undefined), 1600);
    return () => clearTimeout(timer);
  }, [viewport.scale]);
  const fraction = (value: number) => `${Math.round(Math.min(1, Math.max(0, value)) * 10000) / 100}%`;
  const view = {left: fraction(viewport.x), top: fraction(viewport.y), width: fraction(viewport.width), height: fraction(viewport.height)};
  if (presentation === 'phone') return <div className={styles.zoomLayer} data-reader-zoom aria-hidden="true">
    <span className={styles.zoomPill} data-shown={announced === viewport.scale || undefined}>{percent}{hint && <span>{hint}</span>}</span>
    <span className={styles.pageMap}>{page}<i style={view}/></span>
  </div>;
  return <div className={styles.zoomLens} data-reader-zoom aria-hidden="true">
    <div className={styles.lensMap}>{page}<span style={view}/></div>
    <p><b>{percent}</b>{hint && <> {hint}</>}</p>
  </div>;
}
