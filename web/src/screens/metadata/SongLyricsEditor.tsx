import {useCallback, useEffect, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import {fetchSongLyrics, pasteSongLyrics, readSongLyrics, type Lyrics} from '@core/metadata-lyrics.ts';
import type {ServerRequester} from '@core/query/server-api.ts';
import {Button, TextField} from '../../kit/controls';
import {MetadataError} from './MetadataError';
import styles from './metadata.module.css';

const t = defaultI18n.t;
const ORIGINS: Readonly<Record<string, 'web.metadata.lyricsFromPaste' | 'web.metadata.lyricsFromFile' | 'web.metadata.lyricsFromProvider'>> = {pasted: 'web.metadata.lyricsFromPaste', tags: 'web.metadata.lyricsFromFile', provider: 'web.metadata.lyricsFromProvider'};
/** The song editor's Lyrics tab (area 4 rule 64): the lyrics everyone sees, Fetch lyrics, and lyrics the owner pastes. */
export function SongLyricsEditor({api, songId}: {api: ServerRequester; songId: string}) {
  const [lyrics, setLyrics] = useState<readonly Lyrics[]>(), [busy, setBusy] = useState(true), [error, setError] = useState<unknown>(), [notice, setNotice] = useState('');
  const [text, setText] = useState(''), [language, setLanguage] = useState('');
  // Only this song's newest answer lands; one for a song that is no longer open is dropped.
  const generation = useRef(0);
  const run = useCallback(async (work: () => Promise<readonly Lyrics[] | string>) => {
    const mine = ++generation.current;
    setBusy(true); setError(undefined); setNotice('');
    try {
      const answer = await work();
      if (mine !== generation.current) return;
      if (typeof answer === 'string') {setNotice(answer); setLyrics(await readSongLyrics(api, songId));} else setLyrics(answer);
    } catch (value) {if (mine === generation.current) setError(value);}
    finally {if (mine === generation.current) setBusy(false);}
  }, [api, songId]);
  useEffect(() => {
    const reads = generation;
    void run(() => readSongLyrics(api, songId));
    return () => {reads.current++;};
  }, [api, songId, run]);
  const shown = lyrics?.[0];
  const tooLong = new TextEncoder().encode(text).length > 262144, badLanguage = !!language.trim() && !/^[A-Za-z]{2,3}$/.test(language.trim());
  return <div className={styles.fields}>
    <p className={styles.lockHelp}>{t('web.metadata.lyricsHelp')}</p>
    {error ? <MetadataError error={error}/> : null}
    {notice ? <p role="status">{notice}</p> : null}
    {lyrics && !shown ? <p>{t('web.metadata.lyricsNone')}</p> : null}
    {shown ? <section className={styles.candidate}><div className={styles.matchDetails}>
      <b>{t(ORIGINS[shown.origin] ?? 'web.metadata.lyricsFromFile')}</b>
      <small>{[t(shown.format === 'lrc' ? 'web.metadata.lyricsTimed' : 'web.metadata.lyricsPlain'), shown.credit].filter(Boolean).join(' · ')}</small>
      <pre className={styles.lyricPreview}>{shown.text}</pre></div></section> : null}
    <div className={styles.editorFooter}><Button size="small" disabled={busy} onClick={() => void run(async () => t(await fetchSongLyrics(api, songId) ? 'web.metadata.lyricsFetched' : 'web.metadata.lyricsNotFound'))}>{t('web.metadata.lyricsFetch')}</Button></div>
    <section className={styles.fields}>
      <h3>{t('web.metadata.lyricsAdd')}</h3>
      <TextField label={t('web.metadata.lyricsLanguage')} description={t('web.metadata.lyricsLanguageHelp')} value={language} disabled={busy} maxLength={3}
        onChange={event => setLanguage(event.currentTarget.value)} error={badLanguage ? t('web.metadata.lyricsLanguageHelp') : undefined}/>
      <label className={styles.lyricText}>{t('web.metadata.tabLyrics')}<textarea value={text} disabled={busy} placeholder={t('web.metadata.lyricsPasteHelp')} onChange={event => setText(event.currentTarget.value)}/></label>
      <div className={styles.editorFooter}><Button size="small" variant="primary" disabled={busy || !text.trim() || tooLong || badLanguage} onClick={() => void run(async () => {const next = await pasteSongLyrics(api, songId, text, language); setText(''); return next;})}>{t('web.metadata.lyricsSave')}</Button></div>
    </section>
  </div>;
}
