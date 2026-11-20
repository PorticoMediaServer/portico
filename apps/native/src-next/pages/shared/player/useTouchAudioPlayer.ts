import {useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import type {View} from 'react-native';
import {Gesture} from 'react-native-gesture-handler';
import {runOnJS, useSharedValue} from 'react-native-reanimated';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {durationText} from '../../../../../../packages/client-core/src/presentation/kit-model/text';
import type {V1QueueEditIntent, V1QueuePolicyIntent} from '../../../../../../packages/client-core/src/playback-v1/v1-queue-player';
import {nativeEqualizer} from '../../../platform/playback/equalizer';
import {registerOverlayKeys} from '../../../platform/overlay-keys';
import type {NativeSleepChoice} from '../../../platform/playback/types';
import {registerBack} from '../../../kit/navigation/back';
import {useTheme} from '../../../kit/theme';
import {GestureCommitScope} from '../../../kit/controls/gesture-commit';
import {useListeningScrubberPublication, useListeningPageActive, useListeningPaintPermit} from '../../../kit/dock/ListeningCoverBridge';
import {commandAllowed, dockCommand, playbackPosition, seekBounds, seekTo} from '../../../kit/dock/model';
import type {DockCommand, DockOccupant} from '../../../kit/dock/types';
import {nowPlayingQueueEntry, nowPlayingQueueWindow, nowPlayingSource} from '../../../screens/destinations/music/NowPlayingQueue';
import {listeningSongFavorite} from '../../../screens/destinations/music/ListeningSongActions';
import {useAudiobookRead} from '../../../screens/destinations/music/use-audiobook-read';
import {currentListeningChapter, locateListeningChapter} from '../../../screens/destinations/music/audiobook-controls';
import type {NowPlayingScreenProps} from './audio-player';

const t = defaultI18n.t;
export const bookSleepChoices: readonly NativeSleepChoice[] = Object.freeze([null, 15, 30, 45, 60, 90, 'chapter', 'book']);
export function sleepLabel(choice: NativeSleepChoice): string {
  return choice === 'item' ? t('player.opt.sleepEndOfTitle') : choice === 'chapter' ? t('player.opt.sleepEndOfChapter') : choice === 'book' ? t('player.opt.sleepEndOfBook')
    : choice === null ? t('player.opt.off') : t('player.opt.minutes', {count: choice});
}
export function useTouchAudioPlayer({occupant, queue, onAction, queuePolicy, policyLifetime, queueEdit, song, reportError = () => {}, lyrics, audiobook, sleepAtMs = null, sleepEnd}: NowPlayingScreenProps) {
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
  const playingFrom = nowPlayingSource(queue);
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
  const wide = theme.form !== 'phone';
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
  const bookRemaining = book && fullBounds ? durationText(Math.max(0, fullBounds.end - position), t) : undefined;
  return {p, playback, book, playOnButton, paintPermit, paintActive, drawnQueue, playingFrom, policyReady, shuffled, repeat,
    queueEditPort, changePolicy, focus, paneOwner, wide, sheet, setSheet, panel, setPanel, moreRef, bookReader, bookSnapshot,
    reportBookError, setBrowsingChapters, chapterBusy, signalRef, closePanel, equalizer, side, position, bookFacts, chapterData,
    wholeBook, byChapter, chapter, chapterTotal, bounds, span, inputActive, seekBook, sleepMinutes, sleepSelected, skipBack,
    skipForward, fraction, act, fenceKey, scrub, hostedScrubber, adjustSeek, favoritePaint, facts, album, bookRemaining};
}
