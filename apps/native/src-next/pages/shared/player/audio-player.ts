import type {RefObject, ReactNode, ComponentProps} from 'react';
import type {View} from 'react-native';
import type {QueuePlaybackView} from '../../../../../../packages/client-core/src/queue-controller';
import type {V1QueueEditIntent} from '../../../../../../packages/client-core/src/playback-v1/v1-queue-player';
import type {PlaybackFence} from '../../../../../../packages/player-core/src';
import type {MessageId} from '../../../../../../packages/i18n/src';
import type {DockCommand, DockOccupant} from '../../../kit/dock/types';
import type {NowPlayingQueueProps} from './useNowPlayingQueue';
import type {MusicSignalView} from '../../../screens/destinations/music/signal-path-model';
import type {AudiobookRead} from '../../../screens/destinations/music/use-audiobook-read';
import type {NativeListeningQueuePolicy} from '../../../shell/listening-queue-policy';
import type {NativePlaybackReadLifetime, NativeSleepChoice, NativeSleepEnd} from '../../../platform/playback/types';
import type {BookChaptersView} from '../../../../../../packages/client-core/src/book-chapters';
import type {NativeListeningSong} from '../../../shell/listening-song-detail';

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
  artwork?: NowPlayingQueueProps['artwork'];
  /** The song's own synced lyrics body (the caller owns its reader); drawn as a
   * tab of the side panel, or a bottom sheet on a narrow window. */
  lyrics?: ReactNode;
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
  playOn?: Readonly<{enabled: boolean; open: (fence: PlaybackFence, anchorRef?: RefObject<View | null>) => void}>;
  /** The guarded queue edit for loaded upcoming rows, bound to the fence this screen drew. */
  queueEdit?: Readonly<{enabled: boolean; lease?: object; apply: (fence: PlaybackFence, intent: V1QueueEditIntent, queueId: string, expectedRevision: string) => void}>;
  /** The current song's issued Detail token and its one favorite ticket. */
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
