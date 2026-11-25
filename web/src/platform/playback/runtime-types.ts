import type {PlaybackService} from '@core/index.ts';
import type {V1QueuePlayer} from '@core/playback-v1/v1-queue-player.ts';
import type {V1Http} from '@core/playback-v1/http.ts';
import type {PlaybackOptionsClient} from '@core/playback-v1/options.ts';
import type {PlayerOffersService} from '@core/player-offers.ts';
import {providerProblem, subtitleSetupReason, type SubtitleCandidate, type SubtitleService, type SubtitleSnapshot} from '@core/subtitles.ts';
import {serviceText} from '@core/presentation/service-text.ts';
import type {SubtitlePreferences} from '@core/preference-effects.ts';
import type {PlaybackOptions, Selector} from '@core/playback-v1/types.ts';
import type {PlaybackSubject} from '@core/presentation/kit-model/index.ts';
import type {GuideChannel} from '@core/channel-guide.ts';
import type {PendingChoiceProgress} from '@core/pending-track-choice.ts';
import type {SubtitleLines} from '@core/subtitle-originals.ts';
import type {DockPlayback, PlaybackFence} from '../../../../packages/player-core/src/index.ts';
import type {WebPlayback} from './web-playback.ts';
import type {WebLocalQueue, WebLocalQueueMember, WebLocalQueuePreparation} from './local-queue.ts';
import type {WebPlaybackCapacityWait} from './capacity-retry.ts';
import type {WebPlaybackStartFailure} from './start-failure.ts';
import type {WebStartRegistration} from './preparing-start.ts';
import type {DrawnSubtitleRetry} from './subtitle-retry.ts';
import type {DrawnSubtitleSurface} from './subtitles/drawn-surface.ts';
import type {SubtitleHost} from './subtitle-surface.ts';
import type {VideoLike} from './web-media-player.ts';
import type {WebPlaybackAuthority, WebPlaybackSource, WebPlaybackStartDisplay, WebPlaybackStarting} from './types.ts';

/** How long a Play may wait for its playback to start before it is a reported failure. */
export const START_DEADLINE_MS = 20_000;
/** A failed options/offers read after the start is tried once more after this long (PB-5). */
export const METADATA_RETRY_MS = 2_000;
/** How often a deadline waiting behind a drawn held failure checks whether it is still drawn. */
export const START_HELD_POLL_MS = 500;
export const startDeadlineError = () => Object.assign(new Error('playback_start_timeout'), {code: 'playback_start_timeout'});

export type GroupTransportCommand = 'play' | 'pause' | 'seek' | 'previous' | 'next';
export type ChannelTarget = Readonly<{channel: GuideChannel}>;
export type LocalQueueLease = {preparation: WebLocalQueuePreparation; release(): Promise<void>; retired: boolean; released: boolean};
export type Bookmark = Readonly<{
  playback: DockPlayback; subject: PlaybackSubject; queueId?: string; libraryId?: string; channel?: ChannelTarget;
  posterUrl?: string; backdropUrl?: string; durationSeconds: number; rate: number;
  local?: Readonly<{lease: LocalQueueLease; index: number; positionMs: number}>;
}>;
export type OfferBinding = {
  readonly api: WebPlaybackSource['api']; readonly authority: WebPlaybackAuthority;
  readonly sessionKey: string; readonly libraryId: string; readonly itemId: string;
  readonly sessionId: string; readonly generation: number; readonly playbackRun: number;
};
export type Starting = {attach(stack: Stack): void; identify(next: WebPlaybackStartDisplay): void; stopWith(stop: () => void): void; accepted(): void; end(): void; display(): WebPlaybackStartDisplay | undefined};
export type StartingState = {key: string; display?: WebPlaybackStartDisplay; accepted: boolean; stack?: Stack; stop: () => void; retry: () => Promise<void>; timer: unknown; view?: WebPlaybackStarting};
export type WebSleepEnd = 'item' | 'chapter' | 'book';
export type WebSubtitleSearch = Readonly<{
  itemId: string; busy: boolean; searched: boolean; candidates: readonly SubtitleCandidate[];
  error?: string; canRetry?: boolean; off?: boolean; setupReason?: string;
}>;

