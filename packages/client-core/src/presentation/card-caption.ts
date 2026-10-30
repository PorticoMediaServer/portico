import type {ContentEntry} from '../library-content.ts';
import type {MessageId, MessageValues} from '../../../i18n/src/index.ts';

/**
 * The line under a card's title, one rule for every client (Justin, 2 Oct 2026): an album always
 * names its artist, a book its author, an episode its number and show, and nothing ever reads
 * "0 items". Continue Watching says what is left ("24 min left"). Cards lay TV out in Plex's
 * shape through `cardLines` (Justin, 18 Oct 2026); this is the line for everything else.
 */
type Translate = (id: MessageId, values?: MessageValues) => string;
export type CaptionEntry = Pick<ContentEntry, 'kind' | 'subtitle' | 'seasonNumber' | 'episodeNumber' | 'absoluteNumber' | 'count' | 'duration' | 'artist' | 'author' | 'year' | 'airDate' | 'latestEpisode' | 'progressSeconds' | 'countIsLowerBound'>;

const YEAR = /^(18|19|20)\d{2}$/;
const parts = (subtitle?: string) => (subtitle ? subtitle.split(' · ').map(p => p.trim()).filter(Boolean) : []);

/**
 * An episode's number as a code: "S1 E3", "E3" for a show without seasons, and "Episode 1043"
 * when the show is numbered across its seasons (never "Season 1, Episode 1043").
 */
export function episodeCode(entry: Pick<ContentEntry, 'seasonNumber' | 'episodeNumber' | 'absoluteNumber'>, t: Translate): string | undefined {
  if (entry.absoluteNumber != null) return t('title.episodeAbsolute', {number: entry.absoluteNumber});
  if (entry.episodeNumber == null) return undefined;
  return entry.seasonNumber != null ? t('title.episodeCode', {season: entry.seasonNumber, episode: entry.episodeNumber}) : t('title.episodeOnly', {episode: entry.episodeNumber});
}

/** "Oct 1" this year, "Oct 1, 2024" otherwise, from YYYY-MM-DD (read as a calendar day, not an instant). */
export function shortDate(day: string, locale?: string, now: Date = new Date()): string {
  const [y, m, d] = day.split('-').map(Number);
  if (!y || !m || !d) return day;
  const date = new Date(y, m - 1, d);
  return date.toLocaleDateString(locale, y === now.getFullYear() ? {month: 'short', day: 'numeric'} : {month: 'short', day: 'numeric', year: 'numeric'});
}

/** The caption of a card or list row. */
export function cardCaption(entry: CaptionEntry, t: Translate, options: Readonly<{locale?: string}> = {}): string | undefined {
  const count = entry.count ?? 0;
  const sub = parts(entry.subtitle);
  // A grouping under a broad filter counts each group only up to 10,000: "10,000+ titles".
  if (entry.countIsLowerBound && count > 0 && !entry.subtitle) return t('lib.countAtLeast', {count, pivot: 'other'});
  switch (entry.kind) {
    case 'artist': return count > 0 ? t('title.songCount', {count}) : undefined;
    case 'album': {
      // The artist first, always; then the year.
      const artist = entry.artist?.name ?? sub.find(p => !YEAR.test(p));
      const year = entry.year ? String(entry.year) : sub.find(p => YEAR.test(p));
      return [artist, year].filter(Boolean).join(' · ') || undefined;
    }
    case 'book': return entry.author || sub.find(p => !YEAR.test(p)) || undefined;
    case 'episode': {
      // The number first: a narrow card cuts the line short, and the show is already on the poster.
      const out = [episodeCode(entry, t), entry.airDate ? shortDate(entry.airDate, options.locale) : undefined, sub[0]].filter(Boolean);
      return out.length ? out.join(' · ') : undefined;
    }
    case 'collection':
    case 'category':
    case 'author':
    case 'book_series':
      return count > 0 ? t('title.itemCount', {count}) : entry.subtitle || undefined;
    case 'show':
      // Recently aired: the card is the show's, the line its newest episode — or how many aired that day.
      if (entry.latestEpisode) {
        const latest = entry.latestEpisode;
        const what = latest.count > 1 ? t('card.episodesAired', {count: latest.count}) : episodeCode(latest, t);
        return [what, shortDate(latest.airDate, options.locale)].filter(Boolean).join(' · ');
      }
      return entry.subtitle || (entry.year ? String(entry.year) : undefined);
    case 'movie':
      // The year, so two titles of one name can be told apart (search, grids).
      return entry.subtitle || (entry.year ? String(entry.year) : undefined);
    default:
      return entry.subtitle || undefined;
  }
}

