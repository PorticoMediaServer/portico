import {fileInfoBytesValue, type FileInfoFileRow, type FileInfoGroupRow, type FileInfoHeadTrackRow, type FileInfoTrackRow} from '../file-info-pages.ts';
import {fileInfoModel, type FileFacts, type FileInfoModel, type TrackFacts} from './kit-model/index.ts';
import type {TextEnvironment} from './kit-model/text.ts';
import {fileInfoTrackKey, type FileInfoSnapshot} from '../file-info-session.ts';

/** One version's facts for fileInfoModel, from its group row and whatever windows of its tracks
 * are resident (web: the head file's raw tracks; native: the head's offered tracks). Ordinals
 * come from the server, so a windowed list keeps its numbering; tracks not yet read are simply
 * absent, never guessed from a count. An unrepresentable length is left out, never zero. */
export function fileInfoVersionFacts(group: FileInfoGroupRow, audio: readonly (FileInfoTrackRow | FileInfoHeadTrackRow)[] = [], subtitles: readonly (FileInfoTrackRow | FileInfoHeadTrackRow)[] = []): FileFacts {
  const video = group.video, seconds = group.durationMs !== undefined && group.durationMs > 0 ? group.durationMs / 1000 : undefined, bytes = fileInfoBytesValue(group.size);
  const track = (lane: 'audio' | 'subtitle') => (row: FileInfoTrackRow | FileInfoHeadTrackRow): TrackFacts => ({
    id: 'id' in row ? row.id : `${group.headOccurrence}:${lane}:${row.ordinal}`, ordinal: row.ordinal, codec: row.codec, language: row.language, title: row.title,
    channels: row.channels, channelLayout: row.channelLayout, forced: row.forced, default: row.default, external: row.external,
    commentary: row.commentary, hearingImpaired: row.hearingImpaired,
    objectAudio: lane === 'audio' && (row.objectAudio === 'atmos' || row.objectAudio === 'dtsx') ? row.objectAudio : undefined,
  });
  return {
    id: group.id, ordinal: group.ordinal, available: true, edition: group.edition, container: group.container, sizeBytes: bytes,
    durationSeconds: seconds,
    // A whole-version rate from the version's own totals, as the file rate always was: size over length.
    bitRate: bytes !== undefined && bytes > 0 && seconds ? Math.round(bytes * 8 / seconds) : undefined,
    videoCodec: video?.codec, videoProfile: video?.profile, width: video?.width, height: video?.height, dynamicRange: video?.dynamicRange, hdr10Plus: video?.hdr10Plus,
    frameRate: video?.frameRate, bitDepth: video?.bitDepth,
    audio: audio.map(track('audio')), subtitles: subtitles.map(track('subtitle')),
  };
}

/** One file of a flat-file surface (an album's track, a book's part), from its row and resident raw
 * tracks. A flat file has no server ordinal; its place in the resident list numbers it. */
export function fileInfoFileFacts(file: FileInfoFileRow, audio: readonly FileInfoTrackRow[] = [], subtitles: readonly FileInfoTrackRow[] = []): FileFacts {
  const video = file.video, bytes = fileInfoBytesValue(file.size);
  const track = (lane: 'audio' | 'subtitle') => (row: FileInfoTrackRow): TrackFacts => ({
    id: `${file.occurrence}:${lane}:${row.ordinal}`, ordinal: row.ordinal, codec: row.codec, language: row.language, title: row.title,
    channels: row.channels, channelLayout: row.channelLayout, forced: row.forced, default: row.default, external: row.external,
    commentary: row.commentary, hearingImpaired: row.hearingImpaired,
    objectAudio: lane === 'audio' && (row.objectAudio === 'atmos' || row.objectAudio === 'dtsx') ? row.objectAudio : undefined,
  });
  return {
    id: file.occurrence, available: file.available, edition: file.edition, container: file.container, sizeBytes: bytes,
    durationSeconds: file.duration && file.duration > 0 ? file.duration : undefined, bitRate: file.bitRate,
    videoCodec: video?.codec, videoProfile: video?.profile, width: video?.width, height: video?.height, dynamicRange: video?.dynamicRange, hdr10Plus: video?.hdr10Plus,
    frameRate: video?.frameRate, bitDepth: video?.bitDepth,
    audio: audio.map(track('audio')), subtitles: subtitles.map(track('subtitle')),
  };
}

export type FileInfoPanelPolicy = Readonly<{
  /** Web lists a version head's raw stored tracks; native lists the head's offered tracks. */
  tracks: 'raw' | 'offered';
  context: 'desktop' | 'phone';
  manageSurface: 'desktop' | 'phone';
  /** The caller's own gates on top of the server's: a kind that can be split, a surface that merges. */
  split: boolean; merge: boolean;
}>;
/** The File info panel's model from one opening's resident windows: versions in the server's order
 * and numbering, each with its resident tracks, and for an owner the listed files of each version.
 * Split is the server's global answer (`surface.canSplit`), never a count of resident rows. */
export function fileInfoPanelModel(snapshot: FileInfoSnapshot, policy: FileInfoPanelPolicy, env: TextEnvironment): FileInfoModel | undefined {
  const groups = snapshot.groups.rows?.rows, files = snapshot.files?.rows?.rows, surface = snapshot.surface;
  if (files && surface) {
    // A flat-file surface lists each file with its own raw tracks; its owner sees each file's path.
    const raw = (file: FileInfoFileRow, lane: 'audio' | 'subtitles') => snapshot.tracks.get(fileInfoTrackKey(file.occurrence, lane))?.rows?.rows;
    const manage = surface.manage;
    return fileInfoModel(files.map(file => fileInfoFileFacts(file, raw(file, 'audio'), raw(file, 'subtitles'))), manage ? {audience: 'owner', manageSurface: policy.manageSurface, ownerFacts: {
      locations: files.map(file => ({id: file.occurrence, path: file.path, canDelete: manage.delete})),
      canSplit: manage.split && surface.canSplit && policy.split, canMerge: manage.merge && policy.merge,
    }} : {audience: 'viewer'}, env, policy.context);
  }
  if (!groups || !surface) return undefined;
  const tracks = (group: FileInfoGroupRow, lane: 'audio' | 'subtitles') => policy.tracks === 'raw'
    ? snapshot.tracks.get(fileInfoTrackKey(group.headOccurrence, lane))?.rows?.rows : snapshot.headTracks.get(fileInfoTrackKey(group.id, lane))?.rows?.rows;
  const manage = surface.manage;
  return fileInfoModel(groups.map(group => fileInfoVersionFacts(group, tracks(group, 'audio'), tracks(group, 'subtitles'))), manage ? {audience: 'owner', manageSurface: policy.manageSurface, ownerFacts: {
    locations: groups.flatMap(group => (snapshot.members.get(group.id)?.rows?.rows ?? []).map(file => ({id: file.occurrence, versionId: group.id, path: file.path, canDelete: manage.delete}))),
    canSplit: manage.split && surface.canSplit && policy.split, canMerge: manage.merge && policy.merge,
  }} : {audience: 'viewer'}, env, policy.context);
}
