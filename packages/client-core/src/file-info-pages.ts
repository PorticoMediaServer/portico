import type {DiscMediaFacts} from './disc-media-facts.ts';
import type {TitleFile} from './detail.ts';

/** Display rows retained by a File info panel; the item read has no wire windows. */
export type FileInfoRows<Row> = Readonly<{rows: readonly Row[]; more: boolean; earlier: boolean}>;
export type FileInfoManage = Readonly<{delete: boolean; split: boolean; merge: boolean}>;
/** Facts of the item shown by the panel. */
export type FileInfoSurface = Readonly<{
  fileCount: number; eligibleFileCount: number; groupCount: number;
  canSplit: boolean; hasDiscColumns: boolean; ownerDiscOccurrence?: string; manage?: FileInfoManage;
}>;
export type FileInfoVideo = NonNullable<TitleFile['video']> & Readonly<{profile?: string}>;
/** A byte count: `value` when it is a safe JS integer, otherwise only the server's exact decimal. */
export type FileInfoBytes = Readonly<{value?: number; exact?: string}>;
/** One displayed part. The local occurrence is a row key; id names the whole version. */
export type FileInfoFileRow = Readonly<{
  occurrence: string; itemId: string; id: string; path?: string; missingSinceAt?: string; size: FileInfoBytes; container: string;
  duration?: number; durationUnrepresentable?: true; bitRate?: number; available: boolean; partIndex: number; edition?: string;
  video?: FileInfoVideo; disc?: DiscMediaFacts; audioCount: number; subtitleCount: number;
}>;
/** One version, with its first part supplying the stored track facts. */
export type FileInfoGroupRow = Readonly<{
  id: string; headOccurrence: string; ordinal: number; memberCount: number; edition?: string; container: string;
  size: FileInfoBytes; durationMs?: number; durationUnrepresentable?: true; video?: FileInfoVideo; disc?: DiscMediaFacts;
  audioCount: number; subtitleCount: number;
}>;
export type FileInfoTrackRow = Readonly<{
  ordinal: number; codec: string; language?: string; title?: string; channels?: number; channelLayout?: string; objectAudio?: string;
  bitRate?: number; default?: boolean; forced?: boolean; external?: boolean; profile?: string; sampleRate?: number; bitDepth?: number;
  commentary?: boolean; hearingImpaired?: boolean;
}>;
/** An offered track of a version's head. Its id names the exact accepted head track; it is not a playback grant. */
export type FileInfoHeadTrackRow = FileInfoTrackRow & Readonly<{id: string}>;
export type FileInfoTrackLane = 'audio' | 'subtitles';

export function fileInfoBytesValue(size: FileInfoBytes): number | undefined {
  return size.value ?? (size.exact !== undefined ? Number(size.exact) : undefined);
}
