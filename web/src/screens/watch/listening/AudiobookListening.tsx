import type {ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import type {PlayerChapter, PlayerChaptersSnapshot} from '@core/player-chapters.ts';
import type {WebPlaybackRuntimeSnapshot} from '../../../platform/playback';
import type {AmbientArtSource, ArtworkSource} from '../../../kit/artwork';
import {AmbientArt, Artwork} from '../../../kit/artwork';
import {Button, Segmented, Slider} from '../../../kit/controls';
import {Banner} from '../../../kit/overlays';
import {StateView} from '../../../kit/states';
import {Icon} from '../../../kit/Icon';
import type {WatchBindings} from '../types';
import {watchClock, watchSeek, watchSeekBounds, type WatchIntent} from '../model';
import {ListeningSeek} from './ListeningSeek';
import ambientStyles from './listening-ambient.module.css';
import styles from './audiobook-listening.module.css';
const t = defaultI18n.t;

/** Authored audiobook view of an existing local host. It mounts no media,
 * service, timer or runtime. Source-bearing callbacks carry the drawn witness. */
export function AudiobookListening({snapshot, title, subtitle, cover, ambient, chapters, speedChoices, sleepChoices, sleepMinutes, skips, phone = false, sourceLabel, onShowPanel,canPlayOn,
  onIntent, onSpeed, onSleep, onChapter, onChapterPage, onChapterRetry, onCollapse, aside, notice}: Readonly<{
  snapshot: WebPlaybackRuntimeSnapshot; title: string; subtitle: ReactNode; cover?: ArtworkSource; ambient?: AmbientArtSource;
  skips: WatchBindings['skips']; phone?: boolean; sourceLabel?: string;canPlayOn?:boolean;
  onShowPanel?: (shown: WebPlaybackRuntimeSnapshot, panel: 'speed' | 'sleep' | 'chapters' | 'play-on' | 'options') => void;
  chapters: PlayerChaptersSnapshot | null; speedChoices: readonly number[]; sleepChoices: readonly number[]; sleepMinutes: number;
  onIntent: (shown: WebPlaybackRuntimeSnapshot, intent: WatchIntent) => void;
  onSpeed: (shown: WebPlaybackRuntimeSnapshot, rate: number) => void;
  onSleep: (shown: WebPlaybackRuntimeSnapshot, minutes: number) => void;
  onChapter: (shown: PlayerChaptersSnapshot, chapter: PlayerChapter) => void;
  onChapterPage: (shown: PlayerChaptersSnapshot, direction: 'next' | 'previous') => void;
  onChapterRetry: (shown: PlayerChaptersSnapshot) => void;
  onCollapse: () => void; aside?: ReactNode; notice?: ReactNode;
}>) {
  const p = snapshot.presentation;
  if (!p || p.kind !== 'audiobook' || !p.expanded || snapshot.disposed || snapshot.dock.remote?.phase === 'playing') return null;
  const bounds = watchSeekBounds(snapshot), can = (kind: typeof p.controls[number]) => p.controls.includes(kind);
  const rows = chapters?.data?.status === 'available' ? chapters.data.chapters : [];
  const active = rows.find(chapter => p.positionSeconds >= chapter.startSeconds && p.positionSeconds < chapter.endSeconds);
  const chapterIndex = active ? rows.indexOf(active) : -1, previous = rows[chapterIndex - 1], next = rows[chapterIndex + 1];
  const seek = (position: number) => {const seconds = watchSeek(snapshot, position);if (seconds !== null) onIntent(snapshot, {kind: 'seek', seconds});};
  return <section className={styles.frame} aria-label={t('player.nowPlayingTitle', {title})} tabIndex={-1} data-watch-audiobook data-phone={phone || undefined}>
    <AmbientArt source={ambient} className={`${ambientStyles.ambient}${phone ? ` ${ambientStyles.phone}` : ''}`}/>
    <header className={styles.top}>
      <Button variant="glass" icon="i-down" aria-label={t('player.minimize')} onClick={onCollapse}/>
      {sourceLabel && <span>{t('next.music.playingFrom', {source: sourceLabel})}</span>}{!phone && notice}
      {onShowPanel && <Button variant="glass" icon="i-more" aria-label={t('player.opt.menu')} onClick={() => onShowPanel(snapshot, 'options')}/>}
    </header>
    <div className={styles.main}>
      <div className={styles.bookTop}><div className={styles.cover}><Artwork source={cover} alt={title}/></div><div className={styles.identity}><h1>{title}</h1><p>{subtitle}</p></div></div>
      {active && <div className={styles.chapter}><b>{active.title}</b><span>{watchClock(p.positionSeconds - active.startSeconds)} / {watchClock(active.endSeconds - active.startSeconds)}</span></div>}
      <ListeningSeek className={styles.scrub} position={p.positionSeconds} duration={p.durationSeconds} bounds={bounds}
        chapters={rows} disabled={!can('seek')} phone={phone} onSeek={seek}/>
      <div className={styles.transport}>
        <Button variant="ghost" className={styles.control} iconOnly iconSize={phone ? 28 : 24} icon="i-prev" aria-label={t('next.dock.previous')} disabled={!chapters || !previous || !can('seek')} onClick={() => {if (chapters && previous) onChapter(chapters, previous);}}/>
        <button type="button" className={styles.skip} aria-label={t('next.dock.back', {seconds: skips.back})} disabled={!can('seek') || !bounds} onClick={() => seek(p.positionSeconds - skips.back)}><Icon name="i-back" size={phone ? 38 : 34}/><span>{skips.back}</span></button>
        <Button variant="primary" className={styles.playPause} data-playback-primary iconOnly iconSize={phone ? 34 : 30} icon={p.playing ? 'i-pause' : 'i-play'} aria-label={t(p.playing ? 'next.dock.pause' : 'next.dock.play')}
          disabled={!can(p.playing ? 'pause' : 'play')} onClick={() => onIntent(snapshot, {kind: p.playing ? 'pause' : 'play'})}/>
        <button type="button" className={styles.skip} aria-label={t('next.dock.forward', {seconds: skips.forward})} disabled={!can('seek') || !bounds} onClick={() => seek(p.positionSeconds + skips.forward)}><Icon name="i-fwd" size={phone ? 38 : 34}/><span>{skips.forward}</span></button>
        <Button variant="ghost" className={styles.control} iconOnly iconSize={phone ? 28 : 24} icon="i-next" aria-label={t('next.dock.next')} disabled={!chapters || !next || !can('seek')} onClick={() => {if (chapters && next) onChapter(chapters, next);}}/>
      </div>
      {phone && notice && <div className={styles.notice}>{notice}</div>}
      {!phone && <div className={styles.options}>
        {!!speedChoices.length && <div className={styles.option}><b>{t('player.opt.speed')}</b><Segmented className={styles.segments} aria-label={t('player.opt.speed')} value={String(p.rate)} items={speedChoices.map(rate => ({value: String(rate), label: `${defaultI18n.region.formatLocale ? new Intl.NumberFormat(defaultI18n.region.formatLocale).format(rate) : rate}×`}))}
          onValueChange={value => {const rate = Number(value);if (speedChoices.includes(rate)) onSpeed(snapshot, rate);}}/></div>}
        {!!sleepChoices.length && <div className={styles.option}><b>{t('player.opt.sleepTimer')}</b><Segmented className={styles.segments} aria-label={t('player.opt.sleepTimer')} value={String(sleepMinutes)}
          items={sleepChoices.map(minutes => ({value: String(minutes), label: minutes === 0 ? t('player.opt.off') : t('duration.minutes', {minutes})}))}
          onValueChange={value => {const minutes = Number(value);if (sleepChoices.includes(minutes)) onSleep(snapshot, minutes);}}/></div>}
      </div>}
      {!phone && can('volume') && <div className={styles.volume}><Button variant="ghost" icon="i-vol" aria-label={t(p.muted ? 'player.unmute' : 'player.mute')} onClick={() => onIntent(snapshot, {kind: 'mute'})}/>
        <Slider aria-label={t('player.volume')} min={0} max={1} step={.01} value={p.muted ? 0 : p.volume} onValueChange={volume => onIntent(snapshot, {kind: 'volume', volume})}/></div>}
      {phone && <div className={styles.phoneOptions}>
        <button type="button" disabled={!onShowPanel || !speedChoices.length} onClick={() => onShowPanel?.(snapshot, 'speed')}><b>{new Intl.NumberFormat(defaultI18n.region.formatLocale).format(p.rate)}×</b>{t('player.opt.speed')}</button>
        <button type="button" disabled={!onShowPanel || !sleepChoices.length} onClick={() => onShowPanel?.(snapshot, 'sleep')}><b><Icon name="i-moon" size={15}/>{sleepMinutes === 0 ? t('player.opt.off') : t('duration.minutes', {minutes: sleepMinutes})}</b>{t('player.opt.sleepTimer')}</button>
        <button type="button" disabled={!onShowPanel || !chapters} onClick={() => onShowPanel?.(snapshot, 'chapters')}><b><Icon name="i-chapters" size={16}/>{chapters?.data?.status === 'available' ? chapters.data.totalCount : null}</b>{t('player.opt.chapters')}</button>
        {onShowPanel && canPlayOn===true && <button type="button" onClick={() => onShowPanel(snapshot, 'play-on')}><b><Icon name="i-cast" size={17}/></b>{t('next.dock.playOn')}</button>}
      </div>}
    </div>
    {!phone && <aside className={styles.side} aria-label={t('player.opt.chapters')}>
      {aside ?? <>
        <h2>{t('player.opt.chapters')}</h2>
        {chapters?.phase === 'loading' || chapters?.phase === 'idle' ? <p role="status">{t('player.opt.loadingChapters')}</p>
          : chapters?.phase === 'error' || chapters?.phase === 'refresh-required' ? <Banner action={{label: t('next.states.tryAgain'), onAction: () => onChapterRetry(chapters)}}>{t('player.opt.chaptersUnavailable')}</Banner>
            : !chapters || chapters.data?.status === 'unavailable' ? <StateView title={t('player.opt.chaptersUnavailable')}/>
              : chapters.data?.status === 'none' ? <StateView title={t('player.opt.noChapters')}/> : null}
        <div role="list">{rows.map(chapter => <div role="listitem" key={chapter.id} className={styles.chapterRow} data-current={chapter === active || undefined}>
          <button type="button" disabled={!can('seek') || chapters?.phase !== 'ready'} onClick={() => {if (chapters) onChapter(chapters, chapter);}} aria-current={chapter === active ? 'true' : undefined}>
            <span>{chapter === active ? <Icon name={p.playing ? 'i-play' : 'i-pause'} size={16}/> : chapter.index}</span><b>{chapter.title}</b><small>{watchClock(chapter.endSeconds - chapter.startSeconds)}</small>
          </button>
        </div>)}</div>
        {chapters && <div className={styles.pager}>
          {chapters.pagination.canPrevious && <Button variant="glass" disabled={chapters.phase === 'loading'} onClick={() => onChapterPage(chapters, 'previous')}>{t('web.pager.previous')}</Button>}
          {chapters.data?.nextCursor && <Button variant="glass" loading={chapters.phase === 'loading'} onClick={() => onChapterPage(chapters, 'next')}>{t('next.live.more')}</Button>}
        </div>}
      </>}
    </aside>}
  </section>;
}
