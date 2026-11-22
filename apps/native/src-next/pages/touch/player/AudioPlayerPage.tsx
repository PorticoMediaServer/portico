import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {clockText} from '../../../../../../packages/client-core/src/presentation/kit-model/text';
import {AudioPlayerFrame} from '../../../kit/frames';
import {Button} from '../../../kit/controls';
import {ListeningSeekPaint} from '../../../kit/dock/ListeningCoverBridge';
import {AudioPanel, ChapterList, ChapterTicks} from '../../../kit/player/queue';
import {ListeningCover, ListeningNoticeLine, ListeningPageBackground, ListeningPanel, ListeningSurface} from '../../../kit/player/ListeningSurface';
import {BottomSheet, Menu} from '../../../kit/overlays';
import {RetainedOverlay} from '../../../kit/overlays/SurfacePaintHost';
import {nativeEqualizer} from '../../../platform/playback/equalizer';
import {EqualizerPage} from '../../shared/player/EqualizerPage';
import {MusicSignalLine, MusicSignalSheet} from '../../../screens/destinations/music/MusicSignal';
import {NowPlayingQueue} from '../../../kit/player/NowPlayingQueue';
import {TVMusicFocus} from '../../../kit/player/TVMusicFocus';
import {listeningChapterSeek} from '../../../screens/destinations/music/audiobook-controls';
import {NowPlayingLyrics, type NowPlayingLyricsProps} from '../../../kit/player/NowPlayingLyrics';
import {ListeningQueuePaint, ListeningLyricsPaint} from '../../../kit/player/ListeningPanePaint';
import {bookSleepChoices, sleepLabel, useTouchAudioPlayer} from '../../shared/player/useTouchAudioPlayer';
import {equalizerCopy, retainedListeningParts, omittedListeningParts} from '../../../kit/player/listening-presentation';
import type {NowPlayingScreenProps} from '../../shared/player/audio-player';

const t = defaultI18n.t;
const omittedPlayerParts = [...omittedListeningParts, EqualizerPage];

