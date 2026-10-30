import type {ContentEntry} from '../library-content.ts';
import type {WorkspaceShowCredit} from '../show-workspace.ts';

/**
 * Title-page rules shared by every client (Spec — Page Content by Media Type §0): what a related
 * row needs before it is worth a heading, how a show's credits split into cast and key crew, and
 * the one-line facts each page prints.
 */

/** A related row earns a heading with three or more titles; two rows with the same titles are one row. */
export function relatedRowsForPage<T extends Readonly<{id: string; entries: readonly ContentEntry[]}>>(rows: readonly T[], minimum = 3): T[] {
  const seen = new Set<string>();
  const out: T[] = [];
  for (const row of rows) {
    if (row.entries.length < minimum) continue;
    const key = row.entries.map(e => e.id).join('|');
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(row);
  }
  return out;
}

/** Genres once each, in first-seen order, however many providers repeated them. */
export function dedupeNames(names: readonly string[], max = 3): string[] {
  const seen = new Map<string, string>();
  for (const name of names) {
    const trimmed = name.trim();
    if (!trimmed) continue;
    const key = trimmed.toLowerCase();
    if (!seen.has(key)) seen.set(key, trimmed);
  }
  return [...seen.values()].slice(0, max);
}

const CAST_DEPARTMENTS = new Set(['acting', 'cast', 'actor', 'actors', 'guest star', 'guest stars']);
const CREW_RANK: readonly [RegExp, number][] = [[/creator|created by/i, 0], [/showrunner/i, 1], [/directing|director/i, 2], [/writing|writer|screenplay|teleplay/i, 3], [/composer|music/i, 4], [/executive producer|producer/i, 5]];

export type ShowPeople = Readonly<{cast: readonly WorkspaceShowCredit[]; crew: readonly WorkspaceShowCredit[]}>;

/** A show's people split the way the page prints them: cast in billing order, then key crew by role, one row per person, at most `maxCrew`. */
export function showPeople(credits: readonly WorkspaceShowCredit[] | undefined, maxCrew = 8): ShowPeople {
  if (!credits?.length) return {cast: [], crew: []};
  const cast: WorkspaceShowCredit[] = [];
  const ranked: {credit: WorkspaceShowCredit; rank: number}[] = [];
  for (const c of credits) {
    const department = (c.department ?? '').trim().toLowerCase();
    if (CAST_DEPARTMENTS.has(department) || (!department && !c.role)) { cast.push(c); continue; }
    const haystack = `${c.department ?? ''} ${c.role ?? ''}`;
    const rank = CREW_RANK.find(([re]) => re.test(haystack))?.[1];
    if (rank !== undefined) ranked.push({credit: c, rank});
  }
  ranked.sort((a, b) => a.rank - b.rank || a.credit.ordinal - b.credit.ordinal);
  const seen = new Set<string>();
  const crew: WorkspaceShowCredit[] = [];
  for (const {credit} of ranked) {
    const key = credit.id ?? `name:${credit.name.toLowerCase()}`;
    if (seen.has(key)) continue;
    seen.add(key);
    crew.push(credit);
    if (crew.length >= maxCrew) break;
  }
  return {cast: cast.sort((a, b) => a.ordinal - b.ordinal), crew};
}

/** "Created by A, B" style lines for a show: the first names in each key role. */
export function showCreditLines(crew: readonly WorkspaceShowCredit[], label: (role: 'creator' | 'director' | 'writer', names: string) => string, max = 3): string[] {
  const names = (test: RegExp) => dedupeNames(crew.filter(c => test.test(`${c.department ?? ''} ${c.role ?? ''}`)).map(c => c.name), max).join(', ');
  const creators = names(/creator|created by|showrunner/i);
  const directors = names(/directing|director/i);
  const writers = names(/writing|writer|screenplay|teleplay/i);
  return [creators ? label('creator', creators) : '', directors ? label('director', directors) : '', writers ? label('writer', writers) : ''].filter(Boolean);
}

/**
 * A title's own tint when it has no artwork to take one from: a stable hue (0–359) from its id,
 * so every missing still of one show shares a colour and two shows differ (Spec — Page Content
 * §0.2). The server publishes no dominant colour yet; when it does, that wins.
 */
export function titleTintHue(seed: string): number {
  let h = 2166136261;
  for (let i = 0; i < seed.length; i++) h = Math.imul(h ^ seed.charCodeAt(i), 16777619);
  return (h >>> 0) % 360;
}

/** Season progress beside the switcher ("6 of 10 watched"); nothing for a season without episodes. */
export function seasonProgress(watchedCount?: number, unwatchedCount?: number): Readonly<{watched: number; total: number}> | undefined {
  if (watchedCount === undefined || unwatchedCount === undefined) return undefined;
  const total = watchedCount + unwatchedCount;
  return total > 0 ? {watched: watchedCount, total} : undefined;
}

/**
 * The next-up episode pinned above a season's list: shown when it belongs to the season on
 * screen and is not already its first row, so a viewer twelve episodes in never scrolls to find
 * where they are.
 */
export function nextUpPin<T extends Readonly<{id: string; seasonNumber?: number}>>(nextUp: T | undefined, first: Readonly<{id: string}> | undefined, seasonNumber: number | undefined): T | undefined {
  if (!nextUp || !first || nextUp.id === first.id) return undefined;
  if (seasonNumber !== undefined && nextUp.seasonNumber !== undefined && nextUp.seasonNumber !== seasonNumber) return undefined;
  return nextUp;
}

/** An artist's years line: "1965–1970", "since 1996". */
export function activeYearsLabel(begin?: number, end?: number, since = (y: number) => `since ${y}`): string | undefined {
  if (begin && end) return `${begin}–${end}`;
  if (begin) return since(begin);
  return undefined;
}

/** A show's network as the page says it. A library's kind is not a network: "Anime" is never printed as one. */
export function networkName(network?: string): string | undefined {
  const name = network?.trim();
  return name && name.toLowerCase() !== 'anime' ? name : undefined;
}

/** The original title when it differs from the title: shown as a subtitle under the title, not as a fact. */
export function originalTitleLine(title?: string, original?: string): string | undefined {
  const value = original?.trim();
  return value && value.toLowerCase() !== (title ?? '').trim().toLowerCase() ? value : undefined;
}

/** The four per-show settings an owner sets from the show's page, in the order the dialog lists them. */
export const SHOW_SETTING_ROWS = [
  {key: 'hideSeasons', label: 'title.showSettings.hideSeasons', help: 'title.showSettings.hideSeasonsHelp'},
  {key: 'absoluteNumbering', label: 'title.showSettings.absolute', help: 'title.showSettings.absoluteHelp'},
  {key: 'ranges', label: 'title.showSettings.ranges', help: 'title.showSettings.rangesHelp'},
  {key: 'newestFirst', label: 'title.showSettings.newestFirst', help: 'title.showSettings.newestFirstHelp'},
] as const;
