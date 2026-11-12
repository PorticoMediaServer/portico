/**
 * The guide's data on the new server (`GET /v1/live/channels` and `GET /v1/live/guide`), behind the
 * same `GuideDataSource` the guide screens already read. The new server pages channels by cursor and
 * answers the guide for a stretch of time across a run of channels in the same order; this reads those
 * pages in order and keeps what it has read for the view, so scrolling back reads nothing again.
 */
import type {LiveChannel, LiveProgramme} from '../../../contracts/src/next/api.ts';
import {readChannels, readGuide, type ChannelChoice} from '../live-tv.ts';
import type {ServerRequester} from '../query/server-api.ts';
import type {ChannelPage, GuideChannelRow, GuideDataSource, GuideProgram} from './types.ts';

export function nextChannelRow(channel: LiveChannel): GuideChannelRow {
  return Object.freeze({id: channel.id, kind: channel.kind === 'library' ? 'library' : 'live', number: channel.number, name: channel.name, group: channel.group ?? '',
    ...(channel.logo ? {logoUrl: channel.logo} : {}), favorite: channel.favorite, tuneAvailable: true, recordAvailable: channel.kind !== 'library', guide: 'full', native: channel});
}
export function nextProgram(channelId: string, programme: LiveProgramme): GuideProgram {
  return Object.freeze({id: programme.id, channelId, title: programme.title ?? '', start: Date.parse(programme.startsAt), end: Date.parse(programme.endsAt),
    ...(programme.episodeTitle ? {subtitle: programme.episodeTitle} : {}),
    ...(programme.season !== undefined || programme.episode !== undefined ? {episode: {season: programme.season, number: programme.episode}} : {}),
    ...(programme.rating ? {rating: programme.rating} : {}), ...(programme.summary ? {description: programme.summary} : {}), native: programme});
}

type Walk<T> = {items: T[]; cursor?: string; done: boolean; reading?: Promise<void>};
/** The most guide pages read for one stretch of time before the rest is left to a later ask. */
const GUIDE_PAGES = 40;

export function nextGuideSource(api: ServerRequester, choice: ChannelChoice = {}): GuideDataSource {
  let channels: Walk<GuideChannelRow> = {items: [], done: false};
  let windows = new Map<string, Walk<[string, GuideProgram[]]> & {seen: Set<string>}>();
  const more = <T,>(walk: Walk<T>, read: (cursor: string | undefined) => Promise<{items: T[]; nextCursor?: string}>) => {
    walk.reading ??= read(walk.cursor).then(page => {walk.items.push(...page.items); walk.cursor = page.nextCursor; walk.done = !page.nextCursor;})
      .finally(() => {walk.reading = undefined;});
    return walk.reading;
  };
  return {
    reset() { channels = {items: [], done: false}; windows = new Map(); },
    async channels(from: number, limit: number, signal: AbortSignal): Promise<ChannelPage> {
      const walk = channels;
      while (walk.items.length < from + limit && !walk.done) {
        await more(walk, cursor => readChannels(api, choice, cursor, signal).then(page => ({items: page.items.map(nextChannelRow), nextCursor: page.nextCursor})));
      }
      // Past the last page the total is known; before it, one more row says there is more to scroll to.
      return {items: walk.items.slice(from, from + limit), total: walk.done ? walk.items.length : walk.items.length + 1};
    },
    async programs(channelIds: readonly string[], start: number, end: number, signal: AbortSignal) {
      const key = `${start}:${end}`;
      let walk = windows.get(key);
      if (!walk) { walk = {items: [], done: false, seen: new Set()}; windows.set(key, walk); }
      const window = walk;
      for (let pages = 0; channelIds.some(id => !window.seen.has(id)) && !window.done && pages < GUIDE_PAGES; pages++) {
        await more(window, cursor => readGuide(api, {from: new Date(start), to: new Date(end)}, choice, cursor, signal).then(page => {
          const rows = page.items.map(row => [row.channel.id, row.programmes.map(programme => nextProgram(row.channel.id, programme))] as [string, GuideProgram[]]);
          for (const [id] of rows) window.seen.add(id);
          return {items: rows, nextCursor: page.nextCursor};
        }));
      }
      const byChannel = new Map(window.items);
      return Object.freeze(Object.fromEntries(channelIds.map(id => [id, byChannel.get(id) ?? []])));
    },
  };
}
