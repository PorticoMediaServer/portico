import {episodeLabel, type EpisodeNumbering} from './facts.ts';
import {compact, durationText, part, supplied, type FactPart, type TextEnvironment} from './text.ts';

export type ChannelIdentity = Readonly<{sourceId: string; id: string}>;
export type ChannelMark = Readonly<{text: string; seed: string}>;
export type MarkedChannel = ChannelIdentity & Readonly<{mark: ChannelMark}>;
export type LineupChannel = ChannelIdentity & Readonly<{name: string; number?: string}>;
/**
 * Authoritative marks can be a bounded page: the server established their full collision domain.
 * `complete-authorized` certifies ALL channels visible to this viewer in each included source,
 * before group/search/favorite/page filters. A paged guide response cannot make this assertion.
 * Home's {channel,source} rows map to {sourceId:channel.sourceId,id:channel.id,mark:channel.mark}.
 */
export type ChannelMarkDomain =
  | Readonly<{kind: 'authoritative'; channels: readonly MarkedChannel[]}>
  | Readonly<{kind: 'complete-authorized'; channels: readonly LineupChannel[]}>;
export type ChannelMarkModel = ChannelIdentity & ChannelMark;
export type ChannelMarkIndex = ReadonlyMap<string, ReadonlyMap<string, ChannelMarkModel>>;
const channelTints = ['#28587a', '#985243', '#796120', '#28665d', '#854d73', '#376f8c', '#4a596a', '#685294'] as const;
/** §33: exact authoritative seed, UTF-16 code units and signed 32-bit wrap.
 * The fixed profile-art palette order is shared by every client. */
export function channelTint(seed: string): string {
  let hash = 0;
  for (let index = 0; index < seed.length; index++) hash = (hash * 31 + seed.charCodeAt(index)) | 0;
  return channelTints[Math.abs(hash) % channelTints.length]!;
}
const compare = (a: string, b: string): number => a < b ? -1 : a > b ? 1 : 0;
const words = (s: string): string[] => s.match(/[\p{L}\p{N}]+/gu) ?? [];
const initials = (tokens: readonly string[]): string => {
  const letters = tokens.filter(t => /\p{L}/u.test(t));
  return (letters.length > 1 ? letters.slice(0, 2).map(t => [...t][0]).join('') : [...(letters[0] ?? tokens[0] ?? '')].slice(0, 2).join('')).toUpperCase();
};

/** No I/O or scans: consumes one caller-certified domain or already authoritative marks. */
export function channelMonograms(domain: ChannelMarkDomain): ChannelMarkIndex {
  if (!domain || domain.kind !== 'authoritative' && domain.kind !== 'complete-authorized') throw new Error('incomplete_channel_domain');
  const sources = new Map<string, Map<string, ChannelMarkModel>>();
  const identity = new Set<string>();
  for (const c of domain.channels) {
    const key = JSON.stringify([c.sourceId, c.id]);
    if (!c.sourceId || !c.id || identity.has(key)) throw new Error('invalid_channel_identity');
    identity.add(key);
  }
  if (domain.kind === 'authoritative') {
    for (const c of domain.channels) {
      if (!supplied(c.mark?.text) || !supplied(c.mark?.seed)) throw new Error('missing_authoritative_channel_mark');
      const group = sources.get(c.sourceId) ?? new Map<string, ChannelMarkModel>();
      group.set(c.id, Object.freeze({sourceId: c.sourceId, id: c.id, text: c.mark.text, seed: c.mark.seed})); sources.set(c.sourceId, group);
    }
  } else {
    const grouped = new Map<string, LineupChannel[]>();
    for (const c of domain.channels) { const group = grouped.get(c.sourceId) ?? []; group.push(c); grouped.set(c.sourceId, group); }
    for (const [sourceId, lineup] of grouped) {
      const rows = lineup.map(c => ({c, tokens: words(c.name), base: initials(words(c.name))}));
      // Shorter names retain the base. Length, channel number, name, then ID settle ties,
      // independently of caller/page order. Base marks are reserved before expanding collisions.
      rows.sort((a, b) => a.tokens.length - b.tokens.length || compare(a.c.number ?? '', b.c.number ?? '') || compare(a.c.name, b.c.name) || compare(a.c.id, b.c.id));
      const groups = new Map<string, typeof rows>();
      for (const row of rows) { const group = groups.get(row.base) ?? []; group.push(row); groups.set(row.base, group); }
      const used = new Set(rows.map(r => r.base).filter(Boolean)), marks = new Map<string, ChannelMarkModel>();
      for (const [base, same] of groups) for (let i = 0; i < same.length; i++) {
        const {c, tokens} = same[i]!;
        let text = base;
        if (i > 0 || !text) {
          const number = tokens.find(t => /^\d+$/.test(t));
          const letters = tokens.filter(t => /\p{L}/u.test(t));
          const candidates = [number ? `${[...letters[0] ?? ''][0] ?? ''}${number}`.toUpperCase() : '', letters.map(t => [...t][0]).join('').toUpperCase(), `${base}${c.number ?? ''}`];
          text = candidates.find(candidate => !!candidate && !used.has(candidate)) ?? '';
          if (!text) {
            let ordinal = 1;
            do { text = `${base}${ordinal++}`; } while (used.has(text));
          }
        }
        used.add(text);
        marks.set(c.id, Object.freeze({sourceId, id: c.id, text, seed: `${sourceId}:${c.id}`}));
      }
      sources.set(sourceId, marks);
    }
  }
  // Identity-keyed output iteration is deterministic too; output never mutates the input.
  return new Map([...sources].sort(([a], [b]) => compare(a, b)).map(([s, m]) => [s, new Map([...m].sort(([a], [b]) => compare(a, b)))]));
}

