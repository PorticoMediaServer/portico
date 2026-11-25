import type {ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import type {PlayerChapter, PlayerChaptersSnapshot} from '@core/player-chapters.ts';
import type {WebPlaybackRuntimeSnapshot} from '../../../platform/playback';
import type {AmbientArtSource, ArtworkSource} from '../../../kit/artwork';
import {generatedCoverTint} from '@design';
import {ArtworkGlow, GeneratedCover, WholeArtwork} from '../../../kit/artwork';
import {Button} from '../../../kit/controls';
import {AudioPlayerFrame, StateRow} from '../../../kit/frames';
import {AudioPanelSurface, PlayerVolume} from '../../../kit/player';
import {SeekLine} from '../../../kit/media';
import {Banner} from '../../../kit/overlays';
import {Icon} from '../../../kit/Icon';
import type {WatchBindings} from '../types';
import {watchClock, watchSeek, watchSeekBounds, type WatchIntent} from '../model';
import {durationText} from '@core/presentation/kit-model/text.ts';
import {BookChapterList, playingBookChapter} from './BookChapterList';
import styles from './audiobook-listening.module.css';
import {sleepChoiceLabel, sleepChoiceValue, type ListeningSleepChoice} from './listening-owner';
const t = defaultI18n.t;

/** Authored audiobook view of an existing local host. It mounts no media,
 * service, timer or runtime. Source-bearing callbacks carry the drawn witness. */
export function AudiobookListening({snapshot, title, subtitleParts = [], cover, ambient, chapters, speedChoices, sleepChoices, sleepChoice, skips, phone = false, sourceLabel, onShowPanel,canPlayOn,
  onIntent, onSpeed, onSleep, onChapter, onChapterPage, onChapterRetry, onCollapse, aside, notice}: Readonly<{
  snapshot: WebPlaybackRuntimeSnapshot; title: string; subtitle: ReactNode;
  /** The byline as words, for the frame (the author, then who reads it). */ subtitleParts?: readonly string[]; cover?: ArtworkSource; ambient?: AmbientArtSource;
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
  const rate = new Intl.NumberFormat(defaultI18n.region.formatLocale).format(p.rate);
  const position = chapterBar ? Math.max(0, p.positionSeconds - chapterBar.offset) : p.positionSeconds;
  const length = chapterBar ? chapterBar.duration - chapterBar.offset : p.durationSeconds;
  const lo = chapterBar?.bounds?.start ?? bounds?.start ?? 0, hi = chapterBar?.bounds?.end ?? bounds?.end ?? Math.max(p.durationSeconds, 1);
  const at = Math.min(hi, Math.max(lo, p.positionSeconds));
  const show = (panel: 'speed' | 'sleep' | 'chapters' | 'play-on' | 'options') => () => onShowPanel?.(snapshot, panel);
  // frame 8's book form (desktop-listen "book-player", phone-app 15): the kit's AudioPlayerFrame places every region; this view
  // hands it the book's facts and the listening owner's commands.
  return <section className={`portico-cover ${styles.frame}`} data-cover={ambient ? 'audio' : undefined} aria-label={t('player.nowPlayingTitle', {title})} tabIndex={-1} data-watch-audiobook data-phone={phone || undefined}>
    <ArtworkGlow source={ambient} phone={phone}/>
    <AudioPlayerFrame
      close={{icon: 'i-down', label: t('player.minimize'), onPress: onCollapse}}
      context={sourceLabel ? t('web.player.playingFromLabel') : undefined} source={sourceLabel}
      device={onShowPanel && canPlayOn === true ? {icon: 'i-cast', label: t('device.thisDevice'), onPress: show('play-on')} : undefined}
      more={{icon: 'i-more', label: t('player.opt.menu'), disabled: !onShowPanel, onPress: show('options')}}
      icon="i-book" cover={<ListeningCover cover={cover} title={title}/>}
      title={title} byline={subtitleParts}
      chapter={{label: active?.title ?? '', of: phone ? (chapterTotal !== undefined ? t('player.chapterTotal', {total: chapterTotal}) : '')
        : active ? t('player.clockOf', {position: watchClock(Math.max(0, p.positionSeconds - active.startSeconds)), duration: watchClock(active.endSeconds - active.startSeconds)}) : ''}}
      scrubber={<SeekLine variant="player" value={hi > lo ? (at - lo) / (hi - lo) : 0}
        marks={chapterBar || !(p.durationSeconds > 0) ? [] : ticks.map(chapter => ({id: String(chapter.startSeconds), kind: 'chapter' as const, start: chapter.startSeconds / p.durationSeconds, end: chapter.startSeconds / p.durationSeconds}))}
        input={{min: lo, max: hi, step: .1, value: at, disabled: !can('seek'), 'aria-label': t('player.position'),
          'aria-valuetext': t('player.clockOf', {position: watchClock(position), duration: watchClock(length)}),
          onChange: event => {const value = event.currentTarget.valueAsNumber; if (Number.isFinite(value)) seek(value);}}} />}
      times={{position: watchClock(position), end: `−${watchClock(Math.max(0, length - position))}`,
        ...(phone && bookLeft !== undefined && bookLeft >= 60 ? {middle: t('web.player.bookLeft', {time: durationText(bookLeft, t) ?? ''})} : {})}}
      transport={[
        {icon: 'i-prev', label: t(book ? 'next.dock.previousChapter' : 'next.dock.previous'), disabled: !can('previous'), onPress: () => onIntent(snapshot, {kind: 'previous'})},
        {icon: 'i-back', label: t('next.dock.back', {seconds: skips.back}), skip: skips.back, disabled: !can('seek') || !bounds, onPress: () => seek(p.positionSeconds - skips.back)},
        {icon: p.playing ? 'i-pause' : 'i-play', label: t(p.playing ? 'next.dock.pause' : 'next.dock.play'), disabled: !can(p.playing ? 'pause' : 'play'), onPress: () => onIntent(snapshot, {kind: p.playing ? 'pause' : 'play'})},
        {icon: 'i-fwd', label: t('next.dock.forward', {seconds: skips.forward}), skip: skips.forward, disabled: !can('seek') || !bounds, onPress: () => seek(p.positionSeconds + skips.forward)},
        {icon: 'i-next', label: t(book ? 'next.dock.nextChapter' : 'next.dock.next'), disabled: !can('next'), onPress: () => onIntent(snapshot, {kind: 'next'})},
      ]}
      volume={!phone && can('volume') ? <PlayerVolume value={p.volume} muted={p.muted} label={t('player.volume')} muteLabel={t(p.muted ? 'player.unmute' : 'player.mute')}
        onChange={volume => onIntent(snapshot, {kind: 'volume', volume})} onMute={() => onIntent(snapshot, {kind: 'mute'})} /> : undefined}
      speed={speedChoices.length ? {label: t('player.opt.speed'), value: `${rate}×`, ...(speedLeft ? {help: speedLeft} : {}), disabled: !onShowPanel && phone, onPress: show('speed'),
        choices: speedChoices.map(choice => ({label: `${new Intl.NumberFormat(defaultI18n.region.formatLocale).format(choice)}×`, selected: choice === p.rate, onPress: () => onSpeed(snapshot, choice)}))} : undefined}
      sleep={sleepChoices.length ? {icon: 'i-moon', label: t('player.opt.sleepTimer'), value: typeof sleepChoice === 'number' && sleepChoice > 0 ? t('duration.minutes', {minutes: sleepChoice}) : sleepChoiceLabel(sleepChoice),
        active: sleepChoice !== 0, ...(sleepStops ? {help: sleepStops} : {}), onPress: show('sleep'),
        choices: sleepChoices.map(choice => ({label: typeof choice === 'number' && choice > 0 ? t('next.kit.duration.minutes', {minutes: choice}) : sleepChoiceLabel(choice), selected: sleepChoiceValue(choice) === sleepChoiceValue(sleepChoice), onPress: () => onSleep(snapshot, choice)}))} : undefined}
      chapters={phone ? {icon: 'i-chapters', label: t('player.opt.chapters'), value: chapterTotal !== undefined ? String(chapterTotal) : '', disabled: !onShowPanel || !chapters && !book, onPress: show('chapters')} : undefined}
      panel={phone ? undefined : <AudioPanelSurface label={t('player.opt.chapters')}><div className={styles.side}>
        {aside ?? <>
        <h2>{t('player.opt.chapters')}</h2>
        {book ? <BookChapterList book={book} playing={p.playing} positionSeconds={p.positionSeconds} disabled={!can('seek')} onPick={chapter => onIntent(snapshot, {kind: 'book-chapter', chapterId: chapter.id})}/> : <>
        {chapters?.phase === 'loading' || chapters?.phase === 'idle' ? <p role="status">{t('player.opt.loadingChapters')}</p>
          : chapters?.phase === 'error' || chapters?.phase === 'refresh-required' ? <Banner action={{label: t('next.states.tryAgain'), onAction: () => onChapterRetry(chapters)}}>{t('player.opt.chaptersUnavailable')}</Banner>
            : !chapters || chapters.data?.status === 'unavailable' ? <StateRow kind="empty" icon="i-chapters" title={t('player.opt.chaptersUnavailable')}/>
              : chapters.data?.status === 'none' ? <StateRow kind="empty" icon="i-chapters" title={t('player.opt.noChapters')}/> : null}
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
      </>}</div></AudioPanelSurface>} />
    {notice && <div className={phone ? styles.notice : styles.topNotice}>{notice}</div>}
  </section>;
}

/** The book's cover, drawn whole in its own shape inside the square; a book with no picture, or one
 * that does not load, draws the kit's generated cover, as its card and page do. */
function ListeningCover({cover, title}: {cover?: ArtworkSource; title: string}) {
  const generated = <GeneratedCover title={title} glyph={Array.from(title.trim())[0] ?? ''} tint={generatedCoverTint(title)}/>;
  return cover ? <WholeArtwork source={cover} alt={title} className={styles.coverImage} fallback={generated}/> : <span className={styles.coverImage}>{generated}</span>;
}
