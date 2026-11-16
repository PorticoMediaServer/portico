import {readGuide} from '../live-tv.ts';
import {nextChannelRow, nextProgram} from '../guide/next-source.ts';
import type {GuideChannelRow, GuideProgram} from '../guide/types.ts';
import type {ServerRequester} from './server-api.ts';

export type SearchLiveChannel = Readonly<{row: GuideChannelRow; now?: GuideProgram}>;

/**
 * What is on now on each channel Search found: the guide for this minute around each channel
 * (`GET /v1/live/guide?around=`). One read often answers several found channels, since the guide
 * around one lists its neighbours too; a channel the guide does not answer is left out, and its
 * result keeps its plain channel row.
 */
export async function readSearchLiveChannels(api: ServerRequester, channelIds: readonly string[], now: number, signal?: AbortSignal): Promise<ReadonlyMap<string, SearchLiveChannel>> {
  const found = new Map<string, SearchLiveChannel>(), wanted = new Set(channelIds), window = {from: new Date(now), to: new Date(now + 60_000)};
  for (const id of channelIds) {
    if (found.has(id)) continue;
    const answer = await readGuide(api, window, {around: id}, undefined, signal);
    for (const item of answer.items ?? []) {
      if (!wanted.has(item.channel.id) || found.has(item.channel.id)) continue;
      const on = item.programmes.find(programme => Date.parse(programme.startsAt) <= now && now < Date.parse(programme.endsAt));
      found.set(item.channel.id, Object.freeze({row: nextChannelRow(item.channel), ...(on ? {now: nextProgram(item.channel.id, on)} : {})}));
    }
  }
  return found;
}