/** Zone is the device zone, never a server or profile setting. Omit it to use the runtime zone. */
export type TimeEnvironment = TextEnvironment & Readonly<{timeZone?: string; hourCycle?: 'h12' | 'h23'}>;
export type ProgrammeRange = Readonly<{start: string; end: string; text: string; spansDays: boolean}>;

/** One supplied instant in the device zone; no current-time read or manufactured interval. */
export function wallClockText(instantMs: number, env: TimeEnvironment): string | undefined {
  if (!Number.isFinite(instantMs) || !Number.isFinite(new Date(instantMs).getTime())) return undefined;
  try { return new Intl.DateTimeFormat(env.locale ?? 'en-US', {hour: 'numeric', minute: '2-digit', timeZone: env.timeZone, ...(env.hourCycle ? {hourCycle: env.hourCycle} : {})}).format(instantMs).replace(/[\u00a0\u202f]/g, ' '); }
  catch { return undefined; }
}

export function programmeTimeRange(startMs: number, endMs: number, env: TimeEnvironment): ProgrammeRange | undefined {
  if (!Number.isFinite(startMs) || !Number.isFinite(endMs) || endMs <= startMs) return undefined;
  const start = new Date(startMs), end = new Date(endMs);
  if (!Number.isFinite(start.getTime()) || !Number.isFinite(end.getTime())) return undefined;
  try {
    const locale = env.locale ?? 'en-US';
    const options: Intl.DateTimeFormatOptions = {hour: 'numeric', minute: '2-digit', timeZone: env.timeZone, ...(env.hourCycle ? {hourCycle: env.hourCycle} : {})};
    const fmt = new Intl.DateTimeFormat(locale, options), dateFmt = new Intl.DateTimeFormat(locale, {year: 'numeric', month: 'short', day: 'numeric', timeZone: env.timeZone});
    const startParts = fmt.formatToParts(start), endParts = fmt.formatToParts(end);
    const dayA = dateFmt.format(start), dayB = dateFmt.format(end), spansDays = dayA !== dayB;
    const periodA = startParts.find(p => p.type === 'dayPeriod')?.value, periodB = endParts.find(p => p.type === 'dayPeriod')?.value;
    const offsetFmt = new Intl.DateTimeFormat(locale, {...options, timeZoneName: 'shortOffset'});
    const offsetA = offsetFmt.formatToParts(start).find(p => p.type === 'timeZoneName')?.value, offsetB = offsetFmt.formatToParts(end).find(p => p.type === 'timeZoneName')?.value;
    const offsetChanged = offsetA !== offsetB;
    const render = (parts: Intl.DateTimeFormatPart[]) => parts.map(p => p.value).join('').replace(/[\u00a0\u202f]/g, ' ').trim();
    const omitPeriod = !spansDays && !offsetChanged && periodA && periodA === periodB && startParts.at(-1)?.type === 'dayPeriod' && endParts.at(-1)?.type === 'dayPeriod';
    let a = render(omitPeriod ? startParts.filter(p => p.type !== 'dayPeriod') : startParts), b = render(endParts);
    if (spansDays) { a = `${dayA}, ${a}`; b = `${dayB}, ${b}`; }
    if (offsetChanged) { a = `${a} ${offsetA}`; b = `${b} ${offsetB}`; }
    return Object.freeze({start: a, end: b, text: env.t('next.kit.range', {start: a, end: b}), spansDays});
  } catch { return undefined; }
}

export type ProgrammeFacts = Readonly<{
  startMs?: number; endMs?: number; episode?: EpisodeNumbering; subtitle?: string;
  year?: number; categories?: readonly string[]; rating?: string;
  flags?: Readonly<{live?: boolean; new?: boolean; premiere?: boolean}>; recording?: boolean;
}>;

export function programmeFactParts(facts: ProgrammeFacts, env: TimeEnvironment, nowMs?: number): readonly FactPart[] {
  const range = facts.startMs !== undefined && facts.endMs !== undefined ? programmeTimeRange(facts.startMs, facts.endMs, env) : undefined;
  const onAir = range && nowMs !== undefined && Number.isFinite(nowMs) && nowMs >= facts.startMs! && nowMs < facts.endMs!;
  const elapsed = onAir ? durationText((nowMs! - facts.startMs!) / 1000, env.t) : undefined;
  const left = onAir ? durationText((facts.endMs! - nowMs!) / 1000, env.t) : undefined;
  return compact([
    range ? part('time-range', range.text, 'context') : undefined,
    episodeLabel(facts.episode, 'player', env),
    supplied(facts.subtitle) ? part('programme-subtitle', facts.subtitle, 'identity') : undefined,
    facts.year && Number.isInteger(facts.year) && facts.year > 0 ? part('year', String(facts.year)) : undefined,
    ...(facts.categories ?? []).filter(supplied).map((c, i) => part(`category-${i}`, c)),
    supplied(facts.rating) ? part('content-rating', facts.rating, 'supplement') : undefined,
    facts.flags?.live === true ? part('live', env.t('next.kit.live'), 'status') : undefined,
    facts.flags?.new === true ? part('new', env.t('next.kit.new'), 'status') : undefined,
    facts.flags?.premiere === true ? part('premiere', env.t('next.kit.premiere'), 'status') : undefined,
    facts.recording === true ? part('recording', env.t('next.kit.recording'), 'status') : undefined,
    elapsed ? part('started', env.t('next.kit.started', {time: elapsed}), 'supplement') : undefined,
    left ? part('remaining', env.t('next.kit.remaining', {time: left}), 'supplement') : undefined,
  ]);
}
