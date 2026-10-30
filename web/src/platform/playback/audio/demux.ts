import {openAdts} from './adts';
import {openFlac} from './flac';
import {openMp3} from './mp3';
import {openMp4} from './mp4';
import {openOgg} from './ogg';
import {openPcm} from './pcm';
import {DemuxError, type ByteReader, type Demuxer} from './types';

/** The plan's `container` (§18.1) → its demuxer. */
export const demuxers: Readonly<Record<string, (reader: ByteReader) => Promise<Demuxer>>> = {
  mp3: openMp3, adts: openAdts, aac: openAdts, flac: openFlac, ogg: openOgg, opus: openOgg, mp4: openMp4, m4a: openMp4, mov: openMp4, wav: openPcm, aiff: openPcm,
};

export function openDemuxer(container: string, reader: ByteReader): Promise<Demuxer> {
  const open = demuxers[container];
  if (!open) return Promise.reject(new DemuxError(`No demuxer for ${container}.`));
  return open(reader);
}
