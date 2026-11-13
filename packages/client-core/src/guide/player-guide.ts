/** The native player's existing row shape, read from the rebuilt viewer channels and guide. */
import {readChannels, readGuide, type ChannelChoice} from '../live-tv.ts';
import type {ServerRequester} from '../query/server-api.ts';
import {legacyChannelRow, type LegacyGuideOptions} from './legacy-source.ts';
import {liveGuideChannel, nextChannelRow, nextProgram, NEXT_CHANNEL_SOURCE} from './next-source.ts';
import type {GuideChannelRow, GuideProgram} from './types.ts';

export type PlayerGuideRow = Readonly<{channel: GuideChannelRow; step: number; programmes: readonly GuideProgram[]}>;

/** At most two neighbours each side. No lineup scan or legacy source-generation read.
 * A programme window uses the same bounded around read on the guide. */
export async function readPlayerGuide(api: ServerRequester, id: string, options: LegacyGuideOptions, signal?: AbortSignal,
  window?: Readonly<{from: Date; to: Date}>): Promise<readonly PlayerGuideRow[]> {
  const choice: ChannelChoice = {around: id, ...(options.sort === 'name' ? {sort: 'name'} : {}),
    ...(options.favorites ? {favorites: true} : {}), ...(options.includeHidden ? {hidden: true} : {}), ...(options.group ? {group: options.group} : {}),
    ...(options.kind === 'library-channel' ? {source: 'library'} : options.sourceId && options.sourceId !== NEXT_CHANNEL_SOURCE ? {source: options.sourceId} : {})};
  const rows = window ? (await readGuide(api, window, choice, undefined, signal)).items
    : (await readChannels(api, choice, undefined, signal)).items.map(channel => ({channel, programmes: []}));
  const at = rows.findIndex(row => row.channel.id === id);
  if (at < 0) throw new Error('channel_source_unavailable');
  return rows.slice(Math.max(0, at - 2), at + 3).map((row, index) => ({
    channel: legacyChannelRow(liveGuideChannel(nextChannelRow(row.channel))!), step: index + Math.max(0, at - 2) - at,
    programmes: row.programmes.map(programme => nextProgram(row.channel.id, programme)),
  }));
}
