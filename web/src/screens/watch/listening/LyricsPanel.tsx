import {useEffect, useRef} from 'react';
import {defaultI18n} from '@i18n';
import {activeLyricLine, lyricSeekPosition, type LyricsSnapshot, type LyricRevision} from '@core/lyrics.ts';
import {Button} from '../../../kit/controls';
import {StateView} from '../../../kit/states';
import {Banner} from '../../../kit/overlays';
import styles from './listening-panel.module.css';
const t = defaultI18n.t;

function prefersReducedMotion(): boolean {
  if (typeof globalThis.matchMedia === 'function' && globalThis.matchMedia('(prefers-reduced-motion: reduce)').matches) return true;
  if (typeof globalThis.document !== 'undefined' && globalThis.document.documentElement?.getAttribute?.('data-reduce-motion') === 'true') return true;
  return false;
}

/** A pure view of the captured service snapshot. Original host callbacks retain
 * their source/authority/opening; this component never loads or owns a service. */
export function LyricsPanel({snapshot, positionSeconds, enabled, canSeek, onRetry, onChoose, onSeek, librarySettings, lyricsSource}: Readonly<{
  snapshot: LyricsSnapshot; positionSeconds: number; enabled: boolean; canSeek: boolean;
  onRetry: (shown: LyricsSnapshot) => void;
  onChoose?: (shown: LyricsSnapshot, resource: LyricRevision | null) => void;
  onSeek: (shown: LyricsSnapshot, index: number) => void;
  /** Already permission-checked by the original owner; absent means no action. */
  librarySettings?: () => void;
  /** The owner's library's lyrics source, once known. Only 'files' (no online lookup) earns the note. */
  lyricsSource?: 'files' | 'lrclib' | null;
}>) {
  const data = enabled ? snapshot.data : undefined, selected = data?.selection.resource, lyricDoc = selected?.document, source = data?.source;
  const active = enabled && lyricDoc && selected && source && Number.isFinite(positionSeconds)
    ? activeLyricLine(lyricDoc, positionSeconds, selected.offsetMs, source.startSeconds) : -1;
  const sectionRef = useRef<HTMLElement>(null);
  const activeLineRef = useRef<HTMLElement | null>(null);
  const manualScrollUntil = useRef(0);
  const lastScrolled = useRef<{key: string; index: number} | null>(null);
  const resourceKey = selected ? `${selected.id}:${selected.revision}` : '';
  useEffect(() => {
    manualScrollUntil.current = 0;
    lastScrolled.current = null;
    const container = sectionRef.current;
    if (!enabled || !container || typeof container.addEventListener !== 'function') return;
    const pause = () => { manualScrollUntil.current = Date.now() + 4000; };
    container.addEventListener('wheel', pause, {passive: true});
    container.addEventListener('touchstart', pause, {passive: true});
    return () => {
      container.removeEventListener('wheel', pause);
      container.removeEventListener('touchstart', pause);
    };
  }, [enabled, resourceKey]);
  useEffect(() => {
    if (!enabled || active < 0) return;
    if (lastScrolled.current?.key === resourceKey && lastScrolled.current.index === active) return;
    lastScrolled.current = {key: resourceKey, index: active};
    if (Date.now() < manualScrollUntil.current) return;
    const node = activeLineRef.current;
    if (!node || typeof node.scrollIntoView !== 'function') return;
    try {
      node.scrollIntoView({behavior: prefersReducedMotion() ? 'auto' : 'smooth', block: 'center', inline: 'nearest'});
    } catch {
      node.scrollIntoView();
    }
  }, [enabled, resourceKey, active]);
  if (!enabled) return null;
  const loading = snapshot.busy && !data || snapshot.generation === 0;
  return <section ref={sectionRef} className={styles.lyrics} aria-label={t('player.lyrics')}>
    {snapshot.error && <Banner action={{label: t('next.states.tryAgain'), onAction: () => onRetry(snapshot)}}>{t('next.states.pageFailedBody')}</Banner>}
    {loading ? <p role="status">{t('next.settings.loading')}</p> : !lyricDoc && !snapshot.error ? <div className={styles.empty}>
      <StateView title={t('player.noLyrics')} body={t('player.noLyricsBody')}/>
      {librarySettings && <div className={styles.owner}>
        {lyricsSource === 'files' && <span><b>{t('player.lyricsOwner')}</b><br/>{t('player.lyricsNotFetched')}</span>}
        <Button variant="glass" size="small" onClick={librarySettings}>{t('next.browse.librarySettings')}</Button>
      </div>}
    </div> : lyricDoc ? <div className={styles.lines}>
      {lyricDoc.lines.map((line, index) => {
        const seekable = canSeek && !snapshot.busy && source && line.atMs !== null && lyricSeekPosition(line, selected!.offsetMs, source) !== null;
        const setRef = index === active ? (node: HTMLElement | null) => { activeLineRef.current = node; } : undefined;
        return seekable ? <button ref={setRef} type="button" key={index} className={styles.line} data-active={index === active || undefined}
          aria-current={index === active ? 'true' : undefined} onClick={() => onSeek(snapshot, index)}>{line.text}</button>
          : <p ref={setRef} key={index} className={styles.line} data-active={index === active || undefined}>{line.text}</p>;
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