export function AudioPlayerPage(props: NowPlayingScreenProps) {
  const {queue, artwork, height, backgroundHosted = false, onQueuePlay, retry, playOn, song, onClose, signal, notice,
    reportError = () => {}, testID = 'now-playing', lyrics, audiobook, sleepEnd} = props;
  const {p, playback, book, playOnButton, paintPermit, paintActive, drawnQueue, playingFrom, policyReady, shuffled, repeat,
    queueEditPort, changePolicy, focus, paneOwner, wide, sheet, setSheet, panel, setPanel, moreRef, bookReader, bookSnapshot,
    reportBookError, setBrowsingChapters, chapterBusy, signalRef, closePanel, equalizer, side, position, bookFacts, chapterData,
    wholeBook, byChapter, chapter, chapterTotal, bounds, span, inputActive, seekBook, sleepMinutes, sleepSelected, skipBack,
    skipForward, fraction, act, fenceKey, scrub, hostedScrubber, adjustSeek, favoritePaint, facts, album, bookRemaining} = useTouchAudioPlayer(props);
  const knownLyrics = React.isValidElement<NowPlayingLyricsProps>(lyrics) && lyrics.type === NowPlayingLyrics ? lyrics : undefined;
  const lyricPaint = knownLyrics ? <ListeningLyricsPaint owner={paneOwner} scope={fenceKey} testID={knownLyrics.props.testID}/> : lyrics;
  const queuePaint = queue ? <ListeningQueuePaint owner={paneOwner} scope={fenceKey} testID={`${testID}-queue`}/> : null;
  const showingLyrics = !book && sheet === 'lyrics' && !!lyrics;
  const showingQueue = sheet === 'queue' && !!queue;
  const showChapterRetry = bookFacts?.chapters.phase === 'error' || bookFacts?.chapters.phase === 'refresh-required';
  const paginateChapters = !!(bookFacts?.chapters.pagination.canPrevious || chapterData?.nextCursor);
  const chapterPane = <ChapterList testID={`${testID}-chapters`}
    status={wholeBook ? undefined : bookFacts?.chapters.phase === 'loading' ? t('status.loading') :
      showChapterRetry || bookFacts?.chapters.phase === 'ready' && chapterData?.status !== 'available' ? t('player.opt.unavailable') : undefined}
    retry={!wholeBook && showChapterRetry ? {label: t('action.tryAgain'), icon: 'i-retry',
      onPress: () => {if (bookReader?.current()) void bookReader.readChapters().catch(reportBookError);}} : undefined}
    chapters={wholeBook ? wholeBook.chapters.map((value, index) => ({
      id: value.id, number: value.number, title: value.title, duration: clockText(value.endSeconds - value.startSeconds),
      selected: index === wholeBook.current, disabled: !p.capabilities.seek, onPress: () => {
        if (paintPermit && !paintPermit.current()) return;
        setBrowsingChapters(false);
        void audiobook?.playBookChapter?.(playback.fence, value.id).catch(reportBookError);
      },
    })) : (chapterData?.chapters ?? []).map(value => ({
      id: value.id, number: value.index, title: value.title, duration: clockText(value.endSeconds - value.startSeconds),
      selected: value.id === chapter?.id, disabled: !p.capabilities.seek, onPress: () => {
        if (!bookReader) return;
        const at = listeningChapterSeek(bookReader, bookSnapshot, value.id);
        if (at !== undefined) {setBrowsingChapters(false); seekBook(at);}
      },
    }))}
    previous={!wholeBook && paginateChapters ? {label: t('player.previous'), icon: 'i-left', disabled: !bookFacts?.chapters.pagination.canPrevious || chapterBusy,
      onPress: () => {if (bookReader?.current()) {setBrowsingChapters(true); void bookReader.readPreviousChapterPage().catch(reportBookError);}}} : undefined}
    next={!wholeBook && paginateChapters ? {label: t('player.next'), icon: 'i-right', disabled: !chapterData?.nextCursor || chapterBusy,
      onPress: () => {if (bookReader?.current()) {setBrowsingChapters(true); void bookReader.readNextChapterPage().catch(reportBookError);}}} : undefined}/>;
  const positionInWindow = Math.max(0, position - (bounds?.start ?? 0));
  const seekPaint = <ListeningSeekPaint fence={playback.fence} fraction={fraction} span={span} seconds={positionInWindow}
    label={t('player.position')} testID={`${testID}-scrubber`} fallbackGesture={hostedScrubber ? undefined : scrub} fallbackAdjust={hostedScrubber || !inputActive ? undefined : adjustSeek}/>;
  const ticks = book && wide && bounds && span > 0
    ? (wholeBook ? wholeBook.chapters.filter(value => value.itemId === wholeBook.itemId) : chapterData?.chapters ?? [])
      .filter(value => value.startSeconds > bounds.start && value.startSeconds < bounds.end)
      .map(value => (value.startSeconds - bounds.start) / span)
    : [];
  const upcoming = queue?.next?.available ? queue.items.find(item => item.entryId === queue.next.entryId)?.title : undefined;
  const tabs = book && queue ? [
    {value: 'chapters', label: t('player.opt.chapters'), icon: 'i-list'},
    {value: 'queue', label: t('player.queue'), icon: 'i-queue'},
  ] as const : !book && queue && lyrics ? [
    {value: 'queue', label: t('player.queue'), icon: 'i-queue'},
    {value: 'lyrics', label: t('player.lyrics'), icon: 'i-cc'},
  ] as const : undefined;
  const widePanel = side && sheet ? <AudioPanel scrollable={false}
    heading={{title: t(sheet === 'chapters' ? 'player.opt.chapters' : sheet === 'lyrics' ? 'player.lyrics' : 'player.queue')}}
    tabs={tabs && {items: tabs, value: sheet, onValueChange: value => {
      if (value === 'queue' || value === 'lyrics' || value === 'chapters') setSheet(value);
    }}}>
    {sheet === 'chapters' ? chapterPane : showingLyrics ? lyricPaint : queuePaint}
  </AudioPanel> : undefined;
  const column = <AudioPlayerFrame testID={`${testID}-column`} title={p.identity.title} byline={wide ? [facts.map(part => part.text).join(' '), album.map(part => part.text).join(' ')] : facts.map(part => part.text)}
    icon={book ? 'i-book' : 'i-music'} cover={p.artwork && <ListeningCover fence={playback.fence} source={p.artwork}/>}
    context={playingFrom ? t('web.player.playingFromLabel') : t('player.queueNowPlaying')} source={playingFrom}
    close={{icon: 'i-down', label: t('player.minimize'), onPress: onClose}}
    more={{icon: 'i-more', label: t('card.more', {title: p.identity.title}), anchorRef: moreRef, onPress: () => setPanel('more')}}
    favourite={favoritePaint ? {icon: favoritePaint.favorite ? 'i-heart-f' : 'i-heart', active: favoritePaint.favorite,
      label: t(favoritePaint.favorite ? 'entry.removeFromFavorites' : 'entry.addToFavorites'), disabled: favoritePaint.pending,
      onPress: () => {if (song?.token) song.favorite(song.token, !favoritePaint.favorite);}} : undefined}
    device={playOn?.enabled ? {icon: 'i-cast', label: t('device.thisDevice'), anchorRef: playOnButton,
      onPress: () => playOn.open(playback.fence, playOnButton)} : undefined}
    chapter={book ? {label: chapter?.title ?? '', of: wide && chapter ? t('player.clockOf', {position: clockText(Math.max(0, position - chapter.startSeconds)), duration: clockText(chapter.endSeconds - chapter.startSeconds)}) : chapterTotal === undefined ? '' : t('player.chapterTotal', {total: chapterTotal})} : undefined}
    panel={widePanel}
    signal={signal || retry?.enabled || notice ? <>
      {signal && <MusicSignalLine view={signal} focusRef={signalRef} onPress={() => setPanel('signal')}/>}
      {retry?.enabled && <Button testID={`${testID}-retry`} variant="primary" icon="i-retry" onPress={() => retry.run(playback.fence)}>{t('next.states.tryAgain')}</Button>}
      {notice && <ListeningNoticeLine text={t(notice)}/>}
    </> : undefined}
    scrubber={ticks.length ? <ChapterTicks ticks={ticks} testID={`${testID}-chapter-ticks`}>{seekPaint}</ChapterTicks> : seekPaint}
    times={{position: clockText(positionInWindow) ?? '', end: wide && !book ? clockText(span || p.durationSeconds) ?? '' : `-${clockText(Math.max(0, (span || p.durationSeconds || 0) - positionInWindow)) ?? ''}`,
      middle: bookRemaining ? t('next.kit.remaining', {time: bookRemaining}) : undefined}}
    transport={[
      book ? {icon: 'i-prev', label: t(byChapter ? 'next.dock.previousChapter' : 'player.previous'), testID: `${testID}-previous-chapter`, disabled: !p.capabilities.previous, onPress: () => act('previous')}
        : {icon: 'i-shuffle', label: t('next.dock.shuffle'), testID: `${testID}-shuffle`, disabled: !policyReady || !!drawnQueue?.radio, active: shuffled, onPress: () => changePolicy({action: 'shuffle', shuffled: !shuffled})},
      book ? {icon: 'i-rew', label: t('player.skipBackSeconds', {seconds: skipBack}), testID: `${testID}-skip-back`, skip: skipBack, disabled: !p.capabilities.seek || !bookReader, onPress: () => seekBook(position - skipBack)}
        : {icon: 'i-prev', label: t('player.previous'), testID: `${testID}-previous`, disabled: !p.capabilities.previous, onPress: () => act('previous')},
      {icon: playback.playing ? 'i-pause' : 'i-play', label: t(playback.playing ? 'action.pause' : 'action.play'), testID: `${testID}-toggle`, disabled: playback.playing ? !p.capabilities.pause : !p.capabilities.play, onPress: () => act(playback.playing ? 'pause' : 'play')},
      book ? {icon: 'i-ffw', label: t('player.skipForwardSeconds', {seconds: skipForward}), testID: `${testID}-skip-forward`, skip: skipForward, disabled: !p.capabilities.seek || !bookReader, onPress: () => seekBook(position + skipForward)}
        : {icon: 'i-next', label: t('player.next'), testID: `${testID}-next`, disabled: !p.capabilities.next, onPress: () => act('next')},
      book ? {icon: 'i-next', label: t(byChapter ? 'next.dock.nextChapter' : 'player.next'), testID: `${testID}-next-chapter`, disabled: !p.capabilities.next, onPress: () => act('next')}
        : {icon: 'i-repeat', label: t(repeat === 'off' ? 'next.dock.repeatOff' : repeat === 'all' ? 'next.dock.repeatAll' : 'next.dock.repeatOne'), testID: `${testID}-repeat`, disabled: !policyReady, active: repeat !== 'off', repeatOne: repeat === 'one',
          onPress: () => changePolicy({action: 'repeat', repeat: repeat === 'off' ? 'all' : repeat === 'all' ? 'one' : 'off'})},
    ]}
    next={upcoming ? {label: t('player.next'), title: upcoming} : undefined}
    lyrics={lyrics && (!side || !tabs || sheet === 'lyrics') ? {icon: 'i-cc', label: t('player.lyrics'), testID: `${testID}-lyrics-toggle`, active: sheet === 'lyrics', onPress: () => setSheet(open => open === 'lyrics' ? null : 'lyrics')} : undefined}
    queue={queue && (!side || !tabs || sheet === 'queue') ? {icon: 'i-queue', label: t('player.queue'), testID: `${testID}-queue-toggle`, active: sheet === 'queue', onPress: () => setSheet(open => open === 'queue' ? null : 'queue')} : undefined}
    speed={book ? {label: t('player.opt.speed'), value: `${bookFacts?.rate.effective ?? bookFacts?.rate.selected ?? 1}×`, testID: `${testID}-speed`, disabled: !bookFacts?.rate.supported?.length, onPress: () => setPanel('speed'),
      choices: (bookFacts?.rate.supported ?? []).map(rate => ({label: `${rate}×`, selected: (bookFacts?.rate.effective ?? bookFacts?.rate.selected) === rate,
        onPress: () => {if (bookReader?.current() && (!paintPermit || paintPermit.current())) audiobook?.setRate(playback.fence, rate);}}))} : undefined}
    sleep={book ? {icon: 'i-moon', label: t('player.opt.sleepTimer'), value: sleepMinutes === null ? undefined : t('player.opt.minutes', {count: sleepMinutes}), testID: `${testID}-sleep`, disabled: !bookReader || !audiobook?.setSleepTimer,
      active: sleepMinutes !== null, onPress: () => setPanel('sleep'), help: sleepEnd || sleepMinutes !== null ? sleepLabel(sleepEnd ?? sleepMinutes) : undefined,
      choices: bookSleepChoices.map(choice => ({label: sleepLabel(choice), selected: sleepSelected(choice), disabled: !bookReader || !audiobook?.setSleepTimer,
        onPress: () => {if (bookReader?.current() && (!paintPermit || paintPermit.current())) void audiobook?.setSleepTimer?.(playback.fence, choice).catch(reportBookError);}}))} : undefined}
    chapters={book ? {icon: 'i-list', label: t('player.opt.chapters'), value: chapterTotal?.toString(), testID: `${testID}-chapters-toggle`, disabled: !bookReader,
      active: sheet === 'chapters', onPress: () => setSheet(open => open === 'chapters' ? null : 'chapters')} : undefined}
  />;
  return <>
    {paintActive && showingQueue && queue && <NowPlayingQueue paintOwner={paneOwner} paintScope={fenceKey} testID={`${testID}-queue`} view={queue} onPlay={onQueuePlay} artwork={artwork} queueEdit={queueEditPort}/>}
    {paintActive && showingLyrics && knownLyrics && React.cloneElement(knownLyrics, {paintOwner: paneOwner, paintScope: fenceKey})}
    <TVMusicFocus id={focus} visible={!panel} entry="toggle" onClose={onClose}><RetainedOverlay permit={panel ? undefined : paintPermit} allowedTypes={retainedListeningParts} omitTypes={omittedPlayerParts}><ListeningSurface fence={playback.fence} testID={testID} blocked={!!panel || !side && !!sheet}>
      {!backgroundHosted && <ListeningPageBackground fence={playback.fence} height={height} ambient={p.ambient}/>}
      {panel === 'signal' && signal ? <ListeningPanel><MusicSignalSheet view={signal} onClose={closePanel} contextTitle={p.identity.title} returnFocusRef={signalRef}/></ListeningPanel> : null}
      {panel === 'eq' ? <ListeningPanel opaque><EqualizerPage onBack={closePanel} copy={equalizerCopy()} reportError={reportError} store={nativeEqualizer}/></ListeningPanel> : null}
      {column}
    </ListeningSurface></RetainedOverlay></TVMusicFocus>
    <BottomSheet open={paintActive && !side && !!sheet} onOpenChange={open => {if (!open) setSheet(null);}}
      title={t(sheet === 'chapters' ? 'player.opt.chapters' : sheet === 'lyrics' ? 'player.lyrics' : 'player.queue')} scrollable={false}
      testID={`${testID}-sheet`} detents={[Math.round(height * .5), Math.round(height * .94)]}>
      {sheet === 'chapters' ? chapterPane : sheet === 'lyrics' ? lyricPaint : queuePaint}
    </BottomSheet>
    <Menu open={paintActive && panel === 'speed'} onOpenChange={open => {if (!open) setPanel(null);}} title={t('player.opt.speed')} testID={`${testID}-speed-menu`}
      items={(bookFacts?.rate.supported ?? []).map(rate => ({id: String(rate), label: rate === 1 ? t('player.opt.normalSpeed') : `${rate}×`, selected: (bookFacts?.rate.effective ?? bookFacts?.rate.selected) === rate,
        onSelect: () => {if (bookReader?.current() && (!paintPermit || paintPermit.current())) audiobook?.setRate(playback.fence, rate);}}))}/>
    <Menu open={paintActive && panel === 'sleep'} onOpenChange={open => {if (!open) setPanel(null);}} title={t('player.opt.sleepTimer')} testID={`${testID}-sleep-menu`}
      items={bookSleepChoices.map(choice => ({id: choice === null ? 'off' : String(choice), label: sleepLabel(choice), selected: sleepSelected(choice),
        onSelect: () => {if (bookReader?.current() && (!paintPermit || paintPermit.current())) void audiobook?.setSleepTimer?.(playback.fence, choice).catch(reportBookError);}}))}/>
    <Menu open={paintActive && panel === 'more'} onOpenChange={open => {if (!open) setPanel(null);}} title={p.identity.title} anchorRef={moreRef} touchContext
      items={[
        ...(book && queue ? [{id: 'queue', label: t('player.queue'), icon: 'i-queue' as const, onSelect: () => setSheet('queue')}] : []),
        ...(signal ? [{id: 'signal', label: t('player.opt.info'), icon: 'i-info' as const, closeOnSelect: false, onSelect: () => setPanel('signal')}] : []),
        ...(equalizer.supported ? [{id: 'equalizer', label: t('next.media.eq.title'), icon: 'i-gauge' as const, closeOnSelect: false, onSelect: () => setPanel('eq')}] : []),
        {id: 'stop', label: t('action.stop'), icon: 'i-stop', disabled: !p.capabilities.stop, onSelect: () => act('stop')},
      ]}/>
  </>;
}