export type Stack = {
  startFailure?: WebPlaybackStartFailure;
  capacityWait?: WebPlaybackCapacityWait;
  capacityCurrent?: () => boolean;
  preplay?: Readonly<{versionId: string; progress: PendingChoiceProgress}>;
  mode: 'server' | 'local'; local?: Readonly<{controller: WebLocalQueue; lease: LocalQueueLease}>;
  capabilityRejected?: boolean;
  web: WebPlayback; service: PlaybackService; queue: V1QueuePlayer; http: V1Http;
  life: AbortController; offers: PlayerOffersService; optionsClient: PlaybackOptionsClient;
  offs: (() => void)[]; unmount?: () => void; subtitles?: SubtitleService; offSubtitles?: () => void;
  subtitleSearch?: WebSubtitleSearch; subtitleSearchFence?: PlaybackFence;
  subtitleKey?: string; documentKey?: string; documentAbort?: AbortController; documentTimer?: unknown;
  /** The overlay this stack draws a styled (ASS) or image (PGS/VobSub/DVB) track on. */
  drawnSurface?: DrawnSubtitleSurface;
  /** A styled track shown as plain lines on the text surface ("Styled subtitles" off). */
  drawnPlain?: boolean;
  /** Tracks that could not be drawn here in this playback run: they are asked for in the picture. */
  undrawable?: Readonly<{run: number; ids: ReadonlySet<string>}>;
  drawnKey?: string;
  drawnAbort?: AbortController;
  drawnRetry?: DrawnSubtitleRetry;
  /** This playback's temporary forced-only choice, gated by the offered lines of one drawn image load. */
  forcedOnly?: boolean;
  forcedLines?: Readonly<{lines: SubtitleLines; key: string; epoch: number}>;
  drawnEpoch?: number;
  drawnFailed?: boolean;
  /** The one track id this runtime told the player it is drawing, and which path wrote it. */
  subtitleRecorded?: Readonly<{id: string | null; path: 'text' | 'drawn'; key: string}>;
  /** A style change made in the options panel, for the one playback run it belongs to. */
  subtitleStyle?: Partial<Pick<SubtitlePreferences, 'size' | 'background'>> & Readonly<{run: number}>;
  /** Belongs to the playback run: the queue moving on to its next track or part keeps it (MU-3). */
  sleepTimer?: Readonly<{minutes: number; endsAt: number; timer: unknown; run: number; token: object}>;
  sessionKey?: string; metadataAbort?: AbortController; options: PlaybackOptions | null;
  /** Reads this session's options and offers again after both tries failed (PB-5). */
  metadataRetry?: () => void;
  offerBinding?: OfferBinding;
  itemId?: string; metadataItemId?: string; libraryId?: string; subject?: PlaybackSubject; showId?: string; seasonId?: string;
  posterUrl?: string; backdropUrl?: string; expanded: boolean; retired: boolean;
  /** Music starts in the dock, decided once for the start's first song. After that, Now playing
   * stays open or closed as the listener left it while the queue moves on (desktop-listen). */
  placed?: true;
  /** The room this stack follows (Watch Together). Each room item is its own stack, so the next item
   * in the same room carries the listener's placement forward instead of docking again. */
  groupRoom?: string;
  channel?: ChannelTarget;
  channelStarts?: WebStartRegistration;
  confirmedEnd?: Readonly<{sessionId: string; generation: number; intentId: number; view: ReturnType<V1QueuePlayer['getSnapshot']>['view']}>;
};

export const emptySubtitle: SubtitleSnapshot = {plan: null, provider: {enabled: true}, busy: false, loading: false, suppressed: true, error: null, canRetry: false, candidates: []};

export function problem(code: string): never { throw new Error(code); }
/** Stop on the starting dock is the viewer's own cancel: never reported as a failure. */
export const startStopped = () => new DOMException('Playback start stopped', 'AbortError');

export function subtitleSearchFailure(itemId: string, error: unknown): WebSubtitleSearch {
  const reason = providerProblem(error), setupReason = subtitleSetupReason((error as {code?: unknown} | null)?.code);
  return Object.freeze({itemId, busy: false, searched: true, candidates: Object.freeze([]),
    ...(setupReason ? {off: true, setupReason}
      : {error: reason?.text ?? serviceText('subtitles.error.failed'), canRetry: reason?.canRetry ?? true})});
}

export const validSeconds = (value: number) => Number.isFinite(value) && value >= 0 && value <= 1e9;
export const wireId = (value: unknown) => typeof value === 'string' && /^[A-Za-z0-9_.:-]{1,128}$/.test(value);

/** A pressed row's list: a bounded id list that holds the row, or a server container. */
export const screenListSelector = (from: Selector, itemId: string): boolean =>
  'items' in from ? from.items.ids.length > 0 && from.items.ids.length <= 500 && from.items.ids.every(wireId) && from.items.ids.includes(itemId)
    : 'container' in from && ['show', 'season', 'album', 'artist', 'disc', 'book', 'playlist', 'collection'].includes(from.container.kind) && wireId(from.container.id);

export const compatibleVideo = (value: VideoLike): value is VideoLike & SubtitleHost =>
  typeof (value as Partial<SubtitleHost>).addTextTrack === 'function' && !!(value as Partial<SubtitleHost>).textTracks;

/** A downloaded title's own stored facts, as the playback subject its dock names. */
export function localMemberSubject(member: WebLocalQueueMember): Exclude<PlaybackSubject, {kind: 'channel'}> {
  const title = member.title, kind = title.kind as 'movie' | 'episode' | 'extra' | 'song' | 'audiobook_file';
  const item = {id: member.itemId, kind, title: title.title, durationSeconds: member.durationMs / 1000,
    ...(title.year > 0 ? {year: title.year} : {}), ...(title.artist ? {artist: title.artist} : {}),
    ...(kind === 'song' && title.parentTitle ? {album: title.parentTitle} : {}),
    ...(kind === 'episode' ? {show: {id: title.parentId, title: title.parentTitle}, episode: {mode: 'seasonal' as const,
      ...(title.groupNumber === null ? {} : {season: title.groupNumber}), ...(title.number === null ? {} : {episode: title.number})}} : {})};
  return {kind: kind === 'song' ? 'music' : kind === 'audiobook_file' ? 'audiobook' : 'video', item};
}

/** The first downloaded title is the one the start loads; a kind this client doesn't know isn't named. */
export function localStartDisplay(member: WebLocalQueueMember | undefined): WebPlaybackStartDisplay | undefined {
  return member && member.title.title && ['movie', 'episode', 'extra', 'song', 'audiobook_file'].includes(member.title.kind) ? {subject: localMemberSubject(member)} : undefined;
}
