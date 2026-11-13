/** The native guide's existing row models, backed by the rebuilt viewer reads. */
import {throwIfAborted} from '../abort-lifetime.ts';
import type {LiveProgramme} from '../../../contracts/src/next/api.ts';
import type {GuideProgramme} from '../channel-guide.ts';
import {readGuide, readProgramme, type ChannelChoice} from '../live-tv.ts';
import type {ServerRequester} from '../query/server-api.ts';
import {unreadableServerResponse} from '../server-messages.ts';
import {legacyChannelRow, legacyProgram, type LegacyGuideOptions} from './legacy-source.ts';
import {liveGuideChannel, nextChannelRow, nextGuideSource, NEXT_CHANNEL_SOURCE, NEXT_CHANNEL_GENERATION} from './next-source.ts';
import type {GuideAiring} from './directory.ts';
import {nextRecording} from '../next-dvr.ts';
import type {GuideDataSource, GuideProgram} from './types.ts';

/** Missing provider series/episode flags stay unknown, rather than offering made-up new-episode choices. */
export function nativeGuideProgramme(channelId: string, programme: LiveProgramme): GuideProgramme {
  return {id: programme.id, channelId, title: programme.title ?? '', start: programme.startsAt, end: programme.endsAt,
    lineage: 'interval-only', seriesId: '', episodeId: '', newEvidence: 'unknown', description: programme.summary ?? '',
    ...(programme.episodeTitle ? {subtitle: programme.episodeTitle} : {}),
    ...(programme.season !== undefined || programme.episode !== undefined ? {episode: {season: programme.season, number: programme.episode}} : {}),
    ...(programme.rating ? {rating: {system: '', value: programme.rating}} : {})};
}

export function nativeGuideSource(api: ServerRequester, options: LegacyGuideOptions): GuideDataSource {
  const choice: ChannelChoice = {...(options.sort === 'name' ? {sort: 'name'} : {}), ...(options.favorites ? {favorites: true} : {}),
    ...(options.group ? {group: options.group} : {}), ...(options.includeHidden || options.includeServerHidden ? {hidden: true} : {}),
    ...(options.kind === 'library-channel' ? {source: 'library'} : options.sourceId && options.sourceId !== NEXT_CHANNEL_SOURCE ? {source: options.sourceId} : {})};
  const source = nextGuideSource(api, choice);
  return {
    reset() {source.reset?.();},
    async channels(from, limit, signal) {
      const page = await source.channels(from, limit, signal);
      throwIfAborted(signal);
      return {...page, items: page.items.map(row => legacyChannelRow(liveGuideChannel(row)!))};
    },
    async programs(channelIds, start, end, signal) {
      const pending = new Set(channelIds), programmes: Record<string, readonly GuideProgram[]> = {};
      // The around read covers up to eleven channels. Ask only around still-needed visible rows;
      // never page through the lineup to reach a programme on a distant channel.
      while (pending.size) {
        throwIfAborted(signal);
        const batch = [...pending].slice(0, 11), anchor = batch[Math.floor(batch.length / 2)];
        const page = await readGuide(api, {from: new Date(start), to: new Date(end)}, {...choice, around: anchor}, undefined, signal);
        throwIfAborted(signal);
        if (!page.items.some(row => row.channel.id === anchor)) throw new Error(unreadableServerResponse);
        for (const row of page.items) if (pending.delete(row.channel.id)) {
          programmes[row.channel.id] = row.programmes.map(programme => legacyProgram(nativeGuideProgramme(row.channel.id, programme)));
        }
      }
      return Object.freeze(programmes);
    },
  };
}

/** A selected programme and its server-authored other airings; its id, not its title, selects it. */
export async function readNativeProgramme(api: ServerRequester, id: string, signal?: AbortSignal) {
  const detail = await readProgramme(api, id, signal);
  throwIfAborted(signal);
  const programme: GuideProgramme = {...nativeGuideProgramme(detail.channel.id, detail),
    ...(detail.recording ? {recordingId: detail.recording.id, recordingState: nextRecording(detail.recording).state} : {})};
  const channel = {...liveGuideChannel(nextChannelRow(detail.channel))!, programmes: [programme],
    recordAvailable: detail.canRecord && detail.channel.kind !== 'library'};
  const airings: GuideAiring[] = detail.airings.map(airing => ({
    channel: {id: airing.channelId, name: airing.channel, number: airing.channelId === channel.id ? channel.number : '', group: '',
      provenance: channel.provenance, sourceId: channel.provenance === 'library-channel' ? airing.channelId : NEXT_CHANNEL_SOURCE, generation: NEXT_CHANNEL_GENERATION},
    programme: nativeGuideProgramme(airing.channelId, {...airing, title: detail.title}),
  }));
  return {channel, programme, airings};
}
