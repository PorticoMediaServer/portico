import type {ContentEntry} from '../library-content.ts';
import type {HomeDocument, HomeRow} from '../home.ts';
import type {MessageId, MessageValues} from '../../../i18n/src/index.ts';
import {continueTitle, episodeCode} from './card-caption.ts';

/**
 * Home's hero (Justin, 2 Oct 2026): what the viewer was last in the middle of. The server names
 * the entry — the newer of the first Continue Watching and the first Continue Listening entry —
 * and this is everything a client writes on it, so the web, iPhone and Apple TV say the same
 * thing. There is no hero when nothing is in progress; Home then opens with its first row.
 */
type Translate = (id: MessageId, values?: MessageValues) => string;

export type HomeHero = Readonly<{
  row: HomeRow;
  entry: ContentEntry;
  /** The show for an episode; the title itself otherwise. */
  title: string;
  /** Under the title: "S2 E3 · Ozymandias" for an episode, the artist for a song or an album, the author for a book. */
  line?: string;
  /** Year · rating · length · up to two genres. */
  meta?: string;
  /** How far in, 0–1; 0 for a next episode that has not been started. */
  progress: number;
  /** The primary button: "Resume · 24 min left", "Resume", or "Play". */
  playLabel: string;
  /** `backdrop` fills the hero with wide artwork; `cover` has only a poster or a square cover to show beside the words. */
  art: 'backdrop' | 'cover';
  /** The cover's shape when `art` is `cover`. */
  coverShape: 'poster' | 'square';
}>;

const SQUARE = new Set<string>(['song', 'album', 'artist', 'book', 'audiobook_file', 'chapter', 'disc']);
const parts = (subtitle?: string) => (subtitle ? subtitle.split(' · ').map(p => p.trim()).filter(Boolean) : []);

function line(entry: ContentEntry, t: Translate): string | undefined {
  if (entry.kind === 'episode') return [episodeCode(entry, t), entry.title].filter(Boolean).join(' · ') || undefined;
  if (entry.artist?.name) return entry.album?.name ? `${entry.artist.name} · ${entry.album.name}` : entry.artist.name;
  if (entry.author) return entry.author;
  return undefined;
}

/** The hero of a Home document. `hidden` holds entries the viewer has just removed from a row. */
export function homeHero(document: HomeDocument | undefined, t: Translate, options: Readonly<{duration: (seconds: number) => string; hidden?: ReadonlySet<string>}>): HomeHero | undefined {
  const ref = document?.hero;
  if (!document || !ref || options.hidden?.has(ref.entryId)) return undefined;
  const row = document.rows.find(r => r.id === ref.rowId);
  const entry = row?.entries.find(e => e.id === ref.entryId);
  if (!row || !entry) return undefined;
  const video = entry.kind === 'movie' || entry.kind === 'episode';
  const year = entry.year ? String(entry.year) : undefined;
  const show = entry.kind === 'episode' ? parts(entry.subtitle)[0] : undefined;
  const meta = [
    // A movie's subtitle is its year, an episode's leads with its show: neither is said twice.
    ...parts(entry.subtitle).filter(p => p !== year && p !== show && video && entry.kind !== 'episode'),
    year, entry.contentRating, entry.duration ? options.duration(entry.duration) : undefined, ...(entry.genres ?? []).slice(0, 2),
  ].filter(Boolean).join(' · ');
  const progress = entry.duration && entry.progressSeconds ? Math.min(1, Math.max(0, entry.progressSeconds / entry.duration)) : 0;
  const left = entry.duration && entry.progressSeconds ? Math.max(0, entry.duration - entry.progressSeconds) : 0;
  // A client's formatter may have nothing to say about a short stretch; the button then says plain "Resume".
  const remaining = left >= 60 ? options.duration(left) : '';
  return Object.freeze({
    row, entry,
    title: continueTitle(entry),
    line: line(entry, t),
    meta: meta || undefined,
    progress,
    playLabel: remaining ? t('entry.resumeRemaining', {remaining}) : t(progress > 0 ? 'entry.resume' : 'action.play'),
    art: entry.backdropUrl ? 'backdrop' : 'cover',
    coverShape: SQUARE.has(entry.kind) ? 'square' : 'poster',
  });
}