/** What is left of a started title ("24 min left", "1 hr 5 min left"); nothing for one not started. */
export function remainingLabel(entry: Pick<ContentEntry, 'duration' | 'progressSeconds'>, t: Translate): string | undefined {
  const left = entry.duration && entry.progressSeconds ? Math.max(0, entry.duration - entry.progressSeconds) : 0;
  if (left <= 0) return undefined;
  const minutes = Math.max(1, Math.round(left / 60));
  return minutes >= 60 ? t('card.hoursLeft', {hours: Math.floor(minutes / 60), minutes: minutes % 60}) : t('card.minutesLeft', {count: minutes});
}

/** Continue Watching: what is left, with the episode's number when it is one ("S1 E3 · 24 min left"). */
export function continueCaption(entry: CaptionEntry, t: Translate): string | undefined {
  const remaining = remainingLabel(entry, t);
  const lead = entry.kind === 'episode' ? episodeCode(entry, t) : undefined;
  const out = [lead, remaining].filter(Boolean);
  return out.length ? out.join(' · ') : cardCaption(entry, t);
}

/** A Continue Watching card's title: the show for an episode, whose number is in the caption. */
export function continueTitle(entry: Pick<ContentEntry, 'kind' | 'title' | 'subtitle'>): string {
  return entry.kind === 'episode' ? parts(entry.subtitle)[0] ?? entry.title : entry.title;
}

/** A card's text, top to bottom: its title, the line under it and, for TV, a third line. */
export type CardLines = Readonly<{title: string; caption?: string; detail?: string}>;
export type CardLinesEntry = CaptionEntry & Pick<ContentEntry, 'title' | 'seasonCount'>;

/**
 * A card's lines, in Plex's shape for TV (Justin, 18 Oct 2026), on every client and every row:
 * an episode reads its show, its own title, then "S1 · E3"; a season its show, then "Season 1";
 * a show added whole (Recently Added) its title, then "6 seasons". Everything else is its title
 * over `cardCaption`.
 */
export function cardLines(entry: CardLinesEntry, t: Translate, options: Readonly<{locale?: string}> = {}): CardLines {
  const show = parts(entry.subtitle)[0];
  switch (entry.kind) {
    case 'episode': {
      const code = entry.absoluteNumber != null ? t('title.episodeAbsolute', {number: entry.absoluteNumber})
        : entry.episodeNumber == null ? undefined
        : entry.seasonNumber != null ? t('card.episodeCode', {season: entry.seasonNumber, episode: entry.episodeNumber})
        : t('title.episodeOnly', {episode: entry.episodeNumber});
      if (!show) return {title: entry.title, caption: code};
      return {title: show, caption: entry.title && entry.title !== show ? entry.title : undefined, detail: code};
    }
    case 'season': {
      const season = entry.seasonNumber === 0 ? t('title.specials') : entry.seasonNumber != null ? t('title.season', {number: entry.seasonNumber}) : entry.title;
      return show ? {title: show, caption: season} : {title: season};
    }
    case 'show':
      if (entry.seasonCount) return {title: entry.title, caption: t('title.seasonCount', {count: entry.seasonCount})};
      break;
  }
  return {title: entry.title, caption: cardCaption(entry, t, options)};
}

/** A Continue Watching card's lines: an episode as everywhere else (the bar shows what is left); anything else says what is left. */
export function continueLines(entry: CardLinesEntry, t: Translate, options: Readonly<{locale?: string}> = {}): CardLines {
  return entry.kind === 'episode' ? cardLines(entry, t, options) : {title: entry.title, caption: continueCaption(entry, t)};
}

/** The rows of what the viewer is in the middle of: Home's, and a library's Discover tab's. */
export const isContinueRow = (rowId: string) => rowId === 'continue' || rowId === 'continue_listening' || rowId === 'continue_watching';

const BOOK_KINDS = new Set(['book', 'audiobook_file', 'chapter', 'book_series']);
const MUSIC_KINDS = new Set(['song', 'album', 'artist', 'disc']);

/** How "watched" is said for a kind: a book is finished, music is played, everything else is watched. */
export function watchedWords(kind?: string): Readonly<{mark: MessageId; unmark: MessageId}> {
  if (kind && BOOK_KINDS.has(kind)) return {mark: 'title.markFinished', unmark: 'title.markNotFinished'};
  if (kind && MUSIC_KINDS.has(kind)) return {mark: 'entry.markPlayed', unmark: 'entry.markUnplayed'};
  return {mark: 'entry.markWatched', unmark: 'entry.markUnwatched'};
}
