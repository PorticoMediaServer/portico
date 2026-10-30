import {dimensionsPart, fileDisplayResolutionPart, bitRateText, bytesText, codecText, hdrText} from './technical.ts';
import {audioTrackModels, subtitleTrackModels, fileInfoTrackModels, versionModels, type TrackFacts, type TrackModel, type VersionInput, type VersionModel} from './tracks.ts';
import {compact, durationText, part, positive, supplied, type FactPart, type TextEnvironment} from './text.ts';

export type TechnicalRow = Readonly<{id: string; label: string; parts: readonly FactPart[]}>;
export type FileFacts = VersionInput & Readonly<{
  available: boolean; durationSeconds?: number; width?: number; frameRate?: number; bitDepth?: number;
  /** Genuine source codec profile only. Never inferred from codec, bit depth, or quality. */
  videoProfile?: string;
  audio: readonly TrackFacts[]; subtitles: readonly TrackFacts[];
}>;
export type FileInfoContext = 'desktop' | 'phone';
export type FileInfoDisplay = Readonly<{
  heading: FactPart; summary: readonly FactPart[]; video: readonly FactPart[];
  audio: readonly Readonly<{id: string; parts: readonly FactPart[]}>[];
  subtitles: readonly FactPart[];
}>;
export type OwnerFileFacts = Readonly<{
  /** Already authorized facts, not a request for a formatter to decide permissions. */
  /** One row per file. `versionId` names its version when a version has several files. */
  locations?: readonly Readonly<{id: string; versionId?: string; path?: string; canDelete?: boolean}>[];
  canSplit?: boolean; canMerge?: boolean;
}>;
export type FileInfoAccess = Readonly<{audience: 'viewer'}> | Readonly<{audience: 'owner'; ownerFacts?: OwnerFileFacts; manageSurface?: 'desktop' | 'phone'}>;
export type ManageAction = Readonly<{id: 'delete-version' | 'split-versions' | 'merge-title'; label: string}>;
export type FileInfoModel = Readonly<{
  heading: string;
  files: readonly Readonly<{id: string; available: boolean; version: VersionModel; rows: readonly TechnicalRow[]; audio: readonly TrackModel[]; subtitles: readonly TrackModel[]; display: FileInfoDisplay}>[];
  owner?: Readonly<{heading: string; locations: readonly Readonly<{id: string; versionId: string; path?: string; actions: readonly ManageAction[]}>[]; actions: readonly ManageAction[]}>;
}>;

/** A File info panel model only. It has no page placement, picker, or playback commands. */
export function fileInfoModel(files: readonly FileFacts[], access: FileInfoAccess, env: TextEnvironment, context: FileInfoContext = 'desktop'): FileInfoModel {
  const {t} = env, versions = versionModels(files, env);
  const output = files.map((file, i) => {
    const rows: TechnicalRow[] = [];
    const add = (id: string, label: string, parts: readonly (FactPart | undefined)[]) => { const present = compact(parts); if (present.length) rows.push(Object.freeze({id, label, parts: present})); };
    const size = bytesText(file.sizeBytes, t), duration = durationText(file.durationSeconds, t), rate = bitRateText(file.bitRate, t);
    const codec = codecText(file.videoCodec, t), range = hdrText(file.dynamicRange, file.hdr10Plus, t);
    add('resolution', t('next.kit.resolution'), [dimensionsPart(file.width, file.height, t)]);
    add('video', t('next.kit.video'), [codec ? part('codec', codec) : undefined, range ? part('dynamic-range', range) : undefined, positive(file.bitDepth) ? part('bit-depth', t('next.kit.bitDepth', {bits: file.bitDepth})) : undefined]);
    add('container', t('next.kit.container'), [supplied(file.container) ? part('container', file.container.toUpperCase()) : undefined]);
    add('size', t('next.kit.size'), [size ? part('size', size) : undefined]);
    add('length', t('next.kit.length'), [duration ? part('duration', duration) : undefined]);
    add('bitrate', t('next.kit.bitrate'), [rate ? part('bitrate', rate) : undefined]);
    add('frame-rate', t('next.kit.frameRate'), [positive(file.frameRate) ? part('frame-rate', t('next.kit.fps', {rate: Number.isInteger(file.frameRate) ? String(file.frameRate) : file.frameRate.toFixed(3)})) : undefined]);
    return Object.freeze({id: file.id, available: file.available, version: versions[i]!, rows: Object.freeze(rows), audio: audioTrackModels(file.audio, env), subtitles: subtitleTrackModels(file.subtitles, env), display: fileDisplay(file, i, context, env)});
  });
  // Whitelist fields: spreading a source file or access object would leak owner-only facts.
  if (access.audience !== 'owner' || !access.ownerFacts) return Object.freeze({heading: t('next.kit.fileInfo'), files: Object.freeze(output)});
  const authorized = access.ownerFacts, known = new Set(files.map(f => f.id)), manage = !!access.manageSurface;
  const locations = (authorized.locations ?? []).filter(x => known.has(x.versionId ?? x.id)).map(x => Object.freeze({id: x.id, versionId: x.versionId ?? x.id, ...(supplied(x.path) ? {path: x.path} : {}), actions: Object.freeze(manage && x.canDelete ? [{id: 'delete-version', label: t('next.kit.deleteVersion')} as const] : [])}));
  const actions: ManageAction[] = [];
  if (manage && authorized.canSplit) actions.push({id: 'split-versions', label: t('next.kit.splitVersions')});
  if (manage && authorized.canMerge) actions.push({id: 'merge-title', label: t('next.kit.mergeTitle')});
  return Object.freeze({heading: t('next.kit.fileInfo'), files: Object.freeze(output), owner: Object.freeze({heading: t('next.kit.locationManage'), locations: Object.freeze(locations), actions: Object.freeze(actions)})});
}

