import {defaultI18n} from '@i18n';
import type {MetadataLibraryChange, MetadataLibrarySettings} from '@core/metadata-settings.ts';
import {Switch} from '../../../kit/controls';
import styles from './metadata-settings.module.css';

const t = defaultI18n.t;
type Patch = Partial<Omit<MetadataLibraryChange, 'id' | 'expectedRevision'>>;
export function MetadataLibraries({libraries, changes, busy, onChange}: {libraries: readonly MetadataLibrarySettings[]; changes: Readonly<Record<string, MetadataLibraryChange>>; busy: boolean; onChange: (row: MetadataLibrarySettings, patch: Patch) => void}) {
  return <div className={styles.tableScroll}><table className={styles.libraryTable}>
    <caption>{t('metadata.settings.libraryTable')}</caption>
    <thead><tr><th scope="col">{t('metadata.settings.library')}</th><th scope="col">{t('metadata.settings.titlesSource')}</th><th scope="col">{t('web.metadata.tabLyrics')}</th><th scope="col">{t('metadata.settings.episodePictures')}</th><th scope="col">{t('metadata.settings.scrubPreviews')}</th></tr></thead>
    <tbody>{libraries.map(row => {
      const value = {...row, ...changes[row.id]}, disabled = busy || row.managed;
      const video = row.kind === 'movie' || row.kind === 'tv' || row.kind === 'anime';
      return <tr key={row.id}><th scope="row">{row.name}</th>
        <td data-label={t('metadata.settings.titlesSource')}><select aria-label={t('metadata.settings.sourceFor', {library: row.name})} value={value.agent} disabled={disabled || row.agentOptions.length < 2} onChange={event => {const agent = event.currentTarget.value; if (agent === 'local' || agent === 'online') onChange(row, {agent});}}>
          {row.agentOptions.map(agent => <option key={agent} value={agent}>{t(agent === 'local' ? 'metadata.settings.sourceLocal' : 'metadata.settings.sourceOnline')}</option>)}
        </select></td>
        <td data-label={t('web.metadata.tabLyrics')} data-applicable={row.kind === 'music'}>{row.kind === 'music' ? <select aria-label={t('metadata.settings.lyricsFor', {library: row.name})} value={value.agent === 'local' ? 'files' : value.lyricsSource} disabled={disabled || value.agent === 'local'} onChange={event => {const lyricsSource = event.currentTarget.value; if (lyricsSource === 'files' || lyricsSource === 'lrclib') onChange(row, {lyricsSource});}}>
          <option value="files">{t('metadata.settings.lyricsFiles')}</option><option value="lrclib">{t('metadata.settings.lyricsProvider')}</option>
        </select> : <span>{t('metadata.settings.notApplicable')}</span>}</td>
        <td data-label={t('metadata.settings.episodePictures')} data-applicable={row.kind === 'tv' || row.kind === 'anime'}>{row.kind === 'tv' || row.kind === 'anime' ? <select aria-label={t('metadata.settings.picturesFor', {library: row.name})} value={value.episodeThumbnailMode} disabled={disabled} onChange={event => {const episodeThumbnailMode = event.currentTarget.value; if (episodeThumbnailMode === 'provider_then_made' || episodeThumbnailMode === 'provider' || episodeThumbnailMode === 'off') onChange(row, {episodeThumbnailMode});}}>
          <option value="provider_then_made">{t('metadata.settings.providerThenMade')}</option><option value="provider">{t('metadata.settings.providerOnly')}</option><option value="off">{t('metadata.settings.off')}</option>
        </select> : <span>{t('metadata.settings.notApplicable')}</span>}</td>
        <td data-label={t('metadata.settings.scrubPreviews')} data-applicable={video}>{video ? <Switch aria-label={t('metadata.settings.scrubFor', {library: row.name})} checked={value.scrubPreviews} disabled={disabled || !row.canScrub} onCheckedChange={scrubPreviews => onChange(row, {scrubPreviews})}/> : <span>{t('metadata.settings.notApplicable')}</span>}</td>
      </tr>;
    })}</tbody>
  </table></div>;
}
