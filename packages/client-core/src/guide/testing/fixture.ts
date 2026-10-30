/**
 * Guide test data (Spec — Channels and Guide §9.4). Not for production.
 *
 * - `demoChannels` / `demoSchedule`: the shapes of the public demo's Live TV fixture
 *   (`portico-internal/deployment/demo/portico-live-fixture.py`): 14 channels in 9 groups, varied program lengths,
 *   episode metadata, a channel with no guide, a channel with 25% gaps and a very long title.
 *   Deterministic per channel and UTC day (a seeded generator, not Python's, so titles and lengths
 *   match the fixture's pools but not its exact sequence).
 * - `syntheticSource`: a `GuideDataSource` of N channels × D days generated on demand, counting
 *   requests, for the 1,000-channel × 14-day scale tests.
 */
import {DAY_MS, MINUTE_MS, floorTo} from '../time.ts';
import type {GuideChannelRow, GuideDataSource, GuideProgram} from '../types.ts';

type Style = 'news' | 'sports' | 'movies' | 'kids' | 'docs' | 'music' | 'local' | 'series' | 'none' | 'gaps';

const CHANNELS: readonly [string, number, string, string, Style][] = [
  ['north.news', 101, 'Northwind News', 'News', 'news'],
  ['north.news2', 102, 'Northwind News 24', 'News', 'news'],
  ['summit.sports', 201, 'Summit Sports', 'Sports', 'sports'],
  ['summit.sports2', 202, 'Summit Sports Extra', 'Sports', 'sports'],
  ['lantern.movies', 301, 'Lantern Movies', 'Movies', 'movies'],
  ['lantern.classics', 302, 'Lantern Classics', 'Movies', 'movies'],
  ['pebble.kids', 401, 'Pebble Kids', 'Kids', 'kids'],
  ['atlas.docs', 501, 'Atlas Documentaries', 'Documentary', 'docs'],
  ['tidal.music', 601, 'Tidal Music TV', 'Music', 'music'],
  ['harbor.local', 701, 'Harbor City Local', 'Local', 'local'],
  ['comet.comedy', 801, 'Comet Comedy', 'Entertainment', 'series'],
  ['ember.drama', 802, 'Ember Drama', 'Entertainment', 'series'],
  ['quiet.noguide', 901, 'Quiet Channel (no guide)', 'Other', 'none'],
  ['patchy.gaps', 902, 'Patchwork TV (guide gaps)', 'Other', 'gaps'],
];

const SERIES: Record<string, readonly [string, number][]> = {
  news: [['Morning Briefing', 60], ['Midday Report', 30], ['World Tonight', 60], ['Market Watch', 30], ['Weather Now', 15], ['Late Edition', 30]],
  sports: [['Summit Football: Harbor City vs. Ridgeview', 150], ['Match Highlights', 30], ['The Locker Room', 60], ['Cycling: Mountain Stage', 180], ['Tennis Open: Quarterfinal', 120]],
  movies: [["The Lighthouse Keeper's Daughter", 118], ['Paper Moons', 96], ['Beneath the Glass Sky', 134], ['A Quiet Harbor', 102], ['The Long Road North', 147], ['Midnight at the Observatory', 109]],
  kids: [['Pebble and Friends', 15], ['Rocket Raccoons', 25], ['Draw Along', 15], ['The Tiny Gardeners', 25], ['Story Time Island', 30]],
  docs: [['Oceans Unseen', 60], ['Cities That Float', 50], ['The Clockmakers', 45], ['Wild Plains', 60], ['How Bridges Stand', 30]],
  music: [['Top 20 Countdown', 60], ['Acoustic Sessions', 30], ['Live from the Harbor Stage', 90], ['Throwback Mix', 60]],
  local: [['Harbor City This Morning', 90], ['Council Live', 120], ['Community Kitchen', 30], ['Local Sports Roundup', 30]],
  series: [['The Night Shift', 44], ['Neighbors & Rivals', 22], ['Station Nine', 44], ['Half Past Seven', 22], ['The Inheritance: A Very Long Title That Should Truncate Gracefully In Every Guide Cell', 44]],
};
const CATEGORIES: Record<string, readonly string[]> = {news: ['News'], sports: ['Sports', 'Live'], movies: ['Movie', 'Drama'], kids: ['Kids', 'Animation'], docs: ['Documentary', 'Nature'], music: ['Music'], local: ['Local', 'Talk'], series: ['Series', 'Comedy']};

/** A small deterministic generator (mulberry32 over an FNV-1a hash of the seed text). */
export function seeded(text: string): () => number {
  let h = 2166136261;
  for (let i = 0; i < text.length; i++) { h ^= text.charCodeAt(i); h = Math.imul(h, 16777619); }
  let a = h >>> 0;
  return () => { a = (a + 0x6d2b79f5) >>> 0; let t = a; t = Math.imul(t ^ (t >>> 15), t | 1); t ^= t + Math.imul(t ^ (t >>> 7), t | 61); return ((t ^ (t >>> 14)) >>> 0) / 4294967296; };
}

