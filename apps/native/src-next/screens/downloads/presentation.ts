import {originalQuality} from '../../../../../packages/client-core/src/downloads';
import {episodeLabel} from '../../../../../packages/client-core/src/presentation/kit-model/facts';
import {fileDisplayResolutionPart} from '../../../../../packages/client-core/src/presentation/kit-model/technical';
import type {TextEnvironment} from '../../../../../packages/client-core/src/presentation/kit-model';
import {defaultI18n, type MessageId} from '../../../../../packages/i18n/src';
import {groupOfflineEntries, type OfflineEntry, type OfflineEntryFacts, type OfflinePlayabilityReason, type OfflineReadingFacts, type OfflineSnapshot} from '../../platform/offline-downloads';

/** These are catalogue identities, never local English fallbacks. */
export type NativeDownloadsCopy = Readonly<{
  emptyBody: MessageId; storageLimitHelp: MessageId; freeOnPhone: MessageId;
  removeFromPhone: MessageId; stoppedBody: MessageId; downloading: MessageId;
  needsCheckIn: MessageId; goneFromServer: MessageId; otherProfiles: MessageId; stoppedAt: MessageId; stoppedHelp: MessageId;
}>;

/** Receipt ownership is authoritative even before/without a legacy owner field. */
export function downloadEntryIdentity(entry: OfflineEntry): string {
  const receipt = entry.receipt, viewer = receipt?.claims.viewer;
  return JSON.stringify([entry.id, entry.itemId, viewer ? [viewer.authority, viewer.accountId, viewer.profileId, viewer.serverId] : entry.owner,
    receipt?.receiptId, receipt?.claims.artifact.sha256, receipt?.claims.artifact.bytes]);
}

/** Group only the actual local movie entries. Other kinds retain the device's ordering. */
export function downloadGroups(entries: readonly OfflineEntry[]) {
  const groups = groupOfflineEntries(entries), movies = entries.filter(entry => (entry.info?.kind ?? entry.kind) === 'movie'), movieIds = new Set(movies.map(entry => entry.id));
  let placedMovies = false;
  return groups.flatMap(group => {
    if (group.entries.some(entry => movieIds.has(entry.id))) {
      if (placedMovies) return [];
      placedMovies = true;
      return [{key: 'kind:movie', title: defaultI18n.t('library.tab.movies'), entries: movies, episodeCount: undefined}];
    }
    return [{key: group.key, title: group.title, entries: group.entries,
      episodeCount: group.title && group.entries.every(entry => entry.info?.kind === 'episode') ? group.entries.length : undefined}];
  });
}

/** Frame26's bar uses measured allocation on this volume. READY receipt byte totals
 * remain the quota metric in its heading, and never substitute for volume capacity. */
export function downloadStorageSegments(snapshot: Pick<OfflineSnapshot, 'totalBytes' | 'freeBytes' | 'allocatedBytes'>) {
  const {totalBytes: total, freeBytes: free, allocatedBytes: downloads} = snapshot;
  if (total === undefined || downloads === undefined || !Number.isSafeInteger(total) || total <= 0 ||
      !Number.isSafeInteger(free) || free < 0 || free > total || !Number.isSafeInteger(downloads) || downloads < 0 || downloads > total - free) return undefined;
  return {downloads: downloads / total, other: (total - free - downloads) / total};
}

export type DownloadRequestLine = Readonly<{key: 'downloads.requestPreparing' | 'downloads.requestProgress' | 'downloads.requestFailed';
  values?: Readonly<{ready: number; total: number}> | Readonly<{failed: number}>}>;

/** The whole-container request's own progress in today's words: an unknown total
 * is still capturing ("Finding everything to download…"), a known one says how
 * many members are ready, and failed members add their own line. */