/** All context ordering and list punctuation is canonical; render these arrays whole. */
function fileDisplay(file: FileFacts, index: number, context: FileInfoContext, env: TextEnvironment): FileInfoDisplay {
  const {t} = env;
  const separated = (groups: readonly (readonly FactPart[])[], separator: 'comma' | 'space'): readonly FactPart[] => Object.freeze(groups.filter(g => g.length).flatMap((group, i) => i ? [part(`separator-${separator}-${i}`, t(separator === 'comma' ? 'next.kit.fileInfo.comma' : 'next.kit.fileInfo.space'), 'context'), ...group] : [...group]));
  const codec = codecText(file.videoCodec, t), range = hdrText(file.dynamicRange, file.hdr10Plus, t), rate = bitRateText(file.bitRate, t);
  const codecParts = compact([codec ? part('codec', codec) : undefined, codec && supplied(file.videoProfile) ? part('codec-profile', file.videoProfile) : undefined]);
  const hdrParts = compact([range ? part('dynamic-range', range) : undefined]), rateParts = compact([rate ? part('bitrate', rate) : undefined]);
  const video = context === 'phone' ? separated([separated(codecParts.map(p => [p]), 'space'), hdrParts, rateParts], 'comma')
    : compact([...codecParts, ...hdrParts, positive(file.bitDepth) ? part('bit-depth', t('next.kit.bitDepth', {bits: file.bitDepth})) : undefined, ...rateParts, supplied(file.container) ? part('container', file.container.toUpperCase()) : undefined]);
  const audio = fileInfoTrackModels(file.audio, 'audio', env).map(track => {
    const codec = track.technical.filter(p => p.id === 'codec'), layout = track.detail.filter(p => p.id === 'layout');
    const detail = track.detail.filter(p => p.id !== 'layout'), object = track.technical.filter(p => p.id === 'object-audio');
    const parts = context === 'phone' ? separated([[track.name], separated([...codec, ...layout, ...object, ...detail].map(p => [p]), 'space')], 'comma') : Object.freeze([track.name, ...codec, ...layout, ...object, ...detail]);
    return Object.freeze({id: track.id, parts});
  });
  const subtitles = separated(fileInfoTrackModels(file.subtitles, 'subtitle', env).map(track => separated([[track.name], ...track.detail.map(p => [p])], 'space').map(p => part(`subtitle:${track.id}:${p.id}`, p.text, p.role))), 'comma');
  return Object.freeze({heading: part('version', t('next.kit.version', {number: file.ordinal ?? index + 1}), 'identity'), summary: compact([fileDisplayResolutionPart(file.width, file.height, t), ...hdrParts]), video, audio: Object.freeze(audio), subtitles});
}
