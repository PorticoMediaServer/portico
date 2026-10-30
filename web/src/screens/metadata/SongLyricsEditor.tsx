import {useLayoutEffect, useMemo, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {MetadataLyricsService} from '@core/metadata-lyrics.ts';
import type {RepairData, RepairScope, RepairTarget} from '@core/metadata-repair.ts';
import {Button, TextField} from '../../kit/controls';
import {HostedError} from '../../kit/accounts/ServicesAuth';
import styles from './metadata.module.css';

const t = defaultI18n.t;
export function SongLyricsEditor({api, scope, target, repairData, isCurrent}: {
  api: ConstructorParameters<typeof MetadataLyricsService>[0]['api']; scope: RepairScope; target: RepairTarget;
  repairData: RepairData; isCurrent: (data: RepairData) => boolean;
}) {
  const service = useMemo(() => new MetadataLyricsService({api, scope, target, current: () => isCurrent(repairData)}), [api, scope, target, repairData, isCurrent]);
  const state = useSyncExternalStore(service.subscribe, service.getSnapshot);
  const [text, setText] = useState(''), [language, setLanguage] = useState(() => repairData.snapshot.fields.metadataLanguage?.value || defaultI18n.region.formatLocale.split('-')[0]);
  const [format, setFormat] = useState<'text' | 'lrc'>('text');
  useLayoutEffect(() => {void service.load(); return () => service.dispose();}, [service]);
  const data = state.data, busy = state.phase !== 'ready';
  const choice = data?.librarySelection;
  return <div className={styles.fields}>
    <p className={styles.lockHelp}>{t('web.metadata.lyricsHelp')}</p>
    {state.error ? <HostedError error={state.error}/> : null}
    {state.phase === 'loading' || state.phase === 'saving' ? <p role="status">{t(state.phase === 'loading' ? 'web.metadata.loading' : 'web.metadata.applying')}</p> : null}
    {state.phase === 'error' || state.phase === 'uncertain' ? <Button size="small" onClick={() => void service.load()}>{t('next.states.tryAgain')}</Button> : null}
    {data && !data.source ? <p>{t('web.metadata.lyricsNoSource')}</p> : null}
    {data?.source ? <>
      <p>{t(choice ? choice.resource ? 'web.metadata.lyricsChosen' : 'web.metadata.lyricsNoneChosen' : 'web.metadata.lyricsAutomatic')}</p>
      {choice?.resource?.document ? <pre className={styles.lyricPreview}>{choice.resource.document.text}</pre> : null}
      {data.resources.filter(resource => resource.scope === 'library' && !resource.deleted).map(resource => {
        const current = choice?.resource?.id === resource.id && choice.resource.revision === resource.revision;
        return <div key={resource.id} className={styles.candidate}>
          <div className={styles.matchDetails}><b>{resource.provenance.label || t('web.metadata.lyricsResource')}</b><small>{resource.language} · {resource.format.toUpperCase()}</small></div>
          {current ? <span>{t('web.metadata.currentMatch')}</span> : <Button size="small" disabled={busy} onClick={() => void service.choose(resource, data)}>{t('web.metadata.lyricsUse')}</Button>}
        </div>;
      })}
      <Button size="small" disabled={busy || Boolean(choice && !choice.resource)} onClick={() => void service.choose(null, data)}>{t('web.metadata.lyricsNone')}</Button>
      <section className={styles.fields}>
        <h3>{t('web.metadata.lyricsAdd')}</h3>
        <TextField label={t('web.metadata.lyricsLanguage')} value={language} disabled={busy} maxLength={63} onChange={event => setLanguage(event.currentTarget.value)}/>
        <label className={styles.picker}>{t('web.metadata.lyricsFormat')}<select value={format} disabled={busy} onChange={event => setFormat(event.currentTarget.value === 'lrc' ? 'lrc' : 'text')}>
          <option value="text">{t('web.metadata.lyricsPlain')}</option><option value="lrc">{t('web.metadata.lyricsTimed')}</option>
        </select></label>
        <label className={styles.lyricText}>{t('web.metadata.tabLyrics')}<textarea value={text} disabled={busy} maxLength={262144} onChange={event => setText(event.currentTarget.value)}/></label>
        <Button size="small" disabled={busy || !text.trim() || new TextEncoder().encode(text).length > 262144 || !/^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8})*$/.test(language)} onClick={() => void service.upload(text, language, format, data)}>{t('web.metadata.lyricsAdd')}</Button>
      </section>
    </> : null}
  </div>;
}
