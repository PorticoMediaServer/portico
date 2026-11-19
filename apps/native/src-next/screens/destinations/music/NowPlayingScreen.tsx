import React, {createContext, useCallback, useContext, useEffect, useId, useLayoutEffect, useMemo, useRef, useState} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {Gesture, GestureDetector} from 'react-native-gesture-handler';
import Animated, {runOnJS, useSharedValue} from 'react-native-reanimated';
import type {QueuePlaybackView} from '../../../../../../packages/client-core/src/queue-controller';
import type {V1QueueEditIntent, V1QueuePolicyIntent} from '../../../../../../packages/client-core/src/playback-v1/v1-queue-player';
import type {PlaybackFence} from '../../../../../../packages/player-core/src';
import type {MessageId} from '../../../../../../packages/i18n/src';
import {useSyncExternalStore} from 'react';
import {nativeEqualizer} from '../../../platform/playback/equalizer';
import {registerOverlayKeys} from '../../../platform/overlay-keys';
import {NativeWatchEqualizer, type NativeEqualizerCopy} from '../../watch/Equalizer';
import {MusicSignalLine, MusicSignalSheet, type MusicSignalView} from './MusicSignal';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {clockText, durationText} from '../../../../../../packages/client-core/src/presentation/kit-model/text';
import {AmbientArt, Artwork} from '../../../kit/artwork';
import {Layer} from '../../../kit/shell/Layer';
import {Button, Focusable} from '../../../kit/controls';
import {GestureCommitScope} from '../../../kit/controls/gesture-commit';
import {Icon, type IconName} from '../../../kit/Icon';
import {AudioPlayerFrame, audioPlayerFramePaintTypes, type AudioControl, type AudioOption, type AudioPlayerFrameProps} from '../../../kit/frames';
import {useFrameMetrics} from '../../../kit/frames/metrics';
import {useTheme} from '../../../kit/theme';
import {ListeningSeekPaint, useListeningScrubberPublication, ListeningGroup, useListeningCoverSlot, useListeningPageOwner, useListeningPageActive, useListeningPageHeight, useListeningPagePresent, useListeningPaintPermit, useListeningPageStyle} from '../../../kit/dock/ListeningCoverBridge';
import {alpha, browseType} from '../../../kit/browse/presentation';
import {commandAllowed, dockCommand, playbackPosition, seekBounds, seekTo} from '../../../kit/dock/model';
import type {DockCommand, DockOccupant} from '../../../kit/dock/types';
import {NowPlayingQueue, nowPlayingQueueEntry, nowPlayingQueueWindow} from './NowPlayingQueue';
import {ListeningSongActions, ListeningFavoritePaint, listeningSongFavorite} from './ListeningSongActions';
import {MusicFocusRegion, TVMusicFocus} from './TVMusicFocus';
import {TVNowPlayingScreen} from './TVNowPlayingScreen';
import type {NativeListeningQueuePolicy} from '../../../shell/listening-queue-policy';
import type {NativePlaybackReadLifetime, NativeSleepChoice, NativeSleepEnd} from '../../../platform/playback/types';
import {BlurView, glassRecipe} from '../../../platform/native-blur';
import {BottomSheet, Menu} from '../../../kit/overlays';
import {useAudiobookRead, type AudiobookRead} from './use-audiobook-read';
import {currentListeningChapter, locateListeningChapter, listeningChapterSeek} from './audiobook-controls';
import type {BookChaptersView} from '../../../../../../packages/client-core/src/book-chapters';
import type {NativeListeningSong} from '../../../shell/listening-song-detail';
import {RetainedOverlay} from '../../../kit/overlays/SurfacePaintHost';
import {usePublishedPlayerMorph} from '../../../kit/dock/PlayerMorphBridge';
import {useOverlayPaintMode} from '../../../kit/overlays/OverlayPaintContext';
import {NowPlayingLyrics, type NowPlayingLyricsProps} from './NowPlayingLyrics';
import {ListeningQueuePaint, ListeningLyricsPaint} from './ListeningPanePaint';
import {useTVPanelPresence} from '../../../kit/overlays/TVPanelMotion';
import {registerBack} from '../../../kit/navigation/back';

const t = defaultI18n.t;
const equalizerCopy = (): NativeEqualizerCopy => ({title: t('next.media.eq.title'), back: t('action.back'), thisDevice: t('device.thisDevice'),
  bands: t('next.media.eq.bands'), preamp: t('next.media.eq.preamp'), reset: t('next.media.eq.reset'),
  musicAndAudiobooks: t('next.settingsSpec.account.music.title'), notBitPerfect: t('next.media.eq.help'),
  gain: (value, unit) => unit ? t('next.media.eq.db', {value}) : String(value),
  frequency: (index) => {const hz = [31, 62, 125, 250, 500, 1000, 2000, 4000, 8000, 16000][index] ?? 0;
    return hz >= 1000 ? t('next.media.eq.khz', {frequency: hz / 1000}) : t('next.media.eq.hz', {frequency: hz});}});

/** tablet-play9/10: the listening surface for music and audiobooks. The real
 * dock occupant supplies the picture, identity, transport capabilities and seek
 * window; the listening owner supplies the queue. Wide windows put the queue
 * beside the now-playing column at 366 pt (the main column never drops below a
 * usable cover); narrower ones present the same panes in a bottom sheet. */
