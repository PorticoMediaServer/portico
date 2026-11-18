/**
 * The guide's row and programme wrappers: a channel or programme in the guide's own shape keeps the
 * object it was made from (`native`), read back through `legacyChannel`/`legacyProgramme` for tuning,
 * recording and favourites. The rows come from the rebuilt server (`next-source.ts`, `native-source.ts`).
 */
import type {GuideChannel, GuideProgramme} from '../channel-guide.ts';
import type {GuideChannelRow, GuideProgram} from './types.ts';

export type LegacyGuideOptions = Readonly<{
  /** `all`: live sources, then Library Channels (All channels, §2.0). */
  kind: 'live-source' | 'library-channel' | 'all';
  /** '' for every source of the kind. */
  sourceId: string;
  timezone: string;
  search?: string;
  group?: string;
  favorites?: boolean;
  /** Owners: include hidden channels (to unhide them). */
  includeHidden?: boolean;
  includeServerHidden?: boolean;
  /**
   * `server` keeps the server's order. `number`: sources in the owner's order, each by channel
   * number; `name` interleaves sources by name (§2.2).
   */
  sort?: 'server' | 'number' | 'name' | 'favorites';
  now?: () => number;
}>;

// The handles this adapter attached, so the accessors never trust an object they didn't make.
const nativeChannels = new WeakSet<object>();
const nativeProgrammes = new WeakSet<object>();

/** The `/v1/guide` channel behind a row this adapter made (for tuning, recording and favorites). */
export function legacyChannel(row: GuideChannelRow): GuideChannel | undefined {
  const n = row.native;
  return n !== null && typeof n === 'object' && nativeChannels.has(n) ? n as GuideChannel : undefined;
}

/** The `/v1/guide` programme behind a program this adapter made (for recording). */
export function legacyProgramme(p: GuideProgram): GuideProgramme | undefined {
  const n = p.native;
  return n !== null && typeof n === 'object' && nativeProgrammes.has(n) ? n as GuideProgramme : undefined;
}

export function legacyChannelRow(c: GuideChannel): GuideChannelRow {
  nativeChannels.add(c);
  return {
    id: c.id, sourceId: c.provenance === 'library-channel' ? 'library' : c.sourceId, kind: c.provenance === 'library-channel' ? 'library' : 'live',
    number: c.number, name: c.name, group: c.group, ...(c.logoPath ? {logoUrl: c.logoPath} : {}), favorite: c.favorite,
    ...(c.mark ? {mark: c.mark} : {}),
    tuneAvailable: c.tuneAvailable, ...(c.tuneUnavailableReason ? {tuneUnavailableReason: c.tuneUnavailableReason} : {}),
    recordAvailable: c.recordAvailable, guide: c.guide, native: c,
  };
}

export function legacyProgram(p: GuideProgramme): GuideProgram {
  nativeProgrammes.add(p);
  return {
    id: p.id, channelId: p.channelId, title: p.title, start: Date.parse(p.start), end: Date.parse(p.end),
    ...(p.seriesId ? {seriesId: p.seriesId} : {}),
    ...(p.subtitle ? {subtitle: p.subtitle} : {}),
    ...(p.episode ? {episode: p.episode} : {}),
    ...(p.categories !== undefined ? {categories: p.categories} : {}),
    ...(p.rating ? {rating: p.rating.value} : {}),
    ...(p.year !== undefined ? {year: p.year} : {}),
    ...(p.starRating ? {starRating: p.starRating} : {}),
    ...(p.image ? {image: p.image} : {}),
    flags: {live: p.flags?.live === true, new: p.flags?.new === true || p.newEvidence === 'new', premiere: p.flags?.premiere === true, repeat: p.flags?.repeat === true || p.newEvidence === 'repeat'},
    ...(p.recordingId ? {recording: {id: p.recordingId, state: p.recordingState ?? ''}} : {}),
    ...(p.description ? {description: p.description} : {}),
    native: p,
  };
}

/** The refresh states in which a source's guide is out of date; any other state is fine. */
const staleRefreshStates: ReadonlySet<string> = new Set(['degraded', 'credentials-required', 'stale']);

/** A live source's guide state from its `/v1/guide` refresh state. */
export function legacyGuideState(refreshState: string): 'ready' | 'refreshing' | 'stale' {
  return refreshState === 'refreshing' ? 'refreshing' : staleRefreshStates.has(refreshState) ? 'stale' : 'ready';
}
