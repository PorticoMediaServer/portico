import {sameFence} from '../../../../../packages/player-core/src';
import type {AudioStream, PlaybackOptions, SubtitleStream} from '../../../../../packages/client-core/src/playback-v1/types';
import {qualityOptionSelected, type QualityOption} from '../../../../../packages/client-core/src/playback-v1/quality';
import type {NativePlaybackRuntimeSnapshot} from '../../platform/playback/types';
import type {SubtitleResource} from '../../../../../packages/client-core/src/subtitles';
import {nativeWatchTracks} from './track-identity';

export type NativeWatchChoiceName = Readonly<{primary: string; secondary?: string; unavailable?: boolean}>;
export type NativeWatchChoiceRow<T> = NativeWatchChoiceName & Readonly<{id: string; selected: boolean; value: T}>;
export type NativeWatchSubtitleOffer = SubtitleStream | SubtitleResource;
/** Canonical localized technical names and actual availability/route facts.
 * No selected identity, default choice, policy ceiling or synthetic data enters
 * through these naming ports. Native owns the rendered rows. */
export type NativeWatchPanelNames = Readonly<{
  audio: (track: AudioStream, source: NativePlaybackRuntimeSnapshot) => NativeWatchChoiceName;
  subtitle: (track: NativeWatchSubtitleOffer, source: NativePlaybackRuntimeSnapshot) => NativeWatchChoiceName;
  quality: (choice: QualityOption, source: NativePlaybackRuntimeSnapshot) => NativeWatchChoiceName;
  audioFooter: (source: NativePlaybackRuntimeSnapshot) => readonly string[];
  qualityFooter: (source: NativePlaybackRuntimeSnapshot) => readonly string[];
}>;
export function nativeWatchPanelRows(source: NativePlaybackRuntimeSnapshot, options: PlaybackOptions | undefined, names: NativeWatchPanelNames) {
  const fence = source.fence, selected = nativeWatchTracks(source), session = source.session;
  const empty = {audio: [], subtitles: [], qualities: []} as const;
  const unique = <T extends {id: string}>(rows: readonly T[]) => new Set(rows.map(row => row.id)).size === rows.length;
  if (selected.local) {
    const audio: readonly NativeWatchChoiceRow<AudioStream>[] = unique(selected.local.audio) ? selected.local.audio.map(track => ({...names.audio(track, source), id: track.id,
      selected: selected.audio?.id === track.id, value: track})) : [];
    const subtitles: readonly NativeWatchChoiceRow<NativeWatchSubtitleOffer>[] = unique(selected.local.subtitles) ? selected.local.subtitles.map(track => ({...names.subtitle(track, source), id: track.id,
      ...(track.unavailable ? {unavailable: true} : {}), selected: selected.subtitle?.id === track.id, value: track})) : [];
    return {audio, subtitles, qualities: []};
  }
  if (!selected.version || !fence || fence.kind !== 'queue' || !session || !options || !sameFence(source.dock.local?.fence, fence)
    || options.itemId !== fence.itemId || new Set(options.versions.map(row => row.id)).size !== options.versions.length) return empty;
  const version = options.versions.find(row => row.id === session.versionId);
  if (!version) return empty;
  // Names use this actual fresh offer; selected identities remain the session's.
  const offeredSource = {...source, options};
  const audio: readonly NativeWatchChoiceRow<AudioStream>[] = unique(version.audio) ? version.audio.map(track => ({...names.audio(track, offeredSource), id: track.id,
    selected: selected.audio?.id === track.id, value: track})) : [];
  const plan = source.subtitles?.plan;
  const resources = !source.subtitles?.suppressed && plan?.sessionId === fence.sessionId && plan.generation === fence.generation && plan.sourceId === fence.sourceId
    ? plan.resources.filter(resource => resource.sourceId === fence.sourceId) : [];
  const offered: readonly NativeWatchSubtitleOffer[] = unique(version.subtitles) && unique(resources)
    ? [...version.subtitles.map(track => resources.find(resource => resource.id === track.id) ?? track), ...resources.filter(resource => !version.subtitles.some(track => track.id === resource.id))] : [];
  const subtitles: readonly NativeWatchChoiceRow<NativeWatchSubtitleOffer>[] = offered.map(track => ({...names.subtitle(track, offeredSource), id: track.id,
    ...('enabled' in track && (!track.enabled || track.retired) ? {unavailable: true} : {}), selected: session.presentation.subtitle?.trackId === track.id, value: track}));
  const qualityIds = options.qualities.map(row => row.mode === 'fixed' ? row.id : row.mode);
  const qualities: readonly NativeWatchChoiceRow<QualityOption>[] = new Set(qualityIds).size === qualityIds.length ? options.qualities.map(choice => ({...names.quality(choice, source),
    id: choice.mode === 'fixed' ? choice.id! : choice.mode, selected: qualityOptionSelected(choice, session.presentation.quality), value: choice})) : [];
  return {audio, subtitles, qualities};
}
