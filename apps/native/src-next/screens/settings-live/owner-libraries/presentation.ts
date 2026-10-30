import {defaultI18n, type MessageId} from '../../../../../../packages/i18n/src/index';
import type {LibraryAdminJob, ManagedLibrary} from '../../../../../../packages/client-core/src/library-management';
import type {ScanIssue} from '../../../../../../packages/client-core/src/scan-issues';
import type {InventorySource, ScanTier} from '../../../../../../packages/client-core/src/library-inventory';
import type {LibrarySettings} from '../../../../../../packages/client-core/src/administration';
import type {IconName} from '../../../kit/Icon';

export type Translate = typeof defaultI18n.t;

/** The seven kinds, in the order Portico lists them, with the kit glyph each one draws. */
export const ownerLibraryKinds: readonly Readonly<{kind: ManagedLibrary['kind']; icon: IconName; label: MessageId; description: MessageId}>[] = Object.freeze([
  {kind: 'movie', icon: 'i-film', label: 'next.ownerLibraries.kind.movie', description: 'next.ownerLibraries.kindDescription.movie'},
  {kind: 'tv', icon: 'i-tvlib', label: 'next.ownerLibraries.kind.tv', description: 'next.ownerLibraries.kindDescription.tv'},
  {kind: 'anime', icon: 'i-playc', label: 'next.ownerLibraries.kind.anime', description: 'next.ownerLibraries.kindDescription.anime'},
  {kind: 'music', icon: 'i-music', label: 'next.ownerLibraries.kind.music', description: 'next.ownerLibraries.kindDescription.music'},
  {kind: 'audiobook', icon: 'i-headph', label: 'next.ownerLibraries.kind.audiobook', description: 'next.ownerLibraries.kindDescription.audiobook'},
  {kind: 'book', icon: 'i-sbook', label: 'next.ownerLibraries.kind.book', description: 'next.ownerLibraries.kindDescription.book'},
  {kind: 'comic', icon: 'i-book', label: 'next.ownerLibraries.kind.comic', description: 'next.ownerLibraries.kindDescription.comic'},
]);

export function ownerLibraryKind(kind: string): (typeof ownerLibraryKinds)[number] {
  return ownerLibraryKinds.find(value => value.kind === kind) ?? ownerLibraryKinds[0]!;
}

/** A fan of covers only ever mixes shapes of one kind: posters, or squares for sound and books. */
export function ownerLibrarySquare(kind: string): boolean {
  return kind === 'music' || kind === 'audiobook' || kind === 'book' || kind === 'comic';
}

const pivots: Readonly<Record<string, string>> = {movie: 'movies', show: 'shows', episode: 'episodes', artist: 'artists', album: 'albums', song: 'songs', book: 'books', author: 'authors', series: 'series', collection: 'collections', ebook: 'books', comic: 'titles'};

/** The server's own counts for this library, said with the catalogue's counted nouns. */
export function ownerLibraryCounts(library: Pick<ManagedLibrary, 'summary'>, t: Translate = defaultI18n.t): readonly string[] {
  const summary = library.summary;
  if (!summary) return [];
  return summary.counts.filter(count => pivots[count.kind]).map(count => t(count.atLeast ? 'lib.countAtLeast' : 'lib.count', {count: count.count, pivot: pivots[count.kind]!}));
}

const liveJob = (status: LibraryAdminJob['status']) => status === 'queued' || status === 'running';

function startOfDay(value: Date): number {return new Date(value.getFullYear(), value.getMonth(), value.getDate()).getTime();}

/** Whole days between two instants in the device's own calendar; only 0 and 1 have drawn words. */
export function ownerLibraryDayDistance(finishedAt: string, now: Date): number | undefined {
  const finished = new Date(finishedAt);
  if (!Number.isFinite(finished.getTime())) return undefined;
  return Math.round((startOfDay(now) - startOfDay(finished)) / 86_400_000);
}

export function ownerLibraryTime(finishedAt: string, locale: string): string {
  const finished = new Date(finishedAt);
  if (!Number.isFinite(finished.getTime())) return '';
  return new Intl.DateTimeFormat(locale, {hour: 'numeric', minute: '2-digit'}).format(finished);
}

export function ownerLibraryDate(finishedAt: string, locale: string): string {
  const finished = new Date(finishedAt);
  if (!Number.isFinite(finished.getTime())) return '';
  return new Intl.DateTimeFormat(locale, {month: 'short', day: 'numeric'}).format(finished);
}

