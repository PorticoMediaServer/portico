import type {ContentEntry, ContentEntryKind} from '../library-content.ts';
import type {IconId} from '../../../design/src/icons.ts';

/**
 * Pure presentation helpers that both clients used to copy (X-05). No React,
 * no platform APIs. Locale-aware formatting lives in `@portico/i18n`
 * (`formatDuration`, `formatBytes`, `formatRelativeTime`); the versions here
 * are the en-US fallbacks the clients use until they adopt it.
 */

const viewerScopes = new WeakMap<object, {viewerId: string; serverId: string}>();

/** Stable viewer identity used to scope every content service. */
export function viewerScope(session: {viewer: {authority: string; accountId: string; profileId: string; serverId: string}} | undefined, api: {baseUrl: string}): {viewerId: string; serverId: string} {
  const value = {
    viewerId: session ? JSON.stringify([session.viewer.authority, session.viewer.accountId, session.viewer.profileId]) : 'signed-out',
    serverId: session?.viewer.serverId ?? api.baseUrl,
  };
  const previous = viewerScopes.get(api);
  if (previous?.viewerId === value.viewerId && previous.serverId === value.serverId) return previous;
  viewerScopes.set(api, value);
  return value;
}

/** The library-family glyph for an entry kind or library kind. */
export function iconFor(kind: ContentEntryKind | string): IconId {
  switch (kind) {
    case 'artist': case 'album': case 'song': case 'disc': case 'music':
      return 'music';
    case 'book': case 'audiobook_file': case 'chapter': case 'author': case 'book_series': case 'audiobook':
      return 'book';
    case 'episode': case 'season': case 'show': case 'tv':
      return 'tv';
    // An Anime library is not a second "TV Shows" in the sidebar and rail (TV-RAIL review).
    case 'anime':
      return 'category';
    case 'collection':
      return 'collection';
    case 'category':
      return 'category';
    default:
      return 'film';
  }
}

/** Secondary caption line for a card. */
export function captionFor(entry: Pick<ContentEntry, 'kind' | 'subtitle' | 'seasonNumber' | 'episodeNumber' | 'count'> & {countIsLowerBound?: boolean}): string | undefined {
  if (entry.subtitle) return entry.subtitle;
  if (entry.kind === 'episode' && entry.seasonNumber != null && entry.episodeNumber != null) return `S${entry.seasonNumber} · E${entry.episodeNumber}`;
  if (entry.count != null && entry.countIsLowerBound) return `${entry.count.toLocaleString('en-US')}+ items`;
  if (entry.count != null) return `${entry.count} ${entry.count === 1 ? 'item' : 'items'}`;
  return undefined;
}

/** Real progress ratio 0…1 (never exaggerated, PC-VISUAL §13.7). */
export function progressFor(entry: {duration?: number; progressSeconds?: number}): number {
  if (!entry.duration || !entry.progressSeconds) return 0;
  return Math.max(0, Math.min(1, entry.progressSeconds / entry.duration));
}

/** "1h 42m", "58m", "45s" (en-US fallback; prefer i18n `formatDuration`). */
export function formatDuration(seconds?: number): string {
  if (!seconds || seconds <= 0 || !Number.isFinite(seconds)) return '';
  const total = Math.round(seconds);
  const h = Math.floor(total / 3600);
  const m = Math.round((total % 3600) / 60);
  if (h > 0) return m ? `${h}h ${m}m` : `${h}h`;
  if (m > 0) return `${m}m`;
  return `${total}s`;
}

/** Media time counter: "1:02:03" or "4:05". */
export function formatClock(seconds: number): string {
  const total = Math.max(0, Math.floor(Number.isFinite(seconds) ? seconds : 0));
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}` : `${m}:${String(s).padStart(2, '0')}`;
}

/** "1.4 GB" (binary units, en-US fallback; prefer i18n `formatBytes`). */
export function formatBytes(bytes?: number): string {
  if (bytes == null || !Number.isFinite(bytes)) return '—';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let v = bytes, i = 0;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  return `${v < 10 && i > 0 ? v.toFixed(1) : Math.round(v)} ${units[i]}`;
}

/** Show a host only when a person would recognise it; generated relay names are noise. */
export function friendlyHost(url?: string): string | undefined {
  if (!url) return undefined;
  const host = url.replace(/^https?:\/\//, '').replace(/[/:].*$/, '');
  if (!host || /^[0-9a-z-]{24,}\./i.test(host) || /^\d+-\d+-\d+-\d+-/.test(host)) return undefined;
  return host;
}

/** Turn a machine token into a sentence-case phrase ("in_progress" → "In progress"). Never Title Case. */
export function sentence(value: string): string {
  const words = value.replace(/([a-z])([A-Z])/g, '$1 $2').replace(/[_-]+/g, ' ').trim().toLowerCase();
  return words.replace(/^\w/, c => c.toUpperCase());
}
