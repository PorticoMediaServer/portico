/**
 * The guide's data on the new server (`GET /v1/live/channels` and `GET /v1/live/guide`), behind the
 * same `GuideDataSource` the guide screens already read. The new server pages channels by cursor and
 * answers the guide for a stretch of time across a run of channels in the same order; this reads those
 * pages in order and keeps what it has read for the view, so scrolling back reads nothing again.
 */
import type {LiveChannel, LiveProgramme} from '../../../contracts/src/next/api.ts';
import type {GuideChannel} from '../channel-guide.ts';
import {channelSources, readChannels, readGuide, readOnNow, type ChannelChoice} from '../live-tv.ts';
import type {GuideSourceSummaries, GuideSourceSummary} from './directory.ts';
import type {ServerRequester} from '../query/server-api.ts';
import type {ChannelPage, GuideChannelRow, GuideDataSource, GuideProgram} from './types.ts';

const liveRows = new WeakSet<GuideChannelRow>();
export function nextChannelRow(channel: LiveChannel): GuideChannelRow {
  const row: GuideChannelRow = Object.freeze({id: channel.id, kind: channel.kind === 'library' ? 'library' : 'live', number: channel.number, name: channel.name, group: channel.group ?? '',
    ...(channel.logo ? {logoUrl: channel.logo} : {}), favorite: channel.favorite, tuneAvailable: true, recordAvailable: channel.kind !== 'library', guide: 'full', native: channel});
  liveRows.add(row);
  return row;
}
/** The new server's channel behind a row made here; none for a row of the old guide. */
export function liveChannel(row: GuideChannelRow): LiveChannel | undefined {
  return liveRows.has(row) ? row.native as LiveChannel : undefined;
}

/**
 * The rebuilt server names a channel by its id alone. The player's channel identity also carries the source and guide
 * generation the old guide had; a new-server channel gives them fixed values (a Library Channel names itself as its source),
 * the same in every reference and fence.
 * They are never shown.
 */
