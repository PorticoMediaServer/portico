import {useEffect, useState} from 'react';
import {useSearch} from '@tanstack/react-router';
import {defaultI18n} from '@i18n';
import {loadGalleryFamilies, type GalleryFamily} from './gallery-registry';
import type {GallerySearch} from '../platform/gallery-search';
import styles from '../gallery.module.css';

export function KitGallery() {
  const search = useSearch({strict: false}) as GallerySearch;
  const [families, setFamilies] = useState<readonly GalleryFamily[]>([]);
  const [ready, setReady] = useState(false);
  const captureFamily = search.capture ? search.family ?? '' : undefined;
  useEffect(() => { let current = true; setReady(false); void loadGalleryFamilies(captureFamily).then(value => { if (current) { setFamilies(value); setReady(true); } }); return () => { current = false; }; }, [captureFamily]);
  const family = families.find(item => item.id === search.family) ?? (search.capture ? undefined : families[0]);
  const specimen = family?.cases.find(item => item.id === search.case) ?? (search.capture ? undefined : family?.cases[0]);
  const width = search.width ?? specimen?.width ?? 1440;
  const height = search.height ?? specimen?.height ?? 900;
  const Render = specimen?.render;
  const go = (familyId: string, caseId?: string) => {
    const url = new URL(location.href);
    url.searchParams.set('family', familyId);
    if (caseId) url.searchParams.set('case', caseId); else url.searchParams.delete('case');
    location.assign(url);
  };
  return <main className={search.capture ? styles.capture : styles.gallery} data-gallery-ready={ready || undefined}>
    {!search.capture && <header className={styles.header}>
      <h1>{defaultI18n.t('next.platform.kitTitle')}</h1>
      {family && <label>{defaultI18n.t('next.platform.galleryFamily')}<select value={family.id} onChange={event => go(event.target.value)}>{families.map(item => <option key={item.id} value={item.id}>{defaultI18n.t(item.labelKey)}</option>)}</select></label>}
      {specimen && <label>{defaultI18n.t('next.platform.galleryCase')}<select value={specimen.id} onChange={event => go(family!.id, event.target.value)}>{family!.cases.map(item => <option key={item.id} value={item.id}>{defaultI18n.t(item.labelKey)}</option>)}</select></label>}
    </header>}
    <div data-gallery-capture className={styles.surface} style={{width, height}}>{Render && <Render />}</div>
  </main>;
}
