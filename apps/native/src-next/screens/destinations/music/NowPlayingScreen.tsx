import React, {createContext, useCallback, useContext, useEffect, useId, useLayoutEffect, useMemo, useRef, useState} from 'react';
import {BackHandler, ScrollView, StyleSheet, Text, View} from 'react-native';
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
import {AmbientArt, Artwork, StandardBackdrop} from '../../../kit/artwork';
import {Button, Focusable} from '../../../kit/controls';
import {GestureCommitScope} from '../../../kit/controls/gesture-commit';
import {Icon} from '../../../kit/Icon';
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
import type {NativePlaybackReadLifetime} from '../../../platform/playback/types';
import {BlurView, glassRecipe} from '../../../platform/native-blur';
import {BottomSheet, Menu} from '../../../kit/overlays';
import {useAudiobookRead, type AudiobookRead} from './use-audiobook-read';
import {currentListeningChapter, locateListeningChapter, moveListeningChapter, listeningChapterSeek} from './audiobook-controls';
import type {NativeListeningSong} from '../../../shell/listening-song-detail';
import {RetainedOverlay} from '../../../kit/overlays/SurfacePaintHost';
import {usePublishedPlayerMorph} from '../../../kit/dock/PlayerMorphBridge';
import {useOverlayPaintMode} from '../../../kit/overlays/OverlayPaintContext';
import {NowPlayingLyrics, type NowPlayingLyricsProps} from './NowPlayingLyrics';
import {ListeningQueuePaint, ListeningLyricsPaint} from './ListeningPanePaint';
import {useTVPanelPresence} from '../../../kit/overlays/TVPanelMotion';

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
  audiobook?: Readonly<{read: AudiobookRead; setRate: (fence: PlaybackFence, rate: number) => void; setSleepTimer?: (fence: PlaybackFence, minutes: number | null) => Promise<void>}>;
  sleepAtMs?: number | null;
  onClose: () => void;
  /** music-plus (MC-8): the committed plan plus the engine's own current-song facts. */
  signal?: MusicSignalView;
  /** listening-notices: a pause the viewer did not make. */
  notice?: MessageId;
  reportError?: (error: unknown) => void;
  testID?: string;
}>;

const QUEUE_WIDTH = 366;
const SIDE_BY_SIDE = 700;
const ListeningChromeGate = createContext(false);
const listeningPaintTypes = [ListeningSurface, ListeningSwipeArea, ListeningColumn, ListeningSidePanel, ListeningPageBackground, ListeningGroup, ListeningSeekPaint, ListeningQueuePaint, ListeningLyricsPaint, MusicFocusRegion, MusicSignalLine, MusicSignalSheet, ListeningFavoritePaint, View, Text, ScrollView, Artwork, AmbientArt, Button, Focusable, Icon, React.Fragment];
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

