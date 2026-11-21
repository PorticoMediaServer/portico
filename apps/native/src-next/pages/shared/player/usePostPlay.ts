import {useEffect, useMemo, useSyncExternalStore} from 'react';
import {DetailService, type DetailApi, type DetailScope, type PersonalIntent} from '../../../../../../packages/client-core/src/detail';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {ArtworkSource} from '../../../kit/artwork';
import {viewerQueriesFor} from '../../../shell/freshness';

/** tv-player 7: a film with nothing queued ends on its own backdrop, with Replay
 * focused, a rating and the server's related titles. The episode cards stay as
 * they are; this screen is the end of a title the queue has nothing after. It reads
 * the title and its related rows; the kit's `PlayerEndCard` draws them. */
export type NativeWatchPostPlayProps = Readonly<{
  entry: ContentEntry;
  api: DetailApi;
  scope: DetailScope;
  requestId: () => Promise<string>;
  current: () => boolean;
  artwork: (path?: string) => ArtworkSource | undefined;
  onReplay: () => void;
  onClose: () => void;
  onOpen: (entry: ContentEntry) => void;
  reportError: (error: unknown) => void;
  /** Phone geometry (phone-player 6): facts left, 85-wide cards right. */
  compact?: boolean;
}>;

export function usePostPlay({entry, api, scope, requestId, current, artwork, onReplay, onClose, onOpen, reportError, compact = false}: NativeWatchPostPlayProps) {
  const t = defaultI18n.t;
  const service = useMemo(() => new DetailService({queries: viewerQueriesFor(api, scope), api, scope, requestId}), [api, scope, requestId]);
  const snapshot = useSyncExternalStore(service.subscribe, service.getSnapshot);
  useEffect(() => {
    if (entry.libraryId) void service.select({libraryId: entry.libraryId, itemId: entry.id}).catch(error => {if (current()) reportError(error);});
    return () => service.dispose();
  }, [service, entry.libraryId, entry.id, current, reportError]);
  const detail = snapshot.data && snapshot.data.item.id === entry.id ? snapshot.data : undefined;
  const rating = detail?.personal.rating ?? null;
  const stars = rating === null ? 0 : Math.round(rating / 2);
  const rate = (value: number | null) => {
    if (!current() || !detail) return;
    const intent: PersonalIntent = {action: 'rating', value};
    void service.mutate(intent).catch(error => {if (current()) reportError(error);});
  };
  const related = detail?.related?.rows.flatMap(row => row.entries).filter((row, index, all) => all.findIndex(other => other.id === row.id) === index).slice(0, 12) ?? [];
  const hours = entry.duration && entry.duration >= 3600 ? Math.floor(entry.duration / 3600) : undefined;
  const minutes = entry.duration ? Math.round(entry.duration % 3600 / 60) : undefined;
  const runtime = entry.duration ? hours ? t('next.kit.duration.hoursMinutes', {hours, minutes}) : t('next.kit.duration.minutes', {minutes: Math.round(entry.duration / 60)}) : undefined;
  return {title: entry.displayTitle ?? entry.title, year: entry.year, runtime, backdrop: artwork(entry.backdropUrl), compact, onReplay, onClose,
    rating: detail ? {stars, onRate: (star: number) => rate(star === stars ? null : star * 2)} : undefined,
    related: related.map(row => ({id: row.id, title: row.title, year: row.year, poster: artwork(row.posterUrl), onPress: () => {if (current()) onOpen(row);}}))};
}