/** The one line of state under a library: scanning wins, then the real finish, then never scanned. */
export function ownerLibraryState(library: ManagedLibrary, options: Readonly<{t?: Translate; locale?: string; now?: Date; short?: boolean}> = {}): Readonly<{text: string; live: boolean}> {
  const t = options.t ?? defaultI18n.t, locale = options.locale ?? defaultI18n.locale, now = options.now ?? new Date(), short = options.short === true;
  if (library.source.availability === 'unavailable') return {text: t('web.libraries.sourceUnavailable'), live: false};
  const scan = library.lastScan;
  if (!scan) return {text: t(short ? 'web.libraries.notScannedYet' : 'next.ownerLibraries.neverScanned'), live: false};
  if (liveJob(scan.status)) return {text: t('next.ownerLibraries.scanning'), live: true};
  if (scan.status === 'paused') return {text: t('next.ownerLibraries.paused'), live: false};
  const finishedAt = scan.finishedAt;
  if (!finishedAt) return {text: t(scan.status === 'failed' ? 'next.ownerLibraries.scanFailed' : 'next.ownerLibraries.neverScanned'), live: false};
  const distance = ownerLibraryDayDistance(finishedAt, now);
  if (distance === 0) return {text: short ? t('next.ownerLibraries.scannedTodayShort') : t('next.ownerLibraries.scannedToday', {time: ownerLibraryTime(finishedAt, locale)}), live: false};
  if (distance === 1) return {text: short ? t('next.ownerLibraries.scannedYesterdayShort') : t('next.ownerLibraries.scannedYesterday', {time: ownerLibraryTime(finishedAt, locale)}), live: false};
  const date = ownerLibraryDate(finishedAt, locale);
  return {text: short ? t('next.ownerLibraries.scannedOnShort', {date}) : t('next.ownerLibraries.scannedOn', {date}), live: false};
}

/** Who may see the library: the server's real audience, never a guess from the owner's own view. */
export function ownerLibraryAudience(library: Pick<ManagedLibrary, 'audience'>, t: Translate = defaultI18n.t): string | undefined {
  if (!library.audience) return undefined;
  return library.audience.everyone ? t('next.ownerLibraries.audienceEveryone') : t('next.ownerLibraries.audience', {count: library.audience.accounts});
}

/** The library's settings document names its own active scan depth; the policy tier is the same value. */
export function ownerLibraryScanDepth(depth: ScanTier | LibrarySettings['scanDepth'], t: Translate = defaultI18n.t): string {
  switch (depth) {
    case 'file_list_only': return t('next.ownerLibraries.scanDepth.fileListOnly');
    case 'basic': return t('next.ownerLibraries.scanDepth.basic');
    case 'complete': return t('next.ownerLibraries.scanDepth.complete');
    default: return t('next.ownerLibraries.scanDepth.custom');
  }
}

export const ownerLibraryIntervals: readonly Readonly<{seconds: number; label: MessageId; values?: Readonly<{count: number}>}>[] = Object.freeze([
  {seconds: 900, label: 'next.ownerLibraries.interval.minutes', values: {count: 15}},
  {seconds: 3600, label: 'next.ownerLibraries.interval.hour'},
  {seconds: 21_600, label: 'next.ownerLibraries.interval.hours', values: {count: 6}},
  {seconds: 86_400, label: 'next.ownerLibraries.interval.day'},
  {seconds: 0, label: 'next.ownerLibraries.interval.never'},
]);

export function ownerLibraryInterval(seconds: number, t: Translate = defaultI18n.t): string {
  const exact = ownerLibraryIntervals.find(value => value.seconds === seconds);
  if (exact) return t(exact.label, exact.values);
  if (seconds === 0) return t('next.ownerLibraries.interval.never');
  if (seconds % 86_400 === 0) return t('next.ownerLibraries.interval.hours', {count: seconds / 3600});
  if (seconds % 3600 === 0) return t('next.ownerLibraries.interval.hours', {count: seconds / 3600});
  return t('next.ownerLibraries.interval.minutes', {count: Math.round(seconds / 60)});
}

/** One folder's availability, in the drawn words: only a ready source is called available. */
export function ownerFolderState(source: InventorySource, t: Translate = defaultI18n.t): string {
  return source.health === 'ready' || source.health === 'healthy' || source.health === 'ok' ? t('next.ownerLibraries.available') : t('next.ownerLibraries.unavailable');
}

/** A folder says whether it lives on this server's disk or on a share, as the frame draws it. */
export function ownerFolderLocation(source: InventorySource, t: Translate = defaultI18n.t): string {
  return t(source.classification === 'network' ? 'web.libraries.networkShare' : 'web.libraries.localDisk');
}

/** Only the files Portico cannot play at all carry a category; warnings stay off this list. */
export function ownerSkippedFiles(issues: readonly ScanIssue[]): readonly ScanIssue[] {
  return issues.filter(issue => issue.category === 'damaged' || issue.category === 'unsupported' || issue.category === 'not-media');
}

export function ownerIssueReason(issue: ScanIssue, t: Translate = defaultI18n.t): string {
  return defaultI18n.has(issue.messageKey) ? t(issue.messageKey as MessageId) : issue.reason;
}

/** The library's own live scan, if one is running now. */
export function ownerLibraryScan(library: ManagedLibrary): LibraryAdminJob | undefined {
  return library.lastScan && (liveJob(library.lastScan.status) || library.lastScan.status === 'paused') ? library.lastScan : undefined;
}