export type NowPlayingScreenProps = Readonly<{
  /** TV presentation only; no extra listening session or command lifetime. */
  tvOpen?: boolean;
  /** A single pure backdrop is hosted beside the persistent player on touch. */
  backgroundHosted?: boolean;
  onTVExited?: () => void;
  occupant: DockOccupant;
  queue?: QueuePlaybackView;
  artwork?: React.ComponentProps<typeof NowPlayingQueue>['artwork'];
  /** The song's own synced lyrics body (the caller owns its reader); drawn as a
   * tab of the side panel, or a bottom sheet on a narrow window. */
  lyrics?: React.ReactNode;
  width: number;
  height: number;
  topInset?: number;
  bottomInset?: number;
  onAction: (command: DockCommand) => void;
  onQueuePlay?: (queueId: string, entryId: string, revision: string) => void;
  onAddToPlaylist?: () => void;
  queuePolicy?: NativeListeningQueuePolicy;
  /** The touch opening-and-route lease both guarded controls borrow; TV's own hook owns one. */
  policyLifetime?: NativePlaybackReadLifetime;
  /** A retained Try again for the failed output this screen drew, through the adapter's retry port. */
  retry?: Readonly<{enabled: boolean; run: (fence: PlaybackFence) => void}>;
  /** The real Play on controller, opened only for the output and page this screen drew. */
  playOn?: Readonly<{enabled: boolean; open: (fence: PlaybackFence, anchorRef?: React.RefObject<View | null>) => void}>;
  /** The guarded queue edit for loaded upcoming rows, bound to the fence this screen drew. */
  queueEdit?: Readonly<{enabled: boolean; lease?: object; apply: (fence: PlaybackFence, intent: V1QueueEditIntent, queueId: string, expectedRevision: string) => void}>;
  /** The current song's issued Detail token and its one favorite ticket; touch only. */
  song?: Readonly<{token?: NativeListeningSong; pending: boolean; favorite: (token: NativeListeningSong, value: boolean) => void}>;
  /** Source-pinned facts and session-only choices; never stored per title. */
  audiobook?: Readonly<{read: AudiobookRead; setRate: (fence: PlaybackFence, rate: number) => void; setSleepTimer?: (fence: PlaybackFence, choice: NativeSleepChoice) => Promise<void>;
    /** MU-2: the playing book's chapters across its parts, and a row's jump. */
    bookChapters?: (fence: PlaybackFence) => BookChaptersView | null; playBookChapter?: (fence: PlaybackFence, chapterId: string) => Promise<void>}>;
  sleepAtMs?: number | null;
  /** The armed end-of timer (MU-6), when that is the timer set instead of minutes. */
  sleepEnd?: NativeSleepEnd;
  onClose: () => void;
  /** music-plus (MC-8): the committed plan plus the engine's own current-song facts. */
  signal?: MusicSignalView;
  /** listening-notices: a pause the viewer did not make. */
  notice?: MessageId;
  reportError?: (error: unknown) => void;
  testID?: string;
}>;

const ListeningChromeGate = createContext(false);
const listeningPaintTypes = [...audioPlayerFramePaintTypes, ListeningSurface, ListeningSwipeArea, ListeningFrameCover, ListeningSidePanel, ListeningPageBackground, ListeningGroup, ListeningSeekPaint, ListeningQueuePaint, ListeningLyricsPaint, MusicFocusRegion, MusicSignalLine, MusicSignalSheet, ListeningFavoritePaint, View, Text, ScrollView, Artwork, AmbientArt, Button, Focusable, Icon, React.Fragment];
const listeningPaintOmissions = [GestureDetector, NowPlayingQueue, NowPlayingLyrics, ListeningSongActions, NativeWatchEqualizer];

export function NowPlayingScreen(props: NowPlayingScreenProps) {
  const theme = useTheme();
  return theme.isTV ? <TVNowPlayingScreen {...props} equalizerCopy={equalizerCopy()}/> : <TouchNowPlayingScreen {...props}/>;
}

/** This retained snapshot is paint only; the host's live adapters remain authoritative. */
export function TVListeningRoute({value, scope, onPresenceChange}: {value?: NowPlayingScreenProps; scope: string; onPresenceChange?: (present: boolean) => void}) {
  const presence = useTVPanelPresence(value, scope);
  const present = presence.value !== undefined, report = useRef(onPresenceChange);
  report.current = onPresenceChange;
  useLayoutEffect(() => {onPresenceChange?.(present);}, [onPresenceChange, present]);
  useLayoutEffect(() => () => report.current?.(false), []);
  return presence.value ? <NowPlayingScreen {...presence.value} tvOpen={presence.open} onTVExited={presence.exited}/> : null;
}

