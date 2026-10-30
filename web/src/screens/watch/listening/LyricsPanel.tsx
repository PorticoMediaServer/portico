import {defaultI18n} from '@i18n';
import {activeLyricLine, lyricSeekPosition, type LyricsSnapshot, type LyricRevision} from '@core/lyrics.ts';
import {Button} from '../../../kit/controls';
import {StateView} from '../../../kit/states';
import {Banner} from '../../../kit/overlays';
import styles from './listening-panel.module.css';
const t = defaultI18n.t;

/** A pure view of the captured service snapshot. Original host callbacks retain
 * their source/authority/opening; this component never loads or owns a service. */
export function LyricsPanel({snapshot, positionSeconds, enabled, canSeek, onRetry, onChoose, onSeek, librarySettings}: Readonly<{
  snapshot: LyricsSnapshot; positionSeconds: number; enabled: boolean; canSeek: boolean;
  onRetry: (shown: LyricsSnapshot) => void;
  onChoose?: (shown: LyricsSnapshot, resource: LyricRevision | null) => void;
  onSeek: (shown: LyricsSnapshot, index: number) => void;
  /** Already permission-checked by the original owner; absent means no action. */
  librarySettings?: () => void;
}>) {
  if (!enabled) return null;
  const data = snapshot.data, selected = data?.selection.resource, document = selected?.document, source = data?.source;
  const active = document && selected && source && Number.isFinite(positionSeconds)
    ? activeLyricLine(document, positionSeconds, selected.offsetMs, source.startSeconds) : -1;
  const loading = snapshot.busy && !data || snapshot.generation === 0;
  return <section className={styles.lyrics} aria-label={t('player.lyrics')}>
    {snapshot.error && <Banner action={{label: t('next.states.tryAgain'), onAction: () => onRetry(snapshot)}}>{t('next.states.pageFailedBody')}</Banner>}
    {loading ? <p role="status">{t('next.settings.loading')}</p> : !document && !snapshot.error ? <div className={styles.empty}>
      <StateView title={t('player.noLyrics')}/>
      {librarySettings && <Button variant="glass" onClick={librarySettings}>{t('next.browse.librarySettings')}</Button>}
    </div> : document ? <div className={styles.lines}>
      {document.lines.map((line, index) => {
        const seekable = canSeek && !snapshot.busy && source && line.atMs !== null && lyricSeekPosition(line, selected!.offsetMs, source) !== null;
        return seekable ? <button type="button" key={index} className={styles.line} data-active={index === active || undefined}
          aria-current={index === active ? 'true' : undefined} onClick={() => onSeek(snapshot, index)}>{line.text}</button>
          : <p key={index} className={styles.line} data-active={index === active || undefined}>{line.text}</p>;
      })}
    </div> : null}
    {!!data?.resources.length && onChoose && <fieldset className={styles.resources} disabled={snapshot.busy}>
      <legend>{t('player.chooseLyrics')}</legend>
      {data.resources.map(resource => <button type="button" className={styles.resource} key={`${resource.id}:${resource.revision}`}
        aria-pressed={resource.id === selected?.id && resource.revision === selected?.revision} onClick={() => onChoose(snapshot, resource)}>
        <b>{resource.language || resource.provenance.label}</b><small>{resource.provenance.label}</small>
      </button>)}
    </fieldset>}
  </section>;
}
