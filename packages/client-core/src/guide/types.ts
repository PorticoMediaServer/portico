import type {GuideChannelMark} from '../channel-guide.ts';

/**
 * The guide's shared data shapes (Spec — Channels and Guide §8.1). Times are UTC milliseconds;
 * the viewer's time zone is used only for labels and day starts (time.ts).
 */
export type ChannelKind = 'live' | 'library';

/** A Channels entry (§2.0): one owner-named source, or Library Channels. */
export type ChannelSource = Readonly<{
  id: string;
  name: string;
  type: 'm3u' | 'xtream' | 'hdhomerun' | 'tuner' | 'library' | 'live';
  position: number;
  recordAvailable: boolean;
  guideState?: 'ready' | 'refreshing' | 'stale' | 'none';
  availableStart?: number;
  availableEnd?: number;
  /**
   * Guide.days for this source's kind (how many calendar days from now are covered, today
   * counts as 1, at most 31). Present only when the server sends it; the day picker offers
   * exactly those days, else it falls back to the available range (7 days).
   */
  guideDays?: number;
}>;

export type GuideChannelRow = Readonly<{
  id: string;
  /** The source this channel belongs to (labels rows in All channels). */
  sourceId?: string;
  kind: ChannelKind;
  number: string;
  name: string;
  group: string;
  logoUrl?: string;
  mark?: GuideChannelMark;
  favorite: boolean;
  tuneAvailable: boolean;
  tuneUnavailableReason?: string;
  recordAvailable: boolean;
  /** Whether the channel publishes a guide: `none` renders one "No information" cell per row. */
  guide: 'full' | 'partial' | 'none';
  /**
   * Opaque: the source's own object for this channel (the stopgap adapter's `/v1/guide` channel).
   * Screens read it only through the adapter's accessor (`legacyChannel`), never directly.
   */
  native?: unknown;
}>;

export type GuideProgram = Readonly<{
  id: string;
  channelId: string;
  title: string;
  start: number;
  end: number;
  subtitle?: string;
  episode?: Readonly<{season?: number; number?: number; display?: string}>;
  categories?: readonly string[];
  rating?: string;
  year?: number;
  starRating?: string;
  image?: string;
  flags?: Readonly<{live?: boolean; new?: boolean; premiere?: boolean; repeat?: boolean; finale?: boolean}>;
  seriesId?: string;
  recording?: Readonly<{id: string; state: string}>;
  /** The program's summary, for the program sheet. */
  description?: string;
  /** Opaque: the source's own object for this program (read through `legacyProgramme`). */
  native?: unknown;
}>;

/** A channel-page request answer. */
export type ChannelPage = Readonly<{items: readonly GuideChannelRow[]; total: number; revision?: string}>;

/**
 * Where the guide comes from (the §8.1 endpoints, or an adapter over today's `/v1/guide`).
 * `programs` returns every program overlapping [start, end) for each channel, whole (true times).
 */
export type GuideDataSource = Readonly<{
  /** Forget directory revisions when the store starts a fresh view. */
  reset?(): void;
  /** Adjacent watchable row in this filtered/sorted view; wraps without loading its full lineup. */
  neighbor?(channel: GuideChannelRow, delta: 1 | -1, signal: AbortSignal): Promise<GuideChannelRow | undefined>;
  channels(from: number, limit: number, signal: AbortSignal): Promise<ChannelPage>;
  programs(channelIds: readonly string[], start: number, end: number, signal: AbortSignal): Promise<Readonly<Record<string, readonly GuideProgram[]>>>;
}>;