export const demoChannels: readonly GuideChannelRow[] = CHANNELS.map(([id, number, name, group, style]) => ({
  id, kind: 'live', number: String(number), name, group, logoUrl: `/fixtures/live/logos/${id}.svg`, favorite: false,
  tuneAvailable: true, recordAvailable: true, guide: style === 'none' ? 'none' : style === 'gaps' ? 'partial' : 'full',
}));

const styleOf = new Map(CHANNELS.map(c => [c[0], c[4]]));

/** One UTC day of a demo channel's schedule. */
export function demoSchedule(channelId: string, dayStart: number): GuideProgram[] {
  const style = styleOf.get(channelId);
  if (!style || style === 'none') return [];
  const rng = seeded(`${channelId}:${new Date(dayStart).toISOString().slice(0, 10)}`);
  const pool = SERIES[style === 'gaps' ? 'series' : style]!;
  const kind = style === 'gaps' ? 'series' : style;
  const out: GuideProgram[] = [];
  const end = dayStart + DAY_MS;
  for (let t = dayStart, n = 0; t < end; n++) {
    const [title, minutes] = pool[Math.floor(rng() * pool.length)]!;
    const slot = style === 'movies' || style === 'sports' ? minutes : Math.max(15, Math.round(minutes / 15) * 15);
    const stop = Math.min(end, t + slot * MINUTE_MS);
    if (style === 'gaps' && rng() < 0.25) { t = stop; continue; }
    const episodic = ['series', 'kids', 'docs', 'local'].includes(kind);
    out.push({
      id: `${channelId}:${t}`, channelId, title, start: t, end: stop, categories: CATEGORIES[kind],
      ...(episodic ? {subtitle: 'The Arrival', episode: {season: 1 + Math.floor(rng() * 6), number: 1 + Math.floor(rng() * 12)}, seriesId: `${channelId}:${title}`} : {}),
      flags: {live: kind === 'sports' && title.includes('vs.'), new: rng() < 0.2},
    });
    t = stop;
  }
  return out;
}

/** Programs of a demo channel overlapping [start, end), whole. */
export function demoPrograms(channelId: string, start: number, end: number): GuideProgram[] {
  const out: GuideProgram[] = [];
  for (let day = floorTo(start, DAY_MS) - DAY_MS; day < end; day += DAY_MS) for (const p of demoSchedule(channelId, day)) if (p.end > start && p.start < end) out.push(p);
  return out;
}

export type SyntheticSource = GuideDataSource & {calls: {channels: number; programs: number; aborted: number}; pending: number};

/**
 * `channels` channels × `days` days from `start`; every 97th channel has no guide, every 13th has
 * gaps. Programs are 15–120 minutes, generated per channel and UTC day on demand (nothing is stored).
 */
export function syntheticSource(o: {channels: number; start: number; days: number; latency?: () => Promise<void>; failPrograms?: (ids: readonly string[], start: number) => boolean}): SyntheticSource {
  const end = o.start + o.days * DAY_MS;
  const calls = {channels: 0, programs: 0, aborted: 0};
  const source: SyntheticSource = {
    calls,
    pending: 0,
    async channels(from, limit, signal) {
      calls.channels++;
      source.pending++;
      try {
        await o.latency?.();
        if (signal.aborted) { calls.aborted++; throw new Error('aborted'); }
        const items: GuideChannelRow[] = [];
        for (let i = from; i < Math.min(o.channels, from + limit); i++) items.push({id: `c${i}`, kind: i % 10 === 9 ? 'library' : 'live', number: String(100 + i), name: `Channel ${i}`, group: ['News', 'Sports', 'Movies', 'Kids'][i % 4]!, favorite: i % 7 === 0, tuneAvailable: true, recordAvailable: i % 10 !== 9, guide: i % 97 === 96 ? 'none' : i % 13 === 12 ? 'partial' : 'full'});
        return {items, total: o.channels};
      } finally { source.pending--; }
    },
    async programs(ids, from, to, signal) {
      calls.programs++;
      source.pending++;
      try {
        await o.latency?.();
        if (signal.aborted) { calls.aborted++; throw new Error('aborted'); }
        if (o.failPrograms?.(ids, from)) throw new Error('unavailable');
        const out: Record<string, GuideProgram[]> = {};
        for (const id of ids) {
          const i = Number(id.slice(1));
          const list: GuideProgram[] = [];
          if (i % 97 !== 96) {
            for (let day = Math.max(o.start, floorTo(from, DAY_MS) - DAY_MS); day < Math.min(end, to); day += DAY_MS) {
              const rng = seeded(`${id}:${day}`);
              for (let t = day; t < day + DAY_MS;) {
                const stop = Math.min(day + DAY_MS, t + (15 + Math.floor(rng() * 8) * 15) * MINUTE_MS);
                if (!(i % 13 === 12 && rng() < 0.25) && stop > from && t < to) list.push({id: `${id}:${t}`, channelId: id, title: `Program ${t / MINUTE_MS}`, start: t, end: stop});
                t = stop;
              }
            }
          }
          out[id] = list;
        }
        return out;
      } finally { source.pending--; }
    },
  };
  return source;
}
