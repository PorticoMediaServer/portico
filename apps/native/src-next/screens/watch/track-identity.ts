import {sameFence} from '../../../../../packages/player-core/src';
import type {NativePlaybackRuntimeSnapshot} from '../../platform/playback/types';
import type {AudioStream, MediaVersion, SubtitleStream} from '../../../../../packages/client-core/src/playback-v1/types';
import type {SubtitleResource} from '../../../../../packages/client-core/src/subtitles';

export type NativeWatchTracks = Readonly<{
  version?: MediaVersion;
  local?: NativePlaybackRuntimeSnapshot['localTracks'];
  audio?: AudioStream;
  subtitle?: SubtitleStream;
  resource?: SubtitleResource;
  /** Absence is known Off only for a current session, never a missing producer. */
  subtitlesOff: boolean;
}>;
export type NativeWatchTrackSource = Pick<NativePlaybackRuntimeSnapshot, 'fence' | 'session' | 'options' | 'retired' | 'subtitles' | 'audioTrackSelection' | 'localTracks'> & Readonly<{
  playback?: Pick<NativePlaybackRuntimeSnapshot['playback'], 'intentId'>;
  dock: Readonly<{local: Readonly<{fence: NonNullable<NativePlaybackRuntimeSnapshot['fence']>}> | null}>;
}>;

/** Exact current session identities only. Defaults, first rows and plan previews are not selections. */
export function nativeWatchTracks(source: NativeWatchTrackSource): NativeWatchTracks {
  const {fence, session, options} = source;
  if (fence?.kind === 'local') {
    const local = source.localTracks;
    if (source.retired || !local || !sameFence(source.dock.local?.fence, fence)
      || local.binding.sessionId !== fence.sessionId || local.binding.generation !== fence.generation
      || local.binding.intentId !== source.playback?.intentId) return Object.freeze({subtitlesOff: false});
    const audio = local.audio.filter(row => row.id === local.audioTrackId);
    const subtitle = local.subtitles.filter(row => row.id === local.subtitleTrackId && !row.unavailable);
    return Object.freeze({local, audio: audio.length === 1 ? audio[0] : undefined, subtitle: subtitle.length === 1 ? subtitle[0] : undefined,
      subtitlesOff: local.subtitleTrackId === null});
  }
  if (source.retired || !fence || fence.kind !== 'queue' || !session || !options
    || session.id !== fence.sessionId || session.presentation.generation !== fence.generation
    || session.itemId !== fence.itemId || options.itemId !== fence.itemId || session.versionId !== fence.sourceId
    || session.queue?.entryId !== fence.entryId || !source.dock.local || !sameFence(source.dock.local.fence, fence)) return Object.freeze({subtitlesOff: false});
  const versions = options.versions.filter(row => row.id === session.versionId);
  if (versions.length !== 1) return Object.freeze({subtitlesOff: false});
  const version = versions[0]!;
  const applied = source.audioTrackSelection;
  const audio = version.audio.filter(row => row.id === session.presentation.audioTrackId && (!applied
    || applied.state === 'applied' && applied.command.binding.sessionId === fence.sessionId && applied.command.binding.generation === fence.generation
      && row.id === `a${applied.command.streamIndex}`));
  const subtitle = version.subtitles.filter(row => row.id === session.presentation.subtitle?.trackId);
  const plan = source.subtitles?.plan, selected = plan?.selected;
  const resource = !source.subtitles?.suppressed && plan?.mode === 'track' && selected && selected.id === session.presentation.subtitle?.trackId
    && plan.sessionId === fence.sessionId && plan.generation === fence.generation && plan.sourceId === fence.sourceId
    && selected.sourceId === fence.sourceId && selected.enabled && plan.resources.some(row => row.id === selected.id && row.revision === selected.revision) ? selected : undefined;
  return Object.freeze({version, audio: audio.length === 1 ? audio[0] : undefined, subtitle: subtitle.length === 1 ? subtitle[0] : undefined,
    resource, subtitlesOff: !session.presentation.subtitle});
}
