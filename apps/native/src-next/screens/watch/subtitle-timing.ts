import type {PlaybackChoices} from '../../../../../packages/client-core/src/playback-v1/choices';
import type {NativePlaybackRuntimeSnapshot} from '../../platform/playback/types';
import {nativeWatchTracks} from './track-identity';

/** One complete current-plan correction. File offsets are not added again;
 * delivery comes from the current presentation/renderer, never an extension. */
export function nativeWatchSubtitleTiming(source: NativePlaybackRuntimeSnapshot, deltaMs: number): PlaybackChoices | undefined {
  const tracks = nativeWatchTracks(source), local = tracks.local;
  if (local) {
    const correction = local.subtitleOffsetMs + deltaMs;
    if (!local.subtitleTimingSupported || !tracks.subtitle || !Number.isFinite(deltaMs) || !Number.isSafeInteger(correction) || Math.abs(correction) > 600_000) return;
    return {subtitleTrackId: tracks.subtitle.id, subtitleOffsetMs: correction};
  }
  const selected = tracks.resource, plan = source.subtitles?.plan, presentation = source.session?.presentation.subtitle;
  if (!selected || !plan || plan.mode !== 'track' || plan.selected?.id !== selected.id || presentation?.trackId !== selected.id
    || !Number.isFinite(deltaMs)) return;
  const current = Number(plan.offsetUs), correction = current / 1000 + deltaMs;
  if (!Number.isSafeInteger(current) || !Number.isFinite(correction) || Math.abs(correction) > 600_000) return;
  const rich = ['ass', 'pgs', 'vobsub', 'dvb'].includes(presentation.kind);
  const delivery = presentation.delivery === 'burn' || plan.renderer === 'burn_in' ? 'burn'
    : rich && (presentation.delivery === 'device' || plan.renderer === 'client') ? 'embeddedClient' : undefined;
  return {subtitleTrackId: selected.id, subtitleOffsetMs: correction, ...(delivery ? {subtitleDelivery: delivery} : {})};
}