const bookSleepChoices: readonly NativeSleepChoice[] = Object.freeze([null, 15, 30, 45, 60, 90, 'chapter', 'book']);
function sleepLabel(choice: NativeSleepChoice): string {
  return choice === 'item' ? t('player.opt.sleepEndOfTitle') : choice === 'chapter' ? t('player.opt.sleepEndOfChapter') : choice === 'book' ? t('player.opt.sleepEndOfBook')
    : choice === null ? t('player.opt.off') : t('player.opt.minutes', {count: choice});
}
function TouchNowPlayingScreen({occupant, queue, artwork, width, height, backgroundHosted = false, onAction, onQueuePlay, queuePolicy, policyLifetime, retry, playOn, queueEdit, song, onClose, signal, notice, reportError = () => {}, testID = 'now-playing', lyrics, audiobook, sleepAtMs = null, sleepEnd}: NowPlayingScreenProps) {
  const theme = useTheme(), {presentation: p, playback} = occupant, book = occupant.mode === 'audiobook';
  // The frame's forms decide the layout: one column on a phone, the column beside the panel from the medium form.
  const wide = useFrameMetrics().form !== 'phone';
  const playOnButton = useRef<View>(null);
  const paintPermit = useListeningPaintPermit(playback.fence), paintActive = useListeningPageActive(playback.fence);
  // Repeat and shuffle are the queue's own facts, and the answer to a change is the server's
  // queue read that follows, never a locally flipped control. A request keeps the exact queue
  // and revision this screen drew, so a queue that moved on refuses it.
  const drawnFence = playback.fence, drawnQueue = queue?.queue, lifetime = policyLifetime;
  // Only the accepted window for exactly this queue and revision may name what the run started
  // from; anything else leaves the line off rather than borrowing an album's own title.
  const drawnWindow = queue ? nowPlayingQueueWindow(queue) : undefined;
  const currentFacts = drawnQueue?.currentEntryId ? nowPlayingQueueEntry(drawnWindow, drawnQueue.currentEntryId) : undefined;
  const currentSegmentId = currentFacts?.available === true ? currentFacts.segmentId : undefined;
  const playingFrom = currentSegmentId ? drawnWindow?.segments.find(segment => segment.id === currentSegmentId && segment.label)?.label : undefined;
  const policyReady = !!drawnQueue && drawnFence.kind === 'queue' && !!lifetime && !!queuePolicy?.canApply(drawnFence);
  const shuffled = !!drawnQueue?.shuffled, repeat = drawnQueue?.repeat ?? 'off';
  // The edit port exists only while the accepted window really matches the drawn queue and
  // names each row once; the pane then asks that same window for a row's own facts.
  const queueEditPort = queueEdit?.enabled && drawnWindow ? {enabled: true, lease: queueEdit.lease, fenceKey: JSON.stringify(playback.fence),
    entry: (entryId: string) => nowPlayingQueueEntry(drawnWindow, entryId),
    apply: (intent: V1QueueEditIntent, queueId: string, expectedRevision: string) => queueEdit.apply(playback.fence, intent, queueId, expectedRevision)} : undefined;
  const changePolicy = (intent: V1QueuePolicyIntent) => {
    if (!policyReady || !drawnQueue || !lifetime || !queuePolicy) return;
    void queuePolicy.apply(drawnFence, {queueId: drawnQueue.id, expectedRevision: drawnQueue.revision, intent}, lifetime).catch(reportError);
  };
  const focus = useId();
  const paneOwner = useMemo(() => Object.freeze({}), []);
  const [sheet, setSheet] = useState<'queue' | 'lyrics' | 'chapters' | null>(() => !wide ? null : book ? 'chapters' : queue ? 'queue' : lyrics ? 'lyrics' : null);
  const [panel, setPanel] = useState<'signal' | 'eq' | 'speed' | 'sleep' | 'more' | null>(null);
  const moreRef = useRef<View | null>(null);
  const {reader: bookReader, snapshot: bookSnapshot} = useAudiobookRead(book && paintActive ? audiobook?.read : undefined, playback.fence, reportError);
  const reportBookError = useCallback((error: unknown) => {if (bookReader?.current()) reportError(error);}, [bookReader, reportError]);
  const [browsingChapters, setBrowsingChapters] = useState(false);
  const [chapterBusy, setChapterBusy] = useState(false), chapterRead = useRef<object | undefined>(undefined);
  useEffect(() => {chapterRead.current = undefined; setChapterBusy(false); setBrowsingChapters(false);}, [bookReader]);
  useEffect(() => {
    setPanel(null);
    setSheet(open => book ? open && open !== 'chapters' ? wide ? 'chapters' : null : open
      : open === 'chapters' ? wide ? queue ? 'queue' : lyrics ? 'lyrics' : null : null : open);
    // Only a change of listening mode replaces a pane, not every queue update.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [book]);
  // music-plus 6/6b and listening-notices 8: the signal sheet (like the EQ panel) is the top
  // surface while it is open, so Back closes it rather than folding the player, and focus returns
  // to the line that opened it.
  const signalRef = useRef<View | null>(null);
  const closePanel = useCallback(() => {
    setPanel(null);
    if (theme.isTV && panel !== 'signal') requestAnimationFrame(() => (signalRef.current as unknown as {requestTVFocus?: () => void} | null)?.requestTVFocus?.());
  }, [theme.isTV, panel]);
  useEffect(() => {
    if (!panel || (theme.isTV && panel === 'signal')) return;
    const releaseBack = registerBack(() => {closePanel(); return true;}, {scope: 'overlay'});
    const release = theme.isTV ? registerOverlayKeys(key => {if (key === 'escape' || key === 'menu') {closePanel(); return true;} return false;}) : () => {};
    return () => {releaseBack(); release();};
  }, [panel, closePanel, theme.isTV]);
  const equalizer = useSyncExternalStore(nativeEqualizer.subscribe, nativeEqualizer.getSnapshot);
  const side = wide && sheet !== null, position = playbackPosition(occupant);
  const bookFacts = bookSnapshot.phase === 'current' ? bookSnapshot : undefined;
  const chapterData = bookFacts?.chapters.data;
  // MU-2: the whole book's chapters when known (every part, numbered through the book); the
  // playing part's own chapter pages otherwise.
  const bookView = book ? audiobook?.bookChapters?.(playback.fence) : null, bookList = audiobook?.playBookChapter ? bookView : null, wholeBook = bookList?.phase === 'ready' ? bookList : null;
  // Previous and Next move by chapter only when the book's list covers this part; else by part, as for a track.
  const byChapter = bookView?.phase === 'ready';
  // The playing chapter belongs to this part (before its first chapter, the last one is the part before's).
  const playingRow = wholeBook ? wholeBook.chapters[wholeBook.current] : undefined, playingChapter = playingRow && playingRow.itemId === wholeBook?.itemId ? playingRow : undefined;
  const chapter = wholeBook ? playingChapter && {id: playingChapter.id, index: playingChapter.number, title: playingChapter.title, startSeconds: playingChapter.startSeconds, endSeconds: playingChapter.endSeconds}
    : currentListeningChapter(bookSnapshot, position);
  const chapterTotal = wholeBook ? wholeBook.chapters.length : chapterData?.totalCount, inWholeBook = !!wholeBook, hasChapter = !!chapter;
  const fullBounds = seekBounds(occupant);
  const bounds = book && !wide && chapter && fullBounds ? {start: Math.max(fullBounds.start, chapter.startSeconds), end: Math.min(fullBounds.end, chapter.endSeconds)} : fullBounds;
  const span = bounds ? Math.max(0, bounds.end - bounds.start) : 0;
  const inputActive = paintActive && !panel && (wide || !sheet) && !!bounds && p.capabilities.seek && span > 0;
  useEffect(() => {
    if (inWholeBook || !bookReader || browsingChapters || hasChapter || chapterRead.current || bookFacts?.chapters.phase !== 'ready' || chapterData?.status !== 'available') return;
    const first = chapterData.chapters[0], last = chapterData.chapters.at(-1);
    if (!first || !last || !(position < first.startSeconds && bookFacts.chapters.pagination.canPrevious || position >= last.endSeconds && chapterData.nextCursor)) return;
    const run = {}; chapterRead.current = run; setChapterBusy(true);
    void locateListeningChapter(bookReader, position).catch(reportBookError).finally(() => {
      if (chapterRead.current === run) {chapterRead.current = undefined; setChapterBusy(false);}
    });
  }, [inWholeBook, bookReader, hasChapter, bookFacts?.chapters.phase, bookFacts?.chapters.pagination.canPrevious, chapterData, position, browsingChapters, reportBookError]);
  useEffect(() => {if (sheet !== 'chapters') setBrowsingChapters(false);}, [sheet]);
  const seekBook = (seconds: number) => {
    if (!bookReader?.current() || paintPermit && !paintPermit.current()) return;
    const command = seekTo(occupant, seconds); if (command) onAction(command);
  };
  const sleepMinutes = sleepAtMs === null ? null : Math.max(0, Math.ceil((sleepAtMs - Date.now()) / 60_000));
  // A book's timer: off, minutes, or the end of this chapter or the book (MU-6, FACTS §5).
  const sleepSelected = (choice: NativeSleepChoice) => typeof choice === 'string' ? sleepEnd === choice : !sleepEnd && sleepMinutes === choice;
  const skipBack = (bookFacts?.preferences.audiobook?.skipBackMs ?? p.skipBackSeconds * 1000) / 1000;
  const skipForward = (bookFacts?.preferences.audiobook?.skipForwardMs ?? (p.skipForwardSeconds ?? p.skipBackSeconds) * 1000) / 1000;
  const fraction = bounds && span > 0 ? Math.min(1, Math.max(0, (position - bounds.start) / span)) : 0;
  const act = (type: Parameters<typeof dockCommand>[1]) => {if (commandAllowed(occupant, type)) onAction(dockCommand(occupant, type));};
  // The touch bar previews on the UI thread. The gesture binds at BEGIN to the
  // occupant, bounds and callback this screen was actually drawing, so its own
  // successful lift can only ever invoke those: a successor that arrives mid-drag
  // retires both the paint and the captured pair instead of receiving the seek.
  const travel = useSharedValue(0), paintFraction = useSharedValue(0), dragFraction = useSharedValue<number | null>(null);
  type BeginSeek = Readonly<{occupant: DockOccupant; start: number; span: number; onAction: (command: DockCommand) => void}>;
  const fenceKey = JSON.stringify(occupant.playback.fence ?? null);
  useEffect(() => {setPanel(null);}, [fenceKey]);
  const knownLyrics = React.isValidElement<NowPlayingLyricsProps>(lyrics) && lyrics.type === NowPlayingLyrics ? lyrics : undefined;
  const lyricPaint = knownLyrics ? <ListeningLyricsPaint owner={paneOwner} scope={fenceKey} testID={knownLyrics.props.testID}/> : lyrics;
  const queuePaint = queue ? <ListeningQueuePaint owner={paneOwner} scope={fenceKey} testID={`${testID}-queue`}/> : null;
  const showingLyrics = !book && sheet === 'lyrics' && !!lyrics;
  const showingQueue = sheet === 'queue' && !!queue;
  const scopeGeneration = useRef(0);
  const scopes = useRef<Readonly<{key: string; start?: number; end?: number; enabled: boolean; span: number; generation: number; value: GestureCommitScope<BeginSeek>}> | undefined>(undefined);
  const prior = scopes.current;
  if (!prior || prior.key !== fenceKey || prior.start !== bounds?.start || prior.end !== bounds?.end || prior.enabled !== inputActive || prior.span !== span) {
    scopes.current = {key: fenceKey, start: bounds?.start, end: bounds?.end, enabled: inputActive, span, generation: ++scopeGeneration.current, value: new GestureCommitScope<BeginSeek>()};
  }
  const seekScope = scopes.current!.value, drawnScope = scopes.current!.generation;
  const scopeTicket = useSharedValue(0), activeScope = useSharedValue(0), dragRun = useSharedValue(0), dragTicket = useSharedValue(0), dragArmed = useSharedValue(false);
  useEffect(() => {if (dragFraction.value === null) paintFraction.value = fraction;}, [dragFraction, paintFraction, fraction]);
  useLayoutEffect(() => {
    if (!inputActive) {scopeTicket.value = 0; activeScope.value = 0; dragArmed.value = false; dragFraction.value = null; return;}
    const ticket = seekScope.activate(); scopeTicket.value = ticket; activeScope.value = drawnScope;
    dragArmed.value = false; dragFraction.value = null;
    return () => {
      if (!seekScope.retire(ticket)) return;
      scopeTicket.value = 0; activeScope.value = 0; dragArmed.value = false; dragFraction.value = null;
    };
  }, [seekScope, drawnScope, inputActive, activeScope, scopeTicket, dragArmed, dragFraction]);
  const bindSeek = (valid: boolean, ticket: number, run: number) => {
    seekScope.bind(ticket, run, valid && bounds && span > 0 ? {occupant, start: bounds.start, span, onAction} : undefined);
  };
  const commitSeek = (at: number, success: boolean, ticket: number, run: number) => {
    const bound = seekScope.claim(ticket, run);
    if (success !== true || !Number.isFinite(at) || !bound || paintPermit && !paintPermit.current()) return;
    const command = seekTo(bound.occupant, bound.start + Math.min(1, Math.max(0, at)) * bound.span);
    if (command) bound.onAction(command);
  };
  const discardBound = (ticket: number, run: number) => {seekScope.discard(ticket, run);};
  const adjustSeek = (direction: -1 | 1) => {
    if (!inputActive || !bounds || activeScope.value !== drawnScope || dragArmed.value) return;
    const step = direction < 0 ? p.skipBackSeconds : p.skipForwardSeconds ?? p.skipBackSeconds;
    if (!(step > 0) || !Number.isFinite(step)) return;
    // Accessibility uses the same begin/claim and paint-lifetime fence as a pan.
    // A retained callback cannot borrow the successor's activated seek scope.
    const ticket = scopeTicket.value, run = ++dragRun.value;
    bindSeek(true, ticket, run);
    commitSeek((position + direction * step - bounds.start) / span, true, ticket, run);
  };
  const scrub = Gesture.Pan().enabled(inputActive).minDistance(0)
    .onBegin(event => {
      if (activeScope.value !== drawnScope) return;
      const valid = travel.value > 0 && Number.isFinite(event.x);
      const ticket = scopeTicket.value, run = ++dragRun.value;
      dragTicket.value = ticket;
      runOnJS(bindSeek)(valid, ticket, run);
      dragArmed.value = valid && ticket > 0;
      if (!valid || ticket <= 0) return;
      dragFraction.value = Math.min(1, Math.max(0, event.x / travel.value));
    })
    .onUpdate(event => {
      if (activeScope.value !== drawnScope || !dragArmed.value || dragTicket.value !== scopeTicket.value || !(travel.value > 0) || !Number.isFinite(event.x)) return;
      dragFraction.value = Math.min(1, Math.max(0, event.x / travel.value));
    })
    .onEnd((event, success) => {
      if (activeScope.value !== drawnScope || !dragArmed.value || dragTicket.value !== scopeTicket.value || success !== true || !(travel.value > 0) || !Number.isFinite(event.x)) return;
      dragArmed.value = false;
      runOnJS(commitSeek)(Math.min(1, Math.max(0, event.x / travel.value)), true, dragTicket.value, dragRun.value);
    })
    .onFinalize(() => {if (activeScope.value !== drawnScope) return; dragArmed.value = false; dragFraction.value = null; runOnJS(discardBound)(dragTicket.value, dragRun.value);});
  const scrubber = inputActive ? {fence: playback.fence, gesture: scrub, scope: drawnScope, activeScope,
    ticket: scopeTicket, drag: dragFraction, base: paintFraction, travel, adjust: adjustSeek} : undefined;
  const hostedScrubber = useListeningScrubberPublication(scrubber);
  const favoritePaint = song ? listeningSongFavorite(song.token, song.pending) : undefined;
  const facts = p.identity.subtitle.filter(part => part.text), album = (p.album ?? []).filter(part => part.text);
  const bookRemaining = book && !wide && fullBounds ? durationText(Math.max(0, fullBounds.end - position), t) : undefined;
  const chapterPane = <View testID={`${testID}-chapters`} style={{flex: 1, minHeight: 0}}>
    {wholeBook ? <ScrollView showsVerticalScrollIndicator={false} contentContainerStyle={{paddingHorizontal: 12, paddingBottom: 16}}>
      {wholeBook.chapters.map((value, index) => <Focusable key={value.id} accessibilityRole="button" accessibilityState={{selected: index === wholeBook.current}}
        disabled={!p.capabilities.seek} onPress={() => {
          if (paintPermit && !paintPermit.current()) return;
          setBrowsingChapters(false); void audiobook?.playBookChapter?.(playback.fence, value.id).catch(reportBookError);
        }} style={{minHeight: 60, paddingHorizontal: 12, paddingVertical: 10, borderRadius: 10, flexDirection: 'row', alignItems: 'center', gap: 12,
          backgroundColor: index === wholeBook.current ? 'rgba(237,245,250,.1)' : 'transparent'}}>
        <Text style={[browseType(theme, 13, 700), {color: theme.colors.textTertiary, width: 28, fontVariant: ['tabular-nums']}]}>{value.number}</Text>
        <Text style={[browseType(theme, 15, 700), {flex: 1, color: index === wholeBook.current ? theme.colors.accentStrong : theme.colors.text}]}>{value.title}</Text>
        <Text style={[browseType(theme, 13, 500), {color: theme.colors.textTertiary, fontVariant: ['tabular-nums']}]}>{clockText(value.endSeconds - value.startSeconds)}</Text>
      </Focusable>)}
    </ScrollView> : <>
    {bookFacts?.chapters.phase === 'loading' ? <Text style={[browseType(theme, 14, 500), {color: theme.colors.textSecondary, padding: 20}]}>{t('status.loading')}</Text> : null}
    {bookFacts?.chapters.phase === 'error' || bookFacts?.chapters.phase === 'refresh-required' ? <View style={{padding: 20, gap: 12}}>
      <Text style={[browseType(theme, 14, 500), {color: theme.colors.textSecondary}]}>{t('player.opt.unavailable')}</Text>
      <Button variant="glass" size="small" icon="i-retry" onPress={() => {if (bookReader?.current()) void bookReader.readChapters().catch(reportBookError);}}>{t('action.tryAgain')}</Button>
    </View> : null}
    {bookFacts?.chapters.phase === 'ready' && chapterData?.status !== 'available' ? <Text style={[browseType(theme, 14, 500), {color: theme.colors.textSecondary, padding: 20}]}>{t('player.opt.unavailable')}</Text> : null}
    <ScrollView showsVerticalScrollIndicator={false} contentContainerStyle={{paddingHorizontal: 12, paddingBottom: 16}}>
      {chapterData?.chapters.map(value => <Focusable key={value.id} accessibilityRole="button" accessibilityState={{selected: value.id === chapter?.id}}
        disabled={!p.capabilities.seek} onPress={() => {
          if (!bookReader) return;
          const at = listeningChapterSeek(bookReader, bookSnapshot, value.id); if (at !== undefined) {setBrowsingChapters(false); seekBook(at);}
        }} style={{minHeight: 60, paddingHorizontal: 12, paddingVertical: 10, borderRadius: 10, flexDirection: 'row', alignItems: 'center', gap: 12,
          backgroundColor: value.id === chapter?.id ? 'rgba(237,245,250,.1)' : 'transparent'}}>
        <Text style={[browseType(theme, 13, 700), {color: theme.colors.textTertiary, width: 28, fontVariant: ['tabular-nums']}]}>{value.index}</Text>
        <Text style={[browseType(theme, 15, 700), {flex: 1, color: value.id === chapter?.id ? theme.colors.accentStrong : theme.colors.text}]}>{value.title}</Text>
        <Text style={[browseType(theme, 13, 500), {color: theme.colors.textTertiary, fontVariant: ['tabular-nums']}]}>{clockText(value.endSeconds - value.startSeconds)}</Text>
      </Focusable>)}
    </ScrollView>
    {bookFacts?.chapters.pagination.canPrevious || chapterData?.nextCursor ? <View style={{padding: 12, flexDirection: 'row', justifyContent: 'space-between', gap: 10}}>
      <Button variant="glass" size="small" icon="i-left" disabled={!bookFacts?.chapters.pagination.canPrevious || chapterBusy}
        onPress={() => {if (bookReader?.current()) {setBrowsingChapters(true); void bookReader.readPreviousChapterPage().catch(reportBookError);}}}>{t('player.previous')}</Button>
      <Button variant="glass" size="small" icon="i-right" disabled={!chapterData?.nextCursor || chapterBusy}
        onPress={() => {if (bookReader?.current()) {setBrowsingChapters(true); void bookReader.readNextChapterPage().catch(reportBookError);}}}>{t('player.next')}</Button>
    </View> : null}
    </>}
  </View>;
  // The frame (AudioPlayerFrame) places every region for the form; this screen fills them with its listening parts and
  // keeps its behaviour. The open motion's three groups are the screen's (ListeningGroup reads the dock's morph).
  const group = (index: 0 | 1 | 2, children: React.ReactNode) => index === 0
    ? <ListeningSwipeArea fence={playback.fence}><ListeningGroup fence={playback.fence} index={0} height={height} style={{alignItems: 'stretch'}}>{children}</ListeningGroup></ListeningSwipeArea>
    : <ListeningGroup fence={playback.fence} index={index} height={height} style={{alignItems: 'stretch'}}>{children}</ListeningGroup>;
  const control = (icon: IconName, label: string, onPress: () => void, extra: Partial<AudioControl> = {}): AudioControl => ({icon, label, onPress, ...extra});
  const rate = bookFacts?.rate.effective ?? bookFacts?.rate.selected;
  const ifCurrent = (run: () => void) => () => {if (bookReader?.current() && (!paintPermit || paintPermit.current())) run();};
  const toggle = control(playback.playing ? 'i-pause' : 'i-play', t(playback.playing ? 'action.pause' : 'action.play'), () => act(playback.playing ? 'pause' : 'play'),
    {disabled: playback.playing ? !p.capabilities.pause : !p.capabilities.play, testID: `${testID}-toggle`});
  const transport: AudioPlayerFrameProps['transport'] = book ? [
    control('i-prev', t(byChapter ? 'next.dock.previousChapter' : 'player.previous'), () => act('previous'), {disabled: !p.capabilities.previous, testID: `${testID}-previous-chapter`}),
    control('i-rew', t('player.skipBackSeconds', {seconds: skipBack}), () => seekBook(position - skipBack), {skip: skipBack, disabled: !p.capabilities.seek || !bookReader, testID: `${testID}-skip-back`}),
    toggle,
    control('i-ffw', t('player.skipForwardSeconds', {seconds: skipForward}), () => seekBook(position + skipForward), {skip: skipForward, disabled: !p.capabilities.seek || !bookReader, testID: `${testID}-skip-forward`}),
    control('i-next', t(byChapter ? 'next.dock.nextChapter' : 'player.next'), () => act('next'), {disabled: !p.capabilities.next, testID: `${testID}-next-chapter`}),
  ] : [
    // Radio has no shuffle, and a queue whose policy can't change keeps shuffle and repeat in their places, disabled.
    control('i-shuffle', t('next.dock.shuffle'), () => changePolicy({action: 'shuffle', shuffled: !shuffled}), {active: shuffled, disabled: !policyReady || !!drawnQueue?.radio, testID: `${testID}-shuffle`}),
    control('i-prev', t('player.previous'), () => act('previous'), {disabled: !p.capabilities.previous, testID: `${testID}-previous`}),
    toggle,
    control('i-next', t('player.next'), () => act('next'), {disabled: !p.capabilities.next, testID: `${testID}-next`}),
    control('i-repeat', t(repeat === 'off' ? 'next.dock.repeatOff' : repeat === 'all' ? 'next.dock.repeatAll' : 'next.dock.repeatOne'),
      () => changePolicy({action: 'repeat', repeat: repeat === 'off' ? 'all' : repeat === 'all' ? 'one' : 'off'}), {active: repeat !== 'off', disabled: !policyReady, testID: `${testID}-repeat`}),
  ];
  const speed: AudioOption | undefined = book ? {label: t('player.opt.speed'), value: `${rate ?? 1}×`, disabled: !bookFacts?.rate.supported?.length, onPress: () => setPanel('speed'), testID: `${testID}-speed`,
    choices: (bookFacts?.rate.supported ?? []).map(value => ({label: `${value}×`, selected: rate === value, onPress: ifCurrent(() => audiobook?.setRate(playback.fence, value))}))} : undefined;
  const sleep: AudioOption | undefined = book ? {label: t('player.opt.sleepTimer'), icon: 'i-moon', value: sleepMinutes === null ? undefined : t('player.opt.minutes', {count: sleepMinutes}),
    help: sleepEnd || sleepMinutes !== null ? sleepLabel(sleepEnd ?? sleepMinutes) : undefined, active: sleepMinutes !== null, disabled: !bookReader || !audiobook?.setSleepTimer, onPress: () => setPanel('sleep'), testID: `${testID}-sleep`,
    choices: bookSleepChoices.map(choice => ({label: sleepLabel(choice), selected: sleepSelected(choice), onPress: ifCurrent(() => void audiobook?.setSleepTimer?.(playback.fence, choice).catch(reportBookError))}))} : undefined;
  const chapters: AudioOption | undefined = book ? {label: t('player.opt.chapters'), icon: 'i-list', value: chapterData?.totalCount !== undefined ? String(chapterData.totalCount) : undefined,
    active: sheet === 'chapters', disabled: !bookReader, onPress: () => setSheet(open => open === 'chapters' ? null : 'chapters'), testID: `${testID}-chapters-toggle`} : undefined;
  const elapsed = Math.max(0, position - (bounds?.start ?? 0));
  const tabs = side ? <View style={{flexDirection: 'row', alignItems: 'center', gap: 10, padding: 14}}>
    {book && queue ? <>
      <Button testID={`${testID}-tab-chapters`} variant={sheet === 'chapters' ? 'primary' : 'glass'} size="small" icon="i-list" onPress={() => setSheet('chapters')}>{t('player.opt.chapters')}</Button>
      <Button testID={`${testID}-tab-queue`} variant={sheet === 'queue' ? 'primary' : 'glass'} size="small" icon="i-queue" onPress={() => setSheet('queue')}>{t('player.queue')}</Button>
    </> : !book && queue && lyrics ? <>
      <Button testID={`${testID}-tab-queue`} variant={sheet === 'queue' ? 'primary' : 'glass'} size="small" icon="i-queue" onPress={() => setSheet('queue')}>{t('player.queue')}</Button>
      <Button testID={`${testID}-tab-lyrics`} variant={sheet === 'lyrics' ? 'primary' : 'glass'} size="small" icon="i-lyrics" onPress={() => setSheet('lyrics')}>{t('player.lyrics')}</Button>
    </> : <Text style={[browseType(theme, 20, 800, -0.02), {color: theme.colors.text}]}>{t(sheet === 'chapters' ? 'player.opt.chapters' : sheet === 'lyrics' ? 'player.lyrics' : 'player.queue')}</Text>}
  </View> : null;
  const frame = <AudioPlayerFrame testID={`${testID}-column`} group={group}
    close={control('i-down', t('player.minimize'), onClose, {testID: `${testID}-close`})}
    context={playingFrom ? t('next.music.playingFrom', {source: ''}).trim() : wide ? undefined : t('player.queueNowPlaying')} source={playingFrom}
    device={playOn?.enabled ? control('i-cast', t('device.thisDevice'), () => playOn.open(playback.fence, playOnButton), {testID: `${testID}-play-on`, anchorRef: view => {playOnButton.current = view;}}) : undefined}
    more={control('i-more', t('card.more', {title: p.identity.title}), () => setPanel('more'), {testID: `${testID}-more`, anchorRef: view => {moreRef.current = view;}})}
    notice={notice ? <View accessibilityLiveRegion="polite" accessibilityRole="alert" testID={`${testID}-notice`} style={{flexDirection: 'row', alignItems: 'center', gap: 8, paddingHorizontal: 12, paddingVertical: 7, borderRadius: 10, backgroundColor: 'rgba(7,16,24,.5)'}}>
      <Icon name="i-pause" size={14} color={theme.colors.accentStrong}/><Text style={[browseType(theme, 13, 600), {color: theme.colors.textSecondary}]}>{t(notice)}</Text>
    </View> : undefined}
    artwork={p.artwork} icon={book ? 'i-book' : 'i-music'} cover={p.artwork ? <ListeningFrameCover fence={playback.fence} source={p.artwork}/> : undefined}
    title={p.identity.title} byline={[facts.map(part => part.text).join('  '), album.map(part => part.text).join('  ')].filter(Boolean)}
    favourite={favoritePaint ? control(favoritePaint.favorite ? 'i-heart-f' : 'i-heart', t(favoritePaint.favorite ? 'entry.removeFromFavorites' : 'entry.addToFavorites'),
      () => {if (song?.token) song.favorite(song.token, !favoritePaint.favorite);}, {active: favoritePaint.favorite, disabled: favoritePaint.pending, testID: `${testID}-favorite`}) : undefined}
    chapter={book && chapter ? {label: chapter.title, of: wide ? t('player.clockOf', {position: clockText(Math.max(0, position - chapter.startSeconds)), duration: clockText(chapter.endSeconds - chapter.startSeconds)})
      : chapterTotal !== undefined ? t('player.chapterTotal', {total: chapterTotal}) : ''} : undefined}
    signal={signal ? <MusicFocusRegion id={`${focus}.signal`} entry="music-signal-line" edges={{down: `${focus}.progress`}}><MusicSignalLine view={signal} onPress={() => setPanel('signal')}/></MusicFocusRegion> : undefined}
    action={retry?.enabled ? <Button testID={`${testID}-retry`} variant="primary" icon="i-retry" onPress={() => retry.run(playback.fence)}>{t('next.states.tryAgain')}</Button> : undefined}
    scrubber={<View>
      <ListeningSeekPaint fence={playback.fence} fraction={fraction} span={span} seconds={elapsed}
        label={t('player.position')} testID={`${testID}-scrubber`} fallbackGesture={hostedScrubber ? undefined : scrub} fallbackAdjust={hostedScrubber || !inputActive ? undefined : adjustSeek}/>
      {book && wide && bounds && span > 0 ? <View testID={`${testID}-chapter-ticks`} pointerEvents="none" style={{position: 'absolute', left: 0, right: 0, top: 12, height: 4}}>
        {(wholeBook ? wholeBook.chapters.filter(value => value.itemId === wholeBook.itemId) : chapterData?.chapters ?? []).filter(value => value.startSeconds > bounds.start && value.startSeconds < bounds.end).map(value => <View key={value.id}
          style={{position: 'absolute', top: 0, bottom: 0, width: 2, marginLeft: -1, left: `${(value.startSeconds - bounds.start) / span * 100}%`, backgroundColor: alpha(theme.colors.ink, .75)}}/>)}
      </View> : null}
    </View>}
    times={{position: clockText(elapsed) ?? '', end: !wide || book ? `-${clockText(Math.max(0, (span || p.durationSeconds || 0) - elapsed))}` : clockText(span || p.durationSeconds) ?? '',
      middle: bookRemaining ? t('next.kit.remaining', {time: bookRemaining}) : undefined}}
    transport={transport}
    lyrics={!book && lyrics ? control('i-lyrics', t('player.lyrics'), () => setSheet(open => open === 'lyrics' ? null : 'lyrics'), {active: sheet === 'lyrics', testID: `${testID}-lyrics-toggle`}) : undefined}
    queue={!book && queue ? control('i-queue', t('player.queue'), () => setSheet(open => open === 'queue' ? null : 'queue'), {active: sheet === 'queue', testID: `${testID}-queue-toggle`}) : undefined}
    speed={speed} sleep={sleep} chapters={chapters}
    panel={side ? <ListeningSidePanel>{tabs}{sheet === 'chapters' ? chapterPane : showingLyrics ? lyricPaint : queuePaint}</ListeningSidePanel> : undefined}/>;
  return <>
    {paintActive && showingQueue && queue && <NowPlayingQueue paintOwner={paneOwner} paintScope={fenceKey} testID={`${testID}-queue`} view={queue} onPlay={onQueuePlay} artwork={artwork} queueEdit={queueEditPort}/>}
    {paintActive && showingLyrics && knownLyrics && React.cloneElement(knownLyrics, {paintOwner: paneOwner, paintScope: fenceKey})}
    <TVMusicFocus id={focus} visible={!panel} entry="toggle" onClose={onClose}><RetainedOverlay permit={panel ? undefined : paintPermit} allowedTypes={listeningPaintTypes} omitTypes={listeningPaintOmissions}><ListeningSurface fence={playback.fence} testID={testID} blocked={!!panel || !wide && !!sheet}>
      {!backgroundHosted && <ListeningPageBackground fence={playback.fence} height={height} ambient={p.ambient}/>}
      {panel === 'signal' && signal ? <View style={{position: 'absolute', inset: 0, zIndex: 30}}><MusicSignalSheet view={signal} onClose={closePanel} contextTitle={p.identity.title} returnFocusRef={signalRef}/></View> : null}
      {panel === 'eq' ? <View style={{position: 'absolute', inset: 0, zIndex: 30, backgroundColor: theme.colors.ink}}><NativeWatchEqualizer onBack={closePanel} copy={equalizerCopy()} reportError={reportError} store={nativeEqualizer}/></View> : null}
      {frame}
    </ListeningSurface></RetainedOverlay></TVMusicFocus>
    <BottomSheet open={paintActive && !wide && !!sheet} onOpenChange={open => {if (!open) setSheet(null);}}
      title={t(sheet === 'chapters' ? 'player.opt.chapters' : sheet === 'lyrics' ? 'player.lyrics' : 'player.queue')} scrollable={false}
      contentStyle={{flex: 1, minHeight: 0}} testID={`${testID}-sheet`} detents={[Math.round(height * .5), Math.round(height * .94)]}>
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


/** The listening cover in the frame's artwork box: the slot the dock's morph measures and paints into, and the physical
 * swipe target (queue / lyrics scrolling and the scrubber keep their own recognisers). */
function ListeningFrameCover({fence, source, measureOnly = false}: {fence: PlaybackFence; source: DockOccupant['presentation']['artwork']; measureOnly?: boolean}) {
  const theme = useTheme(), slot = useRef<View>(null), [size, setSize] = useState(0);
  const gesture = useListeningChromeGesture(fence, measureOnly), present = useListeningPagePresent(fence);
  const sharedCover = useListeningCoverSlot(fence, 'full', slot, !measureOnly || !present);
  const view = <View ref={slot} collapsable={false} onLayout={event => {setSize(event.nativeEvent.layout.width); sharedCover.measure();}} style={{width: '100%', height: '100%'}}>
    {!measureOnly && size > 0 && <Animated.View style={sharedCover.style}><Artwork source={source} width={size} height={size} style={{borderRadius: theme.radii.artwork}}/></Animated.View>}
  </View>;
  return gesture ? <GestureDetector gesture={gesture}>{view}</GestureDetector> : view;
}
/** A pure inset glass surface in the frame's panel place; its contents never feed Android's own blur. */
function ListeningSidePanel({children}: {children: React.ReactNode}) {
  const theme = useTheme(), material = glassRecipe();
  const body = <View style={{flex: 1, backgroundColor: material.nativeBlur ? 'rgba(7,16,24,.44)' : theme.colors.ink}}>{children}</View>;
  return <View style={{flex: 1, marginBottom: 28, borderRadius: 20, overflow: 'hidden', borderWidth: 1, borderColor: 'rgba(237,245,250,.08)'}}>
    {material.nativeBlur ? <BlurView style={{flex: 1}} blurType="dark" blurAmount={30} reducedTransparencyFallbackColor={theme.colors.ink}>{body}</BlurView> : body}
  </View>;
}
export function ListeningPageBackground({fence, height, ambient, fallbackVisible = true}: {fallbackVisible?: boolean; fence: PlaybackFence; height: number; ambient: DockOccupant['presentation']['ambient']}) {
  const theme = useTheme(), reportHeight = useListeningPageHeight(), style = useListeningPageStyle(fence, height, fallbackVisible);
  return <Animated.View pointerEvents="none" accessible={false} accessibilityElementsHidden importantForAccessibility="no-hide-descendants" onLayout={event => reportHeight(event.nativeEvent.layout.height)}
    style={[{position: 'absolute', inset: 0, zIndex: theme.layers.player + 1}, style]}><Layer kind="audio" art={ambient} style={{position: 'absolute', inset: 0}}/>
  </Animated.View>;
}
/** Paint only from the admitted mini descriptor. No full screen, queue/lyrics,
 * song read, guarded callback, controller or output is created by this preview. */
export function ListeningPagePreview({occupant, queue, sidePane = false}: {
  occupant: DockOccupant; queue?: QueuePlaybackView; sidePane?: boolean; width: number; height: number; topInset: number; bottomInset: number;
}) {
  const present = useListeningPagePresent(occupant.playback.fence), wide = useFrameMetrics().form !== 'phone', side = wide && sidePane;
  const window = queue ? nowPlayingQueueWindow(queue) : undefined;
  const current = queue?.queue?.currentEntryId ? nowPlayingQueueEntry(window, queue.queue.currentEntryId) : undefined;
  const segment = current?.available === true ? window?.segments.find(value => value.id === current.segmentId && value.label)?.label : undefined;
  const book = occupant.mode === 'audiobook', p = occupant.presentation, none = () => {};
  // Never seen (measure only); each keeps a distinct label, which the frame keys its controls by.
  const blank = (icon: IconName, skip?: number): AudioControl => ({icon, label: icon, onPress: none, skip});
  // The page's own geometry, measured only: the frame with the same regions the page draws, its cover the measured slot.
  return <View pointerEvents="none" accessible={false} accessibilityElementsHidden importantForAccessibility="no-hide-descendants" style={{position: 'absolute', inset: 0, opacity: present ? 0 : 1}}>
    <View style={{flex: 1, opacity: 0}}>
      <AudioPlayerFrame close={blank('i-down')} context={segment ? t('next.music.playingFrom', {source: ''}).trim() : wide ? undefined : t('player.queueNowPlaying')} source={segment}
        artwork={p.artwork} icon={book ? 'i-book' : 'i-music'} cover={p.artwork ? <ListeningFrameCover fence={occupant.playback.fence} source={p.artwork} measureOnly/> : undefined}
        title={p.identity.title} byline={p.identity.subtitle.map(part => part.text).filter(Boolean)} chapter={book ? {label: '', of: ''} : undefined}
        scrubber={<View/>} times={{position: '', end: ''}} transport={book ? [blank('i-prev'), blank('i-rew', 10), blank('i-play'), blank('i-ffw', 30), blank('i-next')] : [blank('i-shuffle'), blank('i-prev'), blank('i-play'), blank('i-next'), blank('i-repeat')]}
        panel={side ? <View style={{flex: 1}}/> : undefined}/>
    </View>
  </View>;
}


/** Only static audited UI outlives its publisher. This root owns no reader,
 * command, playback lease, queue or song controller. */
function ListeningSurface({fence, testID, children, blocked = false}: {fence: PlaybackFence; testID?: string; children: React.ReactNode; blocked?: boolean}) {
  const mode = useOverlayPaintMode(), theme = useTheme(), active = useListeningPageActive(fence);
  useListeningPageOwner(fence);
  if (!mode.visible || !active) return null;
  return <ListeningChromeGate.Provider value={blocked}><View testID={testID} pointerEvents={mode.interactive ? 'box-none' : 'none'} accessible={false}
    accessibilityElementsHidden={!mode.interactive} importantForAccessibility={mode.interactive ? 'auto' : 'no-hide-descendants'}
    style={{position: 'absolute', inset: 0, zIndex: theme.layers.player}}>{children}</View></ListeningChromeGate.Provider>;
}


function useListeningChromeGesture(fence: PlaybackFence, disabled: boolean) {
  const blocked = useContext(ListeningChromeGate), mode = useOverlayPaintMode();
  const controller = usePublishedPlayerMorph(fence), current = useSharedValue(false), permit = useListeningPaintPermit(fence);
  const allowed = !disabled && !blocked && mode.interactive;
  useLayoutEffect(() => {current.value = allowed; return () => {current.value = false;};}, [current, allowed]);
  return useMemo(() => controller?.chromeGesture(allowed, current, permit?.current), [controller, allowed, current, permit]);
}
function ListeningSwipeArea({fence, children}: {fence: PlaybackFence; children: React.ReactNode}) {
  const gesture = useListeningChromeGesture(fence, false);
  const view = <View style={{width: '100%', alignItems: 'center'}}>{children}</View>;
  return gesture ? <GestureDetector gesture={gesture}>{view}</GestureDetector> : view;
}