function TouchNowPlayingScreen({occupant, queue, artwork, width, height, topInset = 0, bottomInset = 0, backgroundHosted = false, onAction, onQueuePlay, queuePolicy, policyLifetime, retry, playOn, queueEdit, song, onClose, signal, notice, reportError = () => {}, testID = 'now-playing', lyrics, audiobook, sleepAtMs = null}: NowPlayingScreenProps) {
  const theme = useTheme(), {presentation: p, playback} = occupant, book = occupant.mode === 'audiobook';
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
  const [sheet, setSheet] = useState<'queue' | 'lyrics' | 'chapters' | null>(() => width < SIDE_BY_SIDE ? null : book ? 'chapters' : queue ? 'queue' : lyrics ? 'lyrics' : null);
  const [panel, setPanel] = useState<'signal' | 'eq' | 'speed' | 'sleep' | 'more' | null>(null);
  const moreRef = useRef<View | null>(null);
  const {reader: bookReader, snapshot: bookSnapshot} = useAudiobookRead(book && paintActive ? audiobook?.read : undefined, playback.fence, reportError);
  const reportBookError = useCallback((error: unknown) => {if (bookReader?.current()) reportError(error);}, [bookReader, reportError]);
  const [browsingChapters, setBrowsingChapters] = useState(false);
  const [chapterBusy, setChapterBusy] = useState(false), chapterRead = useRef<object | undefined>(undefined);
  useEffect(() => {chapterRead.current = undefined; setChapterBusy(false); setBrowsingChapters(false);}, [bookReader]);
  useEffect(() => {
    setPanel(null);
    setSheet(open => book ? open && open !== 'chapters' ? width >= SIDE_BY_SIDE ? 'chapters' : null : open
      : open === 'chapters' ? width >= SIDE_BY_SIDE ? queue ? 'queue' : lyrics ? 'lyrics' : null : null : open);
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
    const back = BackHandler.addEventListener('hardwareBackPress', () => {closePanel(); return true;});
    const release = theme.isTV ? registerOverlayKeys(key => {if (key === 'escape' || key === 'menu') {closePanel(); return true;} return false;}) : () => {};
    return () => {back.remove(); release();};
  }, [panel, closePanel, theme.isTV]);
  const equalizer = useSyncExternalStore(nativeEqualizer.subscribe, nativeEqualizer.getSnapshot);
  const wide = width >= SIDE_BY_SIDE, side = wide && sheet !== null, position = playbackPosition(occupant);
  const chapter = currentListeningChapter(bookSnapshot, position), bookFacts = bookSnapshot.phase === 'current' ? bookSnapshot : undefined;
  const chapterData = bookFacts?.chapters.data;
  const fullBounds = seekBounds(occupant);
  const bounds = book && !wide && chapter && fullBounds ? {start: Math.max(fullBounds.start, chapter.startSeconds), end: Math.min(fullBounds.end, chapter.endSeconds)} : fullBounds;
  const span = bounds ? Math.max(0, bounds.end - bounds.start) : 0;
  const inputActive = paintActive && !panel && (wide || !sheet) && !!bounds && p.capabilities.seek && span > 0;
  useEffect(() => {
    if (!bookReader || browsingChapters || chapter || chapterRead.current || bookFacts?.chapters.phase !== 'ready' || chapterData?.status !== 'available') return;
    const first = chapterData.chapters[0], last = chapterData.chapters.at(-1);
    if (!first || !last || !(position < first.startSeconds && bookFacts.chapters.pagination.canPrevious || position >= last.endSeconds && chapterData.nextCursor)) return;
    const run = {}; chapterRead.current = run; setChapterBusy(true);
    void locateListeningChapter(bookReader, position).catch(reportBookError).finally(() => {
      if (chapterRead.current === run) {chapterRead.current = undefined; setChapterBusy(false);}
    });
  }, [bookReader, chapter, bookFacts?.chapters.phase, bookFacts?.chapters.pagination.canPrevious, chapterData, position, browsingChapters, reportBookError]);
  useEffect(() => {if (sheet !== 'chapters') setBrowsingChapters(false);}, [sheet]);
  const seekBook = (seconds: number) => {
    if (!bookReader?.current() || paintPermit && !paintPermit.current()) return;
    const command = seekTo(occupant, seconds); if (command) onAction(command);
  };
  const moveChapter = (direction: -1 | 1) => {
    if (!bookReader || chapterRead.current || !p.capabilities.seek) return;
    const run = {}; chapterRead.current = run; setChapterBusy(true);
    void moveListeningChapter(bookReader, position, direction, seekBook).catch(reportBookError).finally(() => {
      if (chapterRead.current === run) {chapterRead.current = undefined; setChapterBusy(false);}
    });
  };
  const sleepMinutes = sleepAtMs === null ? null : Math.max(0, Math.ceil((sleepAtMs - Date.now()) / 60_000));
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
  const panelWidth = width >= 1024 ? 380 : QUEUE_WIDTH;
  const cover = book ? Math.min(wide ? 250 : 290, width - 48) : wide ? Math.max(160, Math.min(410, width - (side ? panelWidth + 40 : 0) - 56)) : width - 48;
  const smallTransport = wide ? 52 : 48, playSize = wide ? 74 : 76;
  const outerTransport = {width: smallTransport, height: smallTransport, flexShrink: 0};
  const bookRemaining = book && !wide && fullBounds ? durationText(Math.max(0, fullBounds.end - position), t) : undefined;
  const bookOption = {flex: 1, height: 54, alignItems: 'center', justifyContent: 'center', gap: 2, borderRadius: 12,
    overflow: 'hidden', backgroundColor: alpha(theme.colors.text, .12)} as const;
  const bookOptionActive = {backgroundColor: alpha(theme.colors.accent, .22), borderWidth: 1, borderColor: alpha(theme.colors.accentStrong, .5)};
  const bookGlass = glassRecipe().nativeBlur ? <BlurView pointerEvents="none" style={StyleSheet.absoluteFill} blurType="dark" blurAmount={16} reducedTransparencyFallbackColor={theme.colors.raisedHigh}/> : null;
  const chapterPane = <View testID={`${testID}-chapters`} style={{flex: 1, minHeight: 0}}>
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
  </View>;
  const header = !wide ? <View style={{width: '100%', minHeight: 48, marginHorizontal: -10, flexDirection: 'row', alignItems: 'center', marginBottom: 22}}>
    <Button variant="text" iconOnly icon="i-down" iconColor={theme.colors.text} iconSize={26} style={{width: 48, height: 48}} accessibilityLabel={t('player.minimize')} onPress={onClose}/>
    <View style={{flex: 1, alignItems: 'center', gap: 2}}>
      <Text style={[browseType(theme, 12, 600), {color: theme.colors.textSecondary}]}>{playingFrom ? t('next.music.playingFrom', {source: ''}).trim() : t('player.queueNowPlaying')}</Text>
      {playingFrom ? <Text numberOfLines={1} style={[browseType(theme, 14.5, 700), {color: theme.colors.text}]}>{playingFrom}</Text> : null}
    </View>
    <Button ref={moreRef} variant="text" iconOnly icon="i-more" iconColor={theme.colors.text} iconSize={22} style={{width: 48, height: 48}}
      accessibilityLabel={t('card.more', {title: p.identity.title})} onPress={() => setPanel('more')}/>
  </View> : undefined;
  const bookHorizontal = book && wide && width - (side ? panelWidth + 40 : 0) - 56 >= 560;
  const identity = <View style={{width: '100%', flexDirection: 'row', alignItems: 'center', gap: 12, marginTop: bookHorizontal ? 0 : 26}}>
      <View style={{flex: 1, minWidth: 0}}>
        <Text numberOfLines={2} accessibilityRole="header" style={[browseType(theme, bookHorizontal ? 38 : wide ? 34 : 23, 800, wide ? -0.03 : -0.025), {textAlign: wide && !bookHorizontal ? 'center' : 'left', color: theme.colors.text}]}>{p.identity.title}</Text>
        {facts.length ? <View style={{flexDirection: 'row', flexWrap: 'wrap', justifyContent: wide && !bookHorizontal ? 'center' : 'flex-start', columnGap: 10, rowGap: 4, marginTop: wide ? 8 : 2}}>
          {facts.map((part, index) => <Text key={`${part.id}-${index}`} style={[browseType(theme, wide ? 15 : 16, wide ? 500 : 600), {color: theme.colors.textSecondary}]}>{part.text}</Text>)}
        </View> : null}
        {wide && album.length ? <View style={{flexDirection: 'row', flexWrap: 'wrap', justifyContent: 'center', gap: 10, marginTop: 4}}>
          {album.map((part, index) => <Text key={`${part.id}-${index}`} style={[browseType(theme, 13, 500), {color: theme.colors.textTertiary}]}>{part.text}</Text>)}
        </View> : null}
      </View>
      {favoritePaint ? <Button variant="text" iconOnly icon={favoritePaint.favorite ? 'i-heart-f' : 'i-heart'} iconColor={theme.colors.text} iconSize={24}
        style={{width: 48, height: 48, flexShrink: 0}} disabled={favoritePaint.pending} accessibilityState={{checked: favoritePaint.favorite}}
        accessibilityLabel={t(favoritePaint.favorite ? 'entry.removeFromFavorites' : 'entry.addToFavorites')}
        onPress={() => {if (song?.token) song.favorite(song.token, !favoritePaint.favorite);}}/> : null}
    </View>;
  const column = <ListeningColumn testID={`${testID}-column`} fence={playback.fence} source={p.artwork} playingFrom={wide ? playingFrom : undefined} header={header} cover={cover} wide={wide} height={height} horizontal={bookHorizontal}
    identity={bookHorizontal ? <ListeningGroup fence={playback.fence} index={0} height={height} style={{width: undefined, flex: 1, minWidth: 0, marginBottom: 6}}>{identity}</ListeningGroup> : undefined}>
    <ListeningSwipeArea fence={playback.fence}><ListeningGroup fence={playback.fence} index={0} height={height}>
    {!bookHorizontal ? identity : null}
    {signal ? <MusicFocusRegion id={`${focus}.signal`} entry="music-signal-line" edges={{down: `${focus}.progress`}} style={{alignSelf: 'center', marginTop: 12}}><MusicSignalLine view={signal} onPress={() => setPanel('signal')}/></MusicFocusRegion> : null}
    {retry?.enabled ? <View style={{marginTop: 16}}><Button testID={`${testID}-retry`} variant="primary" icon="i-retry"
      onPress={() => retry.run(playback.fence)}>{t('next.states.tryAgain')}</Button></View> : null}
    {book && chapter ? <View testID={`${testID}-chapter-line`} style={{width: '100%', maxWidth: 600, marginTop: wide ? 26 : 18, flexDirection: 'row', alignItems: 'center', gap: wide ? 8 : 6}}>
      <Icon name="i-list" size={18} color={theme.colors.textSecondary}/>
      <Text style={[browseType(theme, wide ? 14 : 13.5, 700), {flexShrink: 1, color: wide ? theme.colors.text : theme.colors.textSecondary}]}>{chapter.title}</Text>
      <Text style={[browseType(theme, wide ? 13 : 13.5, wide ? 500 : 600), {marginLeft: wide ? undefined : 'auto', color: wide ? theme.colors.textSecondary : theme.colors.textTertiary}]}>{wide ? `${clockText(Math.max(0, position - chapter.startSeconds))} / ${clockText(chapter.endSeconds - chapter.startSeconds)}` : `${chapter.index}/${chapterData?.totalCount}`}</Text>
    </View> : null}
    </ListeningGroup></ListeningSwipeArea>
    <ListeningGroup fence={playback.fence} index={1} height={height}>
    <View style={{width: '100%', maxWidth: book ? 600 : 520, marginTop: book ? wide ? 8 : 12 : 20}}>
      <View>
      <ListeningSeekPaint fence={playback.fence} fraction={fraction} span={span} seconds={position - (bounds?.start ?? 0)}
        label={t('player.position')} testID={`${testID}-scrubber`} fallbackGesture={hostedScrubber ? undefined : scrub} fallbackAdjust={hostedScrubber || !inputActive ? undefined : adjustSeek}/>
      {book && wide && bounds && span > 0 ? <View testID={`${testID}-chapter-ticks`} pointerEvents="none" style={{position: 'absolute', left: 0, right: 0, top: 12, height: 4}}>
        {chapterData?.chapters.filter(value => value.startSeconds > bounds.start && value.startSeconds < bounds.end).map(value => <View key={value.id}
          style={{position: 'absolute', top: 0, bottom: 0, width: 2, marginLeft: -1, left: `${(value.startSeconds - bounds.start) / span * 100}%`, backgroundColor: alpha(theme.colors.ink, .75)}}/>)}
      </View> : null}
      </View>
      <View style={{flexDirection: 'row', justifyContent: 'space-between', marginTop: 2}}>
        <Text style={[browseType(theme, 12.5, 600), {color: theme.colors.textSecondary, fontVariant: ['tabular-nums']}]}>{clockText(Math.max(0, position - (bounds?.start ?? 0)))}</Text>
        <Text style={[browseType(theme, 12.5, 600), {color: theme.colors.textTertiary, fontVariant: ['tabular-nums']}]}>{!wide || book ? `-${clockText(Math.max(0, (span || p.durationSeconds || 0) - Math.max(0, position - (bounds?.start ?? 0))))}` : clockText(span || p.durationSeconds)}</Text>
      </View>
      {bookRemaining ? <Text testID={`${testID}-book-remaining`} style={[browseType(theme, 12, 600), {textAlign: 'center', color: theme.colors.textSecondary, marginTop: -14}]}>{t('next.kit.remaining', {time: bookRemaining})}</Text> : null}
    </View>
    <MusicFocusRegion id={`${focus}.transport`} entry="toggle" orientation="horizontal" style={{width: '100%', maxWidth: book ? 600 : 520, flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', marginTop: book ? 14 : 18}}>
      {book ? <Button testID={`${testID}-previous-chapter`} variant="text" iconOnly icon="i-prev" iconColor={theme.colors.text} iconSize={28} style={outerTransport}
        accessibilityLabel={t('player.previous')} disabled={!bookReader || !p.capabilities.seek || chapterBusy || !!chapter && chapter.index <= 1} onPress={() => moveChapter(-1)}/>
        : policyReady ? <Button testID={`${testID}-shuffle`} variant="text" iconOnly icon="i-shuffle" iconColor={shuffled ? theme.colors.accentStrong : theme.colors.textSecondary} iconSize={22} style={outerTransport}
          accessibilityLabel={t('next.dock.shuffle')} accessibilityState={{selected: shuffled}} onPress={() => changePolicy({action: 'shuffle', shuffled: !shuffled})}/> : null}
      {book ? <Focusable testID={`${testID}-skip-back`} accessibilityRole="button" accessibilityLabel={t('player.skipBackSeconds', {seconds: skipBack})} disabled={!p.capabilities.seek || !bookReader}
        onPress={() => seekBook(position - skipBack)} style={[outerTransport, {alignItems: 'center', justifyContent: 'center'}]}>
        <Icon name="i-rew" size={38} color={theme.colors.text}/><Text style={[browseType(theme, 10.5, 800), {position: 'absolute', color: theme.colors.text}]}>{skipBack}</Text>
      </Focusable> : <Button testID={`${testID}-previous`} variant="text" iconOnly icon="i-prev" iconColor={theme.colors.text} iconSize={wide ? 26 : 34} style={outerTransport}
        accessibilityLabel={t('player.previous')} disabled={!p.capabilities.previous} onPress={() => act('previous')}/>}
      <Button testID={`${testID}-toggle`} variant="primary" iconOnly icon={playback.playing ? 'i-pause' : 'i-play'} iconSize={wide ? 30 : 34}
        style={{height: playSize, width: playSize, borderRadius: playSize / 2, flexShrink: 0}} accessibilityLabel={t(playback.playing ? 'action.pause' : 'action.play')}
        disabled={playback.playing ? !p.capabilities.pause : !p.capabilities.play} onPress={() => act(playback.playing ? 'pause' : 'play')}/>
      {book ? <Focusable testID={`${testID}-skip-forward`} accessibilityRole="button" accessibilityLabel={t('player.skipForwardSeconds', {seconds: skipForward})} disabled={!p.capabilities.seek || !bookReader}
        onPress={() => seekBook(position + skipForward)} style={[outerTransport, {alignItems: 'center', justifyContent: 'center'}]}>
        <Icon name="i-ffw" size={38} color={theme.colors.text}/><Text style={[browseType(theme, 10.5, 800), {position: 'absolute', color: theme.colors.text}]}>{skipForward}</Text>
      </Focusable> : <Button testID={`${testID}-next`} variant="text" iconOnly icon="i-next" iconColor={theme.colors.text} iconSize={wide ? 26 : 34} style={outerTransport}
        accessibilityLabel={t('player.next')} disabled={!p.capabilities.next} onPress={() => act('next')}/>}
      {book ? <Button testID={`${testID}-next-chapter`} variant="text" iconOnly icon="i-next" iconColor={theme.colors.text} iconSize={28} style={outerTransport}
        accessibilityLabel={t('player.next')} disabled={!bookReader || !p.capabilities.seek || chapterBusy || !!chapter && chapter.index >= (chapterData?.totalCount ?? 0)} onPress={() => moveChapter(1)}/>
        : policyReady ? <Button testID={`${testID}-repeat`} variant="text" iconOnly icon="i-repeat" iconColor={repeat === 'off' ? theme.colors.textSecondary : theme.colors.accentStrong} iconSize={22} style={outerTransport}
          accessibilityLabel={t(repeat === 'off' ? 'next.dock.repeatOff' : repeat === 'all' ? 'next.dock.repeatAll' : 'next.dock.repeatOne')}
          accessibilityState={{selected: repeat !== 'off'}} onPress={() => changePolicy({action: 'repeat', repeat: repeat === 'off' ? 'all' : repeat === 'all' ? 'one' : 'off'})}/> : null}
    </MusicFocusRegion>
    </ListeningGroup>
    <ListeningGroup fence={playback.fence} index={2} height={height}>
    {notice ? <View accessibilityLiveRegion="polite" accessibilityRole="alert" testID={`${testID}-notice`} style={{flexDirection: 'row', alignItems: 'center', gap: 8, marginTop: 14, paddingHorizontal: 12, paddingVertical: 7, borderRadius: 10, backgroundColor: 'rgba(7,16,24,.5)'}}>
      <Icon name="i-pause" size={14} color={theme.colors.accentStrong}/><Text style={[browseType(theme, 13, 600), {color: theme.colors.textSecondary}]}>{t(notice)}</Text>
    </View> : null}
    {book && wide ? <View style={{width: '100%', maxWidth: 600, gap: 8, marginTop: 18}}>
      <View style={{flexDirection: 'row', alignItems: 'center', gap: 12}}>
        <Text style={[browseType(theme, 14, 700), {width: 124, color: theme.colors.text}]}>{t('player.opt.speed')}</Text>
        <ScrollView horizontal showsHorizontalScrollIndicator={false} style={{flex: 1, minWidth: 0, borderRadius: 12, backgroundColor: 'rgba(237,245,250,.08)'}} contentContainerStyle={{padding: 3, gap: 2}}>
          {(bookFacts?.rate.supported ?? []).map(rate => <Focusable key={rate} accessibilityRole="radio" accessibilityLabel={`${rate}×`}
            accessibilityState={{checked: (bookFacts?.rate.effective ?? bookFacts?.rate.selected) === rate}}
            onPress={() => {if (bookReader?.current() && (!paintPermit || paintPermit.current())) audiobook?.setRate(playback.fence, rate);}}
            style={{minWidth: 44, height: 44, paddingHorizontal: 4, alignItems: 'center', justifyContent: 'center', borderRadius: 9,
              backgroundColor: (bookFacts?.rate.effective ?? bookFacts?.rate.selected) === rate ? 'rgba(64,159,212,.24)' : 'transparent'}}>
            <Text style={[browseType(theme, 13.5, 700), {color: theme.colors.text}]}>{rate}×</Text>
          </Focusable>)}
        </ScrollView>
      </View>
      <View style={{flexDirection: 'row', alignItems: 'center', gap: 12}}>
        <View style={{width: 124}}><Text style={[browseType(theme, 14, 700), {color: theme.colors.text}]}>{t('player.opt.sleepTimer')}</Text>
          {sleepMinutes === null ? null : <Text style={[browseType(theme, 12, 600), {color: theme.colors.textTertiary, marginTop: 2}]}>{t('player.opt.minutes', {count: sleepMinutes})}</Text>}
        </View>
        <ScrollView horizontal showsHorizontalScrollIndicator={false} style={{flex: 1, minWidth: 0, borderRadius: 12, backgroundColor: 'rgba(237,245,250,.08)'}} contentContainerStyle={{padding: 3, gap: 2}}>
          {[null, 15, 30, 45, 60, 90].map(minutes => <Focusable key={minutes ?? 'off'} disabled={!bookReader || !audiobook?.setSleepTimer}
            accessibilityRole="radio" accessibilityState={{checked: sleepMinutes === minutes}}
            onPress={() => {if (bookReader?.current() && (!paintPermit || paintPermit.current())) void audiobook?.setSleepTimer?.(playback.fence, minutes).catch(reportBookError);}}
            style={{minWidth: 44, height: 44, paddingHorizontal: 8, alignItems: 'center', justifyContent: 'center', borderRadius: 9,
              backgroundColor: sleepMinutes === minutes ? 'rgba(64,159,212,.24)' : 'transparent'}}>
            <Text style={[browseType(theme, 13.5, 700), {color: theme.colors.text}]}>{minutes === null ? t('player.opt.off') : t('player.opt.minutes', {count: minutes})}</Text>
          </Focusable>)}
        </ScrollView>
      </View>
    </View> : null}
    <View style={{width: book && !wide ? '100%' : undefined, flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', justifyContent: 'center', gap: book && !wide ? 8 : 10, marginTop: book ? 18 : 26, marginBottom: 24}}>
      {book ? <>
        {!wide ? <>
          <Focusable testID={`${testID}-speed`} accessibilityRole="button" disabled={!bookFacts?.rate.supported?.length} onPress={() => setPanel('speed')}
            style={bookOption}>{bookGlass}
            <Text style={[browseType(theme, 15, 800), {color: theme.colors.text}]}>{bookFacts?.rate.effective ?? bookFacts?.rate.selected ?? 1}×</Text>
            <Text style={[browseType(theme, 11.5, 700), {color: theme.colors.textSecondary}]}>{t('player.opt.speed')}</Text>
          </Focusable>
          <Focusable testID={`${testID}-sleep`} accessibilityRole="button" disabled={!bookReader || !audiobook?.setSleepTimer} onPress={() => setPanel('sleep')}
            style={[bookOption, sleepMinutes !== null && bookOptionActive]}>{bookGlass}
            <View style={{flexDirection: 'row', alignItems: 'center', gap: 4}}><Icon name="i-moon" size={15} color={sleepMinutes === null ? theme.colors.text : theme.colors.accentStrong}/>
              {sleepMinutes === null ? null : <Text style={[browseType(theme, 15, 800), {color: theme.colors.text}]}>{t('player.opt.minutes', {count: sleepMinutes})}</Text>}
            </View>
            <Text style={[browseType(theme, 11.5, 700), {color: theme.colors.textSecondary}]}>{t('player.opt.sleepTimer')}</Text>
          </Focusable>
          <Focusable testID={`${testID}-chapters-toggle`} accessibilityRole="button" disabled={!bookReader} accessibilityState={{expanded: sheet === 'chapters'}}
            onPress={() => setSheet(open => open === 'chapters' ? null : 'chapters')} style={[bookOption, sheet === 'chapters' && bookOptionActive]}>{bookGlass}
            <View style={{flexDirection: 'row', alignItems: 'center', gap: 4}}><Icon name="i-list" size={16} color={theme.colors.text}/>
              <Text style={[browseType(theme, 15, 800), {color: theme.colors.text}]}>{chapterData?.totalCount}</Text>
            </View>
            <Text style={[browseType(theme, 11.5, 700), {color: theme.colors.textSecondary}]}>{t('player.opt.chapters')}</Text>
          </Focusable>
        </> : <Button testID={`${testID}-chapters-toggle`} variant={sheet === 'chapters' ? 'glass' : 'text'} size="small" icon="i-list" disabled={!bookReader}
          accessibilityState={{expanded: sheet === 'chapters'}} onPress={() => setSheet(open => open === 'chapters' ? null : 'chapters')}>{t('player.opt.chapters')}</Button>}
      </> : <>
        {queue ? <Button testID={`${testID}-queue-toggle`} variant={sheet === 'queue' ? 'primary' : 'glass'} size="small" icon="i-queue"
          accessibilityState={{expanded: sheet === 'queue'}} onPress={() => setSheet(open => open === 'queue' ? null : 'queue')}>{t('player.queue')}</Button> : null}
        {lyrics ? <Button testID={`${testID}-lyrics-toggle`} variant={sheet === 'lyrics' ? 'primary' : 'glass'} size="small" icon="i-cc"
          accessibilityState={{expanded: sheet === 'lyrics'}} onPress={() => setSheet(open => open === 'lyrics' ? null : 'lyrics')}>{t('player.lyrics')}</Button> : null}
      </>}
      {playOn?.enabled && book && !wide ? <Focusable ref={playOnButton} accessibilityRole="button" accessibilityLabel={t('player.playOn')} onPress={() => playOn.open(playback.fence, playOnButton)}
        style={bookOption}>{bookGlass}<Icon name="i-cast" size={17} color={theme.colors.text}/>
        <Text style={[browseType(theme, 11.5, 700), {color: theme.colors.textSecondary}]}>{t('device.thisDevice')}</Text></Focusable> : playOn?.enabled ? <Button ref={playOnButton} testID={`${testID}-play-on`} variant="glass" size="small" icon="i-cast" accessibilityLabel={t('player.playOn')}
        onPress={() => playOn.open(playback.fence, playOnButton)}>{t('device.thisDevice')}</Button> : null}
      {wide && equalizer.supported ? <Button testID={`${testID}-equalizer`} variant="glass" size="small" icon="i-gauge" accessibilityLabel={t('next.media.eq.title')} onPress={() => setPanel('eq')}>{t('next.media.eq.title')}</Button> : null}
      {wide ? <Button testID={`${testID}-close`} variant="glass" size="small" icon="i-down" accessibilityLabel={t('player.minimize')} onPress={onClose}>{t('action.close')}</Button> : null}
    </View>
    </ListeningGroup>
  </ListeningColumn>;
  return <>
    {paintActive && showingQueue && queue && <NowPlayingQueue paintOwner={paneOwner} paintScope={fenceKey} testID={`${testID}-queue`} view={queue} onPlay={onQueuePlay} artwork={artwork} queueEdit={queueEditPort}/>}
    {paintActive && showingLyrics && knownLyrics && React.cloneElement(knownLyrics, {paintOwner: paneOwner, paintScope: fenceKey})}
    <TVMusicFocus id={focus} visible={!panel} entry="toggle" onClose={onClose}><RetainedOverlay permit={panel ? undefined : paintPermit} allowedTypes={listeningPaintTypes} omitTypes={listeningPaintOmissions}><ListeningSurface fence={playback.fence} testID={testID} blocked={!!panel || !wide && !!sheet}>
      {!backgroundHosted && <ListeningPageBackground fence={playback.fence} height={height} ambient={p.ambient}/>}
      <View style={{flex: 1, paddingTop: topInset, paddingBottom: bottomInset}}>
        {panel === 'signal' && signal ? <View style={{position: 'absolute', inset: 0, zIndex: 30}}><MusicSignalSheet view={signal} onClose={closePanel} contextTitle={p.identity.title} returnFocusRef={signalRef}/></View> : null}
        {panel === 'eq' ? <View style={{position: 'absolute', inset: 0, zIndex: 30, backgroundColor: theme.colors.ink}}><NativeWatchEqualizer onBack={closePanel} copy={equalizerCopy()} reportError={reportError} store={nativeEqualizer}/></View> : null}
        <View style={{flex: 1, flexDirection: 'row'}}>
          {column}
          {side ? <View style={{width: panelWidth + 40}}/> : null}
        </View>
        {side ? <ListeningSidePanel width={panelWidth}>
          <View style={{flexDirection: 'row', alignItems: 'center', gap: 10, padding: 14}}>
            {book && queue ? <>
              <Button testID={`${testID}-tab-chapters`} variant={sheet === 'chapters' ? 'primary' : 'glass'} size="small" icon="i-list" onPress={() => setSheet('chapters')}>{t('player.opt.chapters')}</Button>
              <Button testID={`${testID}-tab-queue`} variant={sheet === 'queue' ? 'primary' : 'glass'} size="small" icon="i-queue" onPress={() => setSheet('queue')}>{t('player.queue')}</Button>
            </> : !book && queue && lyrics ? <>
              <Button testID={`${testID}-tab-queue`} variant={sheet === 'queue' ? 'primary' : 'glass'} size="small" icon="i-queue" onPress={() => setSheet('queue')}>{t('player.queue')}</Button>
              <Button testID={`${testID}-tab-lyrics`} variant={sheet === 'lyrics' ? 'primary' : 'glass'} size="small" icon="i-cc" onPress={() => setSheet('lyrics')}>{t('player.lyrics')}</Button>
            </> : <Text style={[browseType(theme, 20, 800, -0.02), {color: theme.colors.text}]}>{t(sheet === 'chapters' ? 'player.opt.chapters' : sheet === 'lyrics' ? 'player.lyrics' : 'player.queue')}</Text>}
          </View>
          {sheet === 'chapters' ? chapterPane : showingLyrics ? lyricPaint : queuePaint}
        </ListeningSidePanel> : null}
      </View>
    </ListeningSurface></RetainedOverlay></TVMusicFocus>
    <BottomSheet open={paintActive && !wide && !!sheet} onOpenChange={open => {if (!open) setSheet(null);}}
      title={t(sheet === 'chapters' ? 'player.opt.chapters' : sheet === 'lyrics' ? 'player.lyrics' : 'player.queue')} scrollable={false}
      contentStyle={{flex: 1, minHeight: 0}} testID={`${testID}-sheet`} detents={[.5, .94]}>
      {sheet === 'chapters' ? chapterPane : sheet === 'lyrics' ? lyricPaint : queuePaint}
    </BottomSheet>
    <Menu open={paintActive && panel === 'speed'} onOpenChange={open => {if (!open) setPanel(null);}} title={t('player.opt.speed')} testID={`${testID}-speed-menu`}
      items={(bookFacts?.rate.supported ?? []).map(rate => ({id: String(rate), label: rate === 1 ? t('player.opt.normalSpeed') : `${rate}×`, selected: (bookFacts?.rate.effective ?? bookFacts?.rate.selected) === rate,
        onSelect: () => {if (bookReader?.current() && (!paintPermit || paintPermit.current())) audiobook?.setRate(playback.fence, rate);}}))}/>
    <Menu open={paintActive && panel === 'sleep'} onOpenChange={open => {if (!open) setPanel(null);}} title={t('player.opt.sleepTimer')} testID={`${testID}-sleep-menu`}
      items={[null, 15, 30, 45, 60, 90].map(minutes => ({id: minutes === null ? 'off' : String(minutes), label: minutes === null ? t('player.opt.off') : t('player.opt.minutes', {count: minutes}), selected: sleepMinutes === minutes,
        onSelect: () => {if (bookReader?.current() && (!paintPermit || paintPermit.current())) void audiobook?.setSleepTimer?.(playback.fence, minutes).catch(reportBookError);}}))}/>
    <Menu open={paintActive && panel === 'more'} onOpenChange={open => {if (!open) setPanel(null);}} title={p.identity.title} anchorRef={moreRef} touchContext
      items={[
        ...(book && queue ? [{id: 'queue', label: t('player.queue'), icon: 'i-queue' as const, onSelect: () => setSheet('queue')}] : []),
        ...(signal ? [{id: 'signal', label: t('player.opt.info'), icon: 'i-info' as const, closeOnSelect: false, onSelect: () => setPanel('signal')}] : []),
        ...(equalizer.supported ? [{id: 'equalizer', label: t('next.media.eq.title'), icon: 'i-gauge' as const, closeOnSelect: false, onSelect: () => setPanel('eq')}] : []),
        {id: 'stop', label: t('action.stop'), icon: 'i-stop', disabled: !p.capabilities.stop, onSelect: () => act('stop')},
      ]}/>
  </>;

}


function ListeningColumn({fence, source, playingFrom, header, identity, horizontal = false, cover, wide, height, measureOnly = false, testID, children}: {
  fence: PlaybackFence; source: DockOccupant['presentation']['artwork']; playingFrom?: string; cover: number; wide: boolean; height: number;
  header?: React.ReactNode; identity?: React.ReactNode; horizontal?: boolean; measureOnly?: boolean; testID?: string; children?: React.ReactNode;
}) {
  const theme = useTheme(), coverSlot = useRef<View>(null);
  const gesture = useListeningChromeGesture(fence, measureOnly);
  const present = useListeningPagePresent(fence);
  const sharedCover = useListeningCoverSlot(fence, 'full', coverSlot, !measureOnly || !present);
  return <ScrollView onScroll={sharedCover.measure} scrollEventThrottle={16} pointerEvents={measureOnly ? 'none' : 'auto'}
    contentContainerStyle={{flexGrow: 1, ...(wide ? {justifyContent: 'center' as const} : {})}}
    style={{flex: 1, minWidth: 0, ...(measureOnly ? {opacity: 0} : {})}} showsVerticalScrollIndicator={false}>
    <View testID={testID} style={{flex: 1, minWidth: 0, alignItems: 'center', paddingHorizontal: wide ? 28 : 24, paddingTop: wide ? 36 : 0}}>
      {header}
      {playingFrom ? <ListeningGroup fence={fence} index={0} height={height}><Text numberOfLines={1} style={[browseType(theme, 13, 600), {marginBottom: 14, textAlign: 'center', color: theme.colors.textSecondary}]}>{t('next.music.playingFrom', {source: playingFrom})}</Text></ListeningGroup> : null}
      <View style={horizontal ? {width: '100%', maxWidth: 600, flexDirection: 'row', alignItems: 'flex-end', gap: 26} : undefined}>
      {source ? <ListeningCoverTarget gesture={gesture} slot={coverSlot} onLayout={sharedCover.measure} size={cover}>
        {!measureOnly && <Animated.View style={sharedCover.style}><Artwork source={source} width={cover} height={cover} style={{borderRadius: 14}}/></Animated.View>}
      </ListeningCoverTarget> : null}
        {identity}
      </View>
      {children}
    </View>
  </ScrollView>;
}
/** A pure inset glass surface; its contents never feed Android's own blur. */
function ListeningSidePanel({width, children}: {width: number; children: React.ReactNode}) {
  const theme = useTheme(), material = glassRecipe();
  const body = <View style={{flex: 1, backgroundColor: material.nativeBlur ? 'rgba(7,16,24,.44)' : theme.colors.ink}}>{children}</View>;
  return <View style={{position: 'absolute', right: 20, top: 100, bottom: 28, width, borderRadius: 20, overflow: 'hidden', borderWidth: 1, borderColor: 'rgba(237,245,250,.08)'}}>
    {material.nativeBlur ? <BlurView style={{flex: 1}} blurType="dark" blurAmount={30} reducedTransparencyFallbackColor={theme.colors.ink}>{body}</BlurView> : body}
  </View>;
}
export function ListeningPageBackground({fence, height, ambient, fallbackVisible = true}: {fallbackVisible?: boolean; fence: PlaybackFence; height: number; ambient: DockOccupant['presentation']['ambient']}) {
  const theme = useTheme(), reportHeight = useListeningPageHeight(), style = useListeningPageStyle(fence, height, fallbackVisible);
  return <Animated.View pointerEvents="none" accessible={false} accessibilityElementsHidden importantForAccessibility="no-hide-descendants" onLayout={event => reportHeight(event.nativeEvent.layout.height)}
    style={[{position: 'absolute', inset: 0, zIndex: theme.layers.player + 1}, style]}><StandardBackdrop/>
    {ambient ? <View style={{position: 'absolute', top: 0, left: 0, right: 0, height: Math.min(height, 720), overflow: 'hidden'}}>
      <AmbientArt source={ambient} treatment="title" style={{position: 'absolute', inset: 0}}/>
      <View style={{position: 'absolute', inset: 0, backgroundColor: 'rgba(7,16,24,.5)'}}/>
    </View> : null}
  </Animated.View>;
}
/** Paint only from the admitted mini descriptor. No full screen, queue/lyrics,
 * song read, guarded callback, controller or output is created by this preview. */
export function ListeningPagePreview({occupant, queue, sidePane = false, width, height, topInset, bottomInset}: {
  occupant: DockOccupant; queue?: QueuePlaybackView; sidePane?: boolean; width: number; height: number; topInset: number; bottomInset: number;
}) {
  const present = useListeningPagePresent(occupant.playback.fence);
  const wide = width >= SIDE_BY_SIDE, side = wide && sidePane;
  const window = queue ? nowPlayingQueueWindow(queue) : undefined;
  const current = queue?.queue?.currentEntryId ? nowPlayingQueueEntry(window, queue.queue.currentEntryId) : undefined;
  const segment = current?.available === true ? window?.segments.find(value => value.id === current.segmentId && value.label)?.label : undefined;
  const book = occupant.mode === 'audiobook', panelWidth = width >= 1024 ? 380 : QUEUE_WIDTH;
  const cover = book ? Math.min(wide ? 250 : 290, width - 48) : wide ? Math.max(160, Math.min(410, width - (side ? panelWidth + 40 : 0) - 56)) : width - 48;
  return <View pointerEvents="none" accessible={false} accessibilityElementsHidden importantForAccessibility="no-hide-descendants" style={{position: 'absolute', inset: 0, opacity: present ? 0 : 1}}>
    <View style={{flex: 1, paddingTop: topInset, paddingBottom: bottomInset, flexDirection: side ? 'row' : 'column'}}>
      <ListeningColumn fence={occupant.playback.fence} source={occupant.presentation.artwork} playingFrom={wide ? segment : undefined} header={!wide ? <View style={{height: 70}}/> : undefined} cover={cover} wide={wide} height={height} horizontal={book && wide && width - (side ? panelWidth + 40 : 0) - 56 >= 560} measureOnly/>
      {side && <View style={{width: panelWidth + 40}}/>}
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


/** The cover is the physical swipe target, leaving queue/lyrics scrolling and
 * the scrubber to their original recognisers. Every target has its own handler. */
function ListeningCoverTarget({gesture, slot, onLayout, size, children}: {gesture?: ReturnType<NonNullable<ReturnType<typeof usePublishedPlayerMorph>>['chromeGesture']>; slot: React.RefObject<View | null>; onLayout: () => void; size: number; children: React.ReactNode}) {
  const view = <View ref={slot} collapsable={false} onLayout={onLayout} style={{width: size, height: size}}>{children}</View>;
  return gesture ? <GestureDetector gesture={gesture}>{view}</GestureDetector> : view;
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
