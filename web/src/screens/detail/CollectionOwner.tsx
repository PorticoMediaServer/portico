import {useEffect, useRef, useState} from 'react';
import {editLibraryCollection, searchLibraryFilms, type CollectionCandidate} from '@core/library-collection-edit.ts';
import {Button, TextField} from '../../kit/controls';
import {Dialog} from '../../kit/overlays';
import {Artwork} from '../../kit/artwork';
import {ResultRow} from '../../kit/search';
import type {ScreenContext} from './context';
import {t} from './text';
import styles from './collection-owner.module.css';

type Api = Parameters<typeof editLibraryCollection>[0];
/** desktop-detail f8: the owner's edits to a library's collection: rename it, add titles, and put titles in order. Each
 * is safe to send twice; the page reads the collection again after it lands. */
export const editCollection = editLibraryCollection;

/** Rename: the collection's name in a field, Save sends it. */
export function RenameCollection({open, name, onOpenChange, onSave}: Readonly<{open: boolean; name: string; onOpenChange: (open: boolean) => void; onSave: (name: string) => Promise<void>}>) {
  const [value, setValue] = useState(name), [busy, setBusy] = useState(false);
  useEffect(() => {if (open) setValue(name);}, [open, name]);
  const trimmed = value.trim();
  const save = () => {if (!trimmed || trimmed === name || busy) return; setBusy(true); void onSave(trimmed).finally(() => setBusy(false));};
  return <Dialog open={open} onOpenChange={onOpenChange} title={t('next.collection.renameTitle')} footerDismisses
    footer={<><Button variant="ghost" onClick={() => onOpenChange(false)}>{t('action.cancel')}</Button><Button variant="primary" loading={busy} disabled={!trimmed || trimmed === name} onClick={save}>{t('action.save')}</Button></>}>
    <form onSubmit={event => {event.preventDefault(); save();}}><TextField autoFocus label={t('next.collection.name')} value={value} maxLength={200} onChange={event => setValue(event.currentTarget.value)}/></form>
  </Dialog>;
}

type Found = CollectionCandidate;
/** Add a film: a search of this library's films that are not in the collection yet; Add sends each one. */
export function AddToCollection({open, api, libraryId, held, artwork, onOpenChange, onAdd}: Readonly<{open: boolean; api: Api; libraryId: string; held: ReadonlySet<string>;
  artwork: ScreenContext['artwork']; onOpenChange: (open: boolean) => void; onAdd: (id: string) => Promise<void>}>) {
  const [query, setQuery] = useState(''), [found, setFound] = useState<readonly Found[]>(), [added, setAdded] = useState<ReadonlySet<string>>(new Set()), [busy, setBusy] = useState<string>();
  const latest = useRef(0);
  useEffect(() => {if (open) {setQuery(''); setFound(undefined); setAdded(new Set());}}, [open]);
  useEffect(() => {
    const q = query.trim(); if (!open || q.length < 2) {setFound(undefined); return;}
    const id = ++latest.current, request = new AbortController();
    const timer = setTimeout(() => {
      void searchLibraryFilms(api, q, libraryId, request.signal).then(films => {
        if (latest.current === id) setFound(films);
      }, () => {if (latest.current === id) setFound([]);});
    }, 250);
    return () => {clearTimeout(timer); request.abort();};
  }, [open, query, api, libraryId]);
  const add = (id: string) => {if (busy) return; setBusy(id); void onAdd(id).then(() => setAdded(before => new Set([...before, id])), () => {}).finally(() => setBusy(undefined));};
  const shown = found?.filter(film => !held.has(film.id) || added.has(film.id));
  return <Dialog open={open} onOpenChange={onOpenChange} title={t('next.collection.addFilm')} description={t('next.collection.addFilmHelp')}>
    <TextField autoFocus type="search" label={t('next.collection.searchFilms')} value={query} onChange={event => setQuery(event.currentTarget.value)}/>
    <div className={styles.found}>
      {shown?.length === 0 && <p className={styles.none}>{t('next.collection.noFilmsFound')}</p>}
      {shown?.map(film => <ResultRow key={film.id} art={<Artwork source={artwork.foreground(film.posterUrl, 112, 2 / 3)} alt=""/>} title={film.title} facts={film.year ? <span>{film.year}</span> : undefined}
        onPress={() => {if (!added.has(film.id)) add(film.id);}}
        more={added.has(film.id) ? <span className={styles.added}>{t('next.collection.added')}</span> : <Button size="small" loading={busy === film.id} disabled={!!busy} onClick={() => add(film.id)}>{t('next.collection.add')}</Button>}/>)}
    </div>
  </Dialog>;
}
