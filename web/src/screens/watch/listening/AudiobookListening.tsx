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
import {durationText} from '@core/presentation/kit-model/text.ts';
import {ListeningSeek} from './ListeningSeek';
import {BookChapterList, playingBookChapter} from './BookChapterList';
import ambientStyles from './listening-ambient.module.css';
import styles from './audiobook-listening.module.css';
import {sleepChoiceFrom, sleepChoiceLabel, sleepChoiceValue, type ListeningSleepChoice} from './listening-owner';
const t = defaultI18n.t;

/** Authored audiobook view of an existing local host. It mounts no media,
 * service, timer or runtime. Source-bearing callbacks carry the drawn witness. */
export function AudiobookListening({snapshot, title, subtitle, cover, ambient, chapters, speedChoices, sleepChoices, sleepChoice, skips, phone = false, sourceLabel, onShowPanel,canPlayOn,
  onIntent, onSpeed, onSleep, onChapter, onChapterPage, onChapterRetry, onCollapse, aside, notice}: Readonly<{
  snapshot: WebPlaybackRuntimeSnapshot; title: string; subtitle: ReactNode; cover?: ArtworkSource; ambient?: AmbientArtSource;
  skips: WatchBindings['skips']; phone?: boolean; sourceLabel?: string;canPlayOn?:boolean;
  onShowPanel?: (shown: WebPlaybackRuntimeSnapshot, panel: 'speed' | 'sleep' | 'chapters' | 'play-on' | 'options') => void;
  chapters: PlayerChaptersSnapshot | null; speedChoices: readonly number[]; sleepChoices: readonly ListeningSleepChoice[]; sleepChoice: ListeningSleepChoice;
  onIntent: (shown: WebPlaybackRuntimeSnapshot, intent: WatchIntent) => void;
  onSpeed: (shown: WebPlaybackRuntimeSnapshot, rate: number) => void;
  onSleep: (shown: WebPlaybackRuntimeSnapshot, choice: ListeningSleepChoice) => void;
  onChapter: (shown: PlayerChaptersSnapshot, chapter: PlayerChapter) => void;
  onChapterPage: (shown: PlayerChaptersSnapshot, direction: 'next' | 'previous') => void;
  onChapterRetry: (shown: PlayerChaptersSnapshot) => void;
  onCollapse: () => void; aside?: ReactNode; notice?: ReactNode;
}>) {
  const p = snapshot.presentation;
  if (!p || p.kind !== 'audiobook' || !p.expanded || snapshot.disposed || snapshot.dock.remote?.phase === 'playing') return null;
  const bounds = watchSeekBounds(snapshot), can = (kind: typeof p.controls[number]) => p.controls.includes(kind);
  const rows = chapters?.data?.status === 'available' ? chapters.data.chapters : [];
  // MU-2: the whole book's chapters when known, else the playing part's own.
  // Previous and Next move by chapter only when the book's list covers this part; else by part, as for a track.
  const book = snapshot.bookChapters?.phase === 'ready' ? snapshot.bookChapters : null;
  const active = book ? playingBookChapter(book) : rows.find(chapter => p.positionSeconds >= chapter.startSeconds && p.positionSeconds < chapter.endSeconds);
  const ticks = book ? book.chapters.filter(chapter => chapter.itemId === book.itemId) : rows;
  const seek = (position: number) => {const seconds = watchSeek(snapshot, position);if (seconds !== null) onIntent(snapshot, {kind: 'seek', seconds});};
  // desktop-listen, audiobook player: under Speed, the whole book's time left at this speed; under
  // Sleep timer, when it stops. Each is drawn only from facts the player has.
  // phone-app 15: the chapter count (the whole book's, else this part's), and the bar over the playing chapter.
  const chapterTotal = book ? book.chapters.length : chapters?.data?.status === 'available' ? chapters.data.totalCount : undefined;
  const chapterBar = phone && active && active.endSeconds > active.startSeconds ? {offset: active.startSeconds, duration: active.endSeconds, chapters: [],
    bounds: bounds && Math.min(bounds.end, active.endSeconds) > Math.max(bounds.start, active.startSeconds) ? {start: Math.max(bounds.start, active.startSeconds), end: Math.min(bounds.end, active.endSeconds)} : null} : undefined;
  const bookLeft = book && active && 'number' in active ? book.chapters.slice(book.current + 1).reduce((sum, chapter) => sum + (chapter.endSeconds - chapter.startSeconds), Math.max(0, active.endSeconds - p.positionSeconds)) : undefined;
  const speedLeft = bookLeft !== undefined && p.rate > 0 ? t('web.player.speedLeft', {time: durationText(bookLeft / p.rate, t) ?? '', rate: new Intl.NumberFormat(defaultI18n.region.formatLocale).format(p.rate)}) : undefined;
  const sleepAt = snapshot.playback?.listeningDeadlines?.sleepAtMs, sleepLeft = typeof sleepAt === 'number' ? Math.max(0, (sleepAt - Date.now()) / 1000) : undefined;
  const sleepStops = sleepLeft !== undefined && sleepLeft >= 60 ? t('web.player.sleepStops', {time: durationText(sleepLeft, t) ?? ''}) : undefined;
  return <section className={styles.frame} aria-label={t('player.nowPlayingTitle', {title})} tabIndex={-1} data-watch-audiobook data-phone={phone || undefined}>
    <AmbientArt source={ambient} className={`${ambientStyles.ambient}${phone ? ` ${ambientStyles.phone}` : ''}`}/>
    <header className={styles.top}>
      <Button variant={phone ? 'ghost' : 'glass'} icon="i-down" aria-label={t('player.minimize')} onClick={onCollapse}/>
      {/* phone-app 15: on a phone "Playing from" sits centred over its source, on two lines. */}
      {sourceLabel && (phone ? <span className={styles.from}><small>{t('web.player.playingFromLabel')}</small><b>{sourceLabel}</b></span>
        : <span>{t('next.music.playingFrom', {source: sourceLabel})}</span>)}{!phone && notice}
      {/* desktop-listen, audiobook player: Play on and More sit at the right, as in the music player. */}
      <span className={styles.topEnd}>
        {!phone && onShowPanel && canPlayOn === true && <Button variant="glass" icon="i-cast" aria-label={t('next.dock.playOn')} onClick={() => onShowPanel(snapshot, 'play-on')}/>}
        {onShowPanel && <Button variant={phone ? 'ghost' : 'glass'} icon="i-more" aria-label={t('player.opt.menu')} onClick={() => onShowPanel(snapshot, 'options')}/>}
      </span>
    </header>
    <div className={styles.main}>
      <div className={styles.bookTop}><div className={styles.cover}><Artwork source={cover} alt={title}/></div><div className={styles.identity}><h1>{title}</h1><p>{subtitle}</p></div></div>
      {/* desktop-listen 8: "Chapter 15 · 12:04 of 31:40". phone-app 15: "≡ Chapter 8 … of 19". */}
      {active && (phone ? <div className={styles.chapter}><b><Icon name="i-chapters" size={16}/>{active.title}</b>{chapterTotal !== undefined && <span>{t('player.chapterTotal', {total: chapterTotal})}</span>}</div>
        : <div className={styles.chapter}><b>{active.title}</b><span>{t('player.clockOf', {position: watchClock(Math.max(0, p.positionSeconds - active.startSeconds)), duration: watchClock(active.endSeconds - active.startSeconds)})}</span></div>)}
      {/* phone-app 15: on a phone the bar is the playing chapter's ("14:02 … −17:43"), with the whole
          book's time left between its times. Desktop keeps frame 8's bar over the playing part. */}
      <ListeningSeek className={styles.scrub} position={p.positionSeconds} disabled={!can('seek')} phone={phone} onSeek={seek}
        {...(chapterBar ? chapterBar : {duration: p.durationSeconds, bounds, chapters: ticks})}
        middle={phone && bookLeft !== undefined && bookLeft >= 60 ? t('web.player.bookLeft', {time: durationText(bookLeft, t) ?? ''}) : undefined}/>
      <div className={styles.transport}>
        <Button variant="ghost" className={styles.control} iconOnly iconSize={phone ? 28 : 24} icon="i-prev" aria-label={t(book ? 'next.dock.previousChapter' : 'next.dock.previous')} disabled={!can('previous')} onClick={() => onIntent(snapshot, {kind: 'previous'})}/>
        <button type="button" className={styles.skip} aria-label={t('next.dock.back', {seconds: skips.back})} disabled={!can('seek') || !bounds} onClick={() => seek(p.positionSeconds - skips.back)}><Icon name="i-back" size={phone ? 38 : 34}/><span>{skips.back}</span></button>
        <Button variant="primary" className={styles.playPause} data-playback-primary iconOnly iconSize={phone ? 34 : 30} icon={p.playing ? 'i-pause' : 'i-play'} aria-label={t(p.playing ? 'next.dock.pause' : 'next.dock.play')}
          disabled={!can(p.playing ? 'pause' : 'play')} onClick={() => onIntent(snapshot, {kind: p.playing ? 'pause' : 'play'})}/>
        <button type="button" className={styles.skip} aria-label={t('next.dock.forward', {seconds: skips.forward})} disabled={!can('seek') || !bounds} onClick={() => seek(p.positionSeconds + skips.forward)}><Icon name="i-fwd" size={phone ? 38 : 34}/><span>{skips.forward}</span></button>
        <Button variant="ghost" className={styles.control} iconOnly iconSize={phone ? 28 : 24} icon="i-next" aria-label={t(book ? 'next.dock.nextChapter' : 'next.dock.next')} disabled={!can('next')} onClick={() => onIntent(snapshot, {kind: 'next'})}/>
      </div>
      {phone && notice && <div className={styles.notice}>{notice}</div>}
      {!phone && <div className={styles.options}>
        {!!speedChoices.length && <div className={styles.option}><b>{t('player.opt.speed')}{speedLeft && <small>{speedLeft}</small>}</b><Segmented className={styles.segments} aria-label={t('player.opt.speed')} value={String(p.rate)} items={speedChoices.map(rate => ({value: String(rate), label: `${defaultI18n.region.formatLocale ? new Intl.NumberFormat(defaultI18n.region.formatLocale).format(rate) : rate}×`}))}
          onValueChange={value => {const rate = Number(value);if (speedChoices.includes(rate)) onSpeed(snapshot, rate);}}/></div>}
        {!!sleepChoices.length && <div className={styles.option}><b>{t('player.opt.sleepTimer')}{sleepStops && <small>{sleepStops}</small>}</b><Segmented className={styles.segments} aria-label={t('player.opt.sleepTimer')} value={sleepChoiceValue(sleepChoice)}
          items={sleepChoices.map(choice => ({value: sleepChoiceValue(choice), label: typeof choice === 'number' && choice > 0 ? t('next.kit.duration.minutes', {minutes: choice}) : sleepChoiceLabel(choice)}))}
          onValueChange={value => {const choice = sleepChoiceFrom(value);if (sleepChoices.includes(choice)) onSleep(snapshot, choice);}}/></div>}
      </div>}
      {!phone && can('volume') && <div className={styles.volume}><Button variant="ghost" icon="i-vol" aria-label={t(p.muted ? 'player.unmute' : 'player.mute')} onClick={() => onIntent(snapshot, {kind: 'mute'})}/>
        <Slider aria-label={t('player.volume')} min={0} max={1} step={.01} value={p.muted ? 0 : p.volume} onValueChange={volume => onIntent(snapshot, {kind: 'volume', volume})}/></div>}
      {phone && <div className={styles.phoneOptions}>
        <button type="button" disabled={!onShowPanel || !speedChoices.length} onClick={() => onShowPanel?.(snapshot, 'speed')}><b>{new Intl.NumberFormat(defaultI18n.region.formatLocale).format(p.rate)}×</b>{t('player.opt.speed')}</button>
        <button type="button" disabled={!onShowPanel || !sleepChoices.length} onClick={() => onShowPanel?.(snapshot, 'sleep')}><b><Icon name="i-moon" size={15}/>{typeof sleepChoice === 'number' && sleepChoice > 0 ? t('duration.minutes', {minutes: sleepChoice}) : sleepChoiceLabel(sleepChoice)}</b>{t('player.opt.sleepTimer')}</button>
        <button type="button" disabled={!onShowPanel || !chapters && !book} onClick={() => onShowPanel?.(snapshot, 'chapters')}><b><Icon name="i-chapters" size={16}/>{book ? book.chapters.length : chapters?.data?.status === 'available' ? chapters.data.totalCount : null}</b>{t('player.opt.chapters')}</button>
        {onShowPanel && canPlayOn===true && <button type="button" onClick={() => onShowPanel(snapshot, 'play-on')}><b><Icon name="i-cast" size={17}/></b>{t('next.dock.playOn')}</button>}
      </div>}
    </div>
    {!phone && <aside className={styles.side} aria-label={t('player.opt.chapters')}>
      {aside ?? <>
        <h2>{t('player.opt.chapters')}</h2>
        {book ? <BookChapterList book={book} playing={p.playing} positionSeconds={p.positionSeconds} disabled={!can('seek')} onPick={chapter => onIntent(snapshot, {kind: 'book-chapter', chapterId: chapter.id})}/> : <>
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
      </>}
    </aside>}
  </section>;
}
