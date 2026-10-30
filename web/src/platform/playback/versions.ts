import type {MediaVersion, PlaybackOptions} from '@core/playback-v1/types.ts';
import type {QueuePlaybackFence} from '../../../../packages/player-core/src/index.ts';

/** Fresh grouped versions in File info order. The current/failed ID comes from the actual
 * playing session, never the planner's preferred ID or a file/source token. */
export type WebPlaybackVersionChoices = Readonly<{
  fence: QueuePlaybackFence;
  options: PlaybackOptions;
  versions: readonly MediaVersion[];
  selectedVersionId: string;
  failedVersionId?: string;
  isCurrent(): boolean;
}>;
export type WebPlaybackVersionChoice = Readonly<{
  choices: WebPlaybackVersionChoices;
  versionId: string;
  signal: AbortSignal;
}>;