export const NEXT_CHANNEL_SOURCE = 'server', NEXT_CHANNEL_GENERATION = '1';
/** A new-server row as the channel the player starts from the guide; none for a row of the old guide. */
export function liveGuideChannel(row: GuideChannelRow): GuideChannel | undefined {
  const channel = liveChannel(row);
  if (!channel) return undefined;
  // A Library Channel is its own source (the player's identity allows no other); a live source's channel takes the stand-in.
  const library = channel.kind === 'library';
  return Object.freeze({id: channel.id, sourceId: library ? channel.id : NEXT_CHANNEL_SOURCE, provenance: library ? 'library-channel' : 'live-source', name: channel.name,
    number: channel.number, group: channel.group ?? '', generation: NEXT_CHANNEL_GENERATION, ...(channel.logo ? {logoPath: channel.logo} : {}), programmes: [],
    tuneAvailable: true, recordAvailable: channel.kind !== 'library', tuneUnavailableReason: '', recordUnavailableReason: '', guide: 'full',
    favorite: channel.favorite, hidden: channel.hidden, preferenceRevision: 0}) as GuideChannel;
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

/**
 * On now from the new server (`GET /v1/live/now`): favourites first, then by number, each channel with what is on and what
 * is next. The same data source the On now row already reads; a block of time answers the programmes on now and next that
 * overlap it.
 */
export function nextOnNowSource(api: ServerRequester): GuideDataSource {
  let walk: Walk<[GuideChannelRow, GuideProgram[]]> = {items: [], done: false};
  const read = (signal: AbortSignal) => {
    walk.reading ??= readOnNow(api, walk.cursor, signal).then(page => {
      walk.items.push(...page.items.map(row => [nextChannelRow(row.channel), [row.now, row.next].flatMap(programme => programme ? [nextProgram(row.channel.id, programme)] : [])] as [GuideChannelRow, GuideProgram[]]));
      walk.cursor = page.nextCursor; walk.done = !page.nextCursor;
    }).finally(() => {walk.reading = undefined;});
    return walk.reading;
  };
  return {
    reset() { walk = {items: [], done: false}; },
    async channels(from: number, limit: number, signal: AbortSignal): Promise<ChannelPage> {
      while (walk.items.length < from + limit && !walk.done) await read(signal);
      return {items: walk.items.slice(from, from + limit).map(([row]) => row), total: walk.done ? walk.items.length : walk.items.length + 1};
    },
    async programs(channelIds: readonly string[], start: number, end: number) {
      const byChannel = new Map(walk.items.map(([row, programmes]) => [row.id, programmes]));
      return Object.freeze(Object.fromEntries(channelIds.map(id => [id, (byChannel.get(id) ?? []).filter(programme => programme.end > start && programme.start < end)])));
    },
  };
}

/**
 * One channel by its id, for a link that names it, with what is on it now: read from the guide around it for this minute, or
 * found by paging the guide when the server has no `around`.
 */
export async function findChannel(api: ServerRequester, id: string, now = Date.now(), signal?: AbortSignal): Promise<Readonly<{row: GuideChannelRow; now?: GuideProgram}> | undefined> {
  const window = {from: new Date(now), to: new Date(now + 60_000)};
  let cursor: string | undefined;
  for (let page = 0; page < 40; page++) {
    let answer;
    try { answer = await readGuide(api, window, page === 0 ? {around: id, hidden: true} : {hidden: true}, cursor, signal); }
    // A channel the profile may not see, or one that does not exist, is not found: nothing to show, never an error.
    catch (error) { if ((error as {status?: number} | null)?.status === 404) return undefined; throw error; }
    const match = answer.items.find(row => row.channel.id === id);
    if (match) {
      const on = match.programmes.find(programme => Date.parse(programme.startsAt) <= now && now < Date.parse(programme.endsAt));
      return {row: nextChannelRow(match.channel), ...(on ? {now: nextProgram(id, on)} : {})};
    }
    // A server without `around` answers from the start: page through the guide from there.
    cursor = answer.nextCursor;
    if (!cursor) return undefined;
  }
  return undefined;
}

/**
 * The channels around one, with what is on each now: the player's channel up and down and its mini guide. One read of the
 * guide around the channel (`around=`); a server without it answers from the start, and then only the channel itself is
 * found (by paging, as `findChannel` does).
 */
export async function channelsAround(api: ServerRequester, id: string, now = Date.now(), signal?: AbortSignal): Promise<Readonly<{rows: readonly GuideChannelRow[]; now: ReadonlyMap<string, GuideProgram>}> | undefined> {
  const window = {from: new Date(now), to: new Date(now + 60_000)};
  let answer;
  try { answer = await readGuide(api, window, {around: id}, undefined, signal); }
  catch (error) { if ((error as {status?: number} | null)?.status === 404) return undefined; throw error; }
  const on = (row: {channel: LiveChannel; programmes: readonly LiveProgramme[]}) => row.programmes.find(programme => Date.parse(programme.startsAt) <= now && now < Date.parse(programme.endsAt));
  if (answer.items.some(row => row.channel.id === id)) {
    const rows = answer.items.map(row => nextChannelRow(row.channel));
    return {rows, now: new Map(answer.items.flatMap(row => {const programme = on(row);return programme ? [[row.channel.id, nextProgram(row.channel.id, programme)] as const] : [];}))};
  }
  const found = await findChannel(api, id, now, signal);
  return found ? {rows: [found.row], now: new Map(found.now ? [[id, found.now]] : [])} : undefined;
}

/**
 * The guide's sources and groups to filter by, from the first page of channels, in the shape the Live page's filters read. A
 * server without the follow-up sends none: the page then offers All and Favorites, and the guide's reach is its own default.
 */
export async function nextGuideSummaries(api: ServerRequester, signal?: AbortSignal): Promise<GuideSourceSummaries> {
  return readNextGuideSummaries(api, false, signal);
}
/** Show hidden includes only this profile's hidden channels; the server always excludes
 * channels hidden by the owner, paused sources and sources the viewer may not watch. */
export async function readNextGuideSummaries(api: ServerRequester, includeHidden: boolean, signal?: AbortSignal): Promise<GuideSourceSummaries> {
  const page = await readChannels(api, {hidden: includeHidden}, undefined, signal);
  const sources = channelSources(page).map((source, position): GuideSourceSummary => ({id: source.id, name: source.name, generation: '', publishedAt: '',
    availableStart: '', availableEnd: source.guideThrough ?? '', provenance: source.kind === 'library' ? 'library-channel' : 'live-source',
    refreshState: source.outOfDate ? 'stale' : 'ready', position, recordAvailable: source.kind !== 'library', guideDays: 0, channelCount: source.channels,
    favoriteCount: Math.min(source.favorites, source.channels), groups: source.groups.map(group => group.name), groupCounts: source.groups, unavailableReasons: {}}));
  return Object.freeze({viewerFence: '', revision: '', sources: Object.freeze(sources)});
}