export function downloadRequestLines(request: Readonly<{totalKnown: boolean; ready: number; total: number; failed: number}>): readonly DownloadRequestLine[] {
  if (!request.totalKnown) return Object.freeze([{key: 'downloads.requestPreparing' as const}]);
  return Object.freeze([{key: 'downloads.requestProgress' as const, values: Object.freeze({ready: request.ready, total: request.total})},
    ...(request.failed > 0 ? [{key: 'downloads.requestFailed' as const, values: Object.freeze({failed: request.failed})}] : [])]);
}

export function downloadTransferProgress(entry: OfflineEntry): number | undefined {
  return Number.isFinite(entry.bytes) && entry.bytes > 0 && Number.isFinite(entry.bytesDone) && entry.bytesDone >= 0
    ? Math.min(1, entry.bytesDone / entry.bytes) : undefined;
}

/** Cached dimensions describe the actual file. Quality identifiers are never parsed
 * as dimensions, bookmarks never become completion, and legacy subtitle prose is unused. */
export function downloadRowText(entry: OfflineEntry, facts: OfflineEntryFacts | undefined, blocked: OfflinePlayabilityReason | null, copy: NativeDownloadsCopy, env: TextEnvironment,
  reading?: OfflineReadingFacts) {
  const metadata: string[] = [];
  if (entry.info?.kind === 'episode') {
    const label = episodeLabel({mode: 'seasonal', season: entry.info.groupNumber ?? undefined, episode: entry.info.number ?? undefined}, 'ordinary', env);
    if (label) metadata.push(label.text);
  }
  if (entry.state === 'ready' && !blocked && !entry.waiting && entry.onServer) {
    if (reading) {
      // The signed binding names the format; the private Reading record names the
      // place. A missing page total is omitted rather than inferred.
      metadata.push(reading.format.toUpperCase());
      if (Number.isFinite(entry.bytes) && entry.bytes > 0) metadata.push(defaultI18n.bytes(entry.bytes));
      const location = reading.location;
      if (location && location.format === reading.format) {
        if (location.format === 'epub') metadata.push(defaultI18n.t('downloads.percent', {percent: Math.round(location.progression * 100)}));
        else {
          const pageIndex = location.pageIndex;
          // The wire location makes pageIndex optional for non-EPUB formats; a
          // missing index omits the place instead of inventing page 0.
          if (typeof pageIndex === 'number' && Number.isSafeInteger(pageIndex) && pageIndex >= 0) {
            const page = pageIndex + 1;
            if (reading.pageCount === undefined) metadata.push(defaultI18n.t('next.reader.pageLabel', {page}));
            else if (page <= reading.pageCount) metadata.push(defaultI18n.t('next.reader.pageOf', {page, count: reading.pageCount}));
          }
        }
      }
    } else {
      const quality = entry.quality === originalQuality ? env.t('next.kit.quality.original')
        : fileDisplayResolutionPart(facts?.media?.width, facts?.media?.height, env.t)?.text;
      if (quality) metadata.push(quality);
      if (Number.isFinite(entry.bytes) && entry.bytes > 0) metadata.push(defaultI18n.bytes(entry.bytes));
    }
  }
  const blockedKeys = {sign_in: 'downloads.offline.signIn', not_finished: 'downloads.offline.notFinished', needs_setup: 'downloads.offline.needsSetup',
    needs_check_in: copy.needsCheckIn, other_profile: 'downloads.offline.otherProfile'} as const;
  const stopped = entry.state === 'failed';
  const reason = entry.waiting ? defaultI18n.t(entry.waiting === 'wifi' ? 'downloads.waiting.wifi' : 'downloads.waiting.network')
    : blocked ? defaultI18n.t(blockedKeys[blocked]) : !entry.onServer ? defaultI18n.t(copy.goneFromServer)
      : entry.changedOnServer ? defaultI18n.t('downloads.offline.changedOnServer') : stopped ? defaultI18n.t(copy.stoppedBody)
        : entry.state === 'paused' ? defaultI18n.t('downloads.paused') : undefined;
  return {metadata, reason, warning: !!entry.waiting || !!blocked, stopped};
}
