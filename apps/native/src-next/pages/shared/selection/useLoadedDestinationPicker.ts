import {useState, useSyncExternalStore} from 'react';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import type {PickerResult} from '../../../kit/selection';
import type {NativeLoadedDestinations} from '../../../screens/search/loaded-destinations';

/** The playlist/collection picker (select.html frames 20–22) on the bounded destination controller: the same
 * controller, chosen destination and completed prefix stay through a retry, so an uncertain write is never cleared.
 * Rows are only the canonical picker page that is open; nothing is defaulted when the server omits it. Moved as it was. */
export function useLoadedDestinationPicker({controller, kind, open, selectionLabel, count, onOpenChange}: {controller: NativeLoadedDestinations;
  kind: 'playlist' | 'collection'; open: boolean; selectionLabel: string; count: number; onOpenChange: (open: boolean) => void}) {
  useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const [creating, setCreating] = useState(false), [name, setName] = useState('');
  const snapshot = controller.snapshot();
  const page = snapshot.picker && snapshot.picker.kind === kind ? snapshot.picker : undefined;
  const selected = snapshot.chosen?.kind === kind ? snapshot.chosen.id : undefined;
  // One canonical artwork choice per actual artwork entry: two URLs on one member are not two mosaic cells.
  const rows = (page?.rows ?? []).map(row => ({id: row.id, title: row.name, size: row.count, canAdd: row.canAdd,
    ...(row.alreadyIn === undefined ? {} : {alreadyIn: row.alreadyIn}),
    paths: row.artwork.map(art => art.posterUrl ?? art.backdropUrl).filter((path): path is string => !!path)}));
  const outcome = snapshot.collectionOutcome;
  const destination = kind === 'playlist'
    ? {id: selected ?? '', title: snapshot.playlist.playlist?.name ?? '', size: snapshot.playlist.playlist?.entryCount}
    : {id: selected ?? '', title: snapshot.collection.resource?.name ?? '', size: snapshot.collection.resource?.entryCount};
  // Only known titles and their coded reason are shown; an unresolved id is never printed. The completed prefix and the
  // original receipts survive a retry.
  const coded = (code: string) => presentError({code}, 'detail', {operation: 'action'}).body;
  const result: PickerResult | undefined = !selected ? undefined : kind === 'playlist'
    ? {destination, processed: snapshot.completed + (snapshot.playlistFailure ? 1 : 0), inserted: snapshot.completed, total: count,
        failed: snapshot.playlistFailure ? 1 : 0, complete: snapshot.completed >= count || !!snapshot.playlistFailure,
        failures: snapshot.playlistFailure ? [{id: snapshot.playlistFailure.id, title: snapshot.playlistFailure.title, reason: coded(snapshot.playlistFailure.code)}] : []}
    : snapshot.collectionReceipts.length
      ? {destination, processed: outcome.added.length + outcome.unchanged.length + outcome.failed.length, inserted: outcome.added.length,
          alreadyIn: outcome.unchanged.length, failed: outcome.failed.length, total: count, complete: true,
          failures: outcome.failed.map(failure => ({id: failure.id, title: failure.title, reason: coded(failure.code)}))}
      : {destination, total: count, failures: [], complete: false};
  const props = {
    open, onOpenChange, count, selectionLabel, query: '', onQueryChange: () => {}, showFind: false,
    creating, onCreatingChange: setCreating, name, onNameChange: setName,
    onCreateAndAdd: (value: string) => {void controller.create(value).catch(() => {});},
    onAdd: (id: string) => {if (!page) return; void controller.choose(kind, id, page.token).catch(() => {});},
    onRetry: () => {void controller.retry().catch(() => {});},
    ...(snapshot.pickerCanRetry && snapshot.pickerErrorToken ? {onRetryError: () => {void controller.retryPicker(snapshot.pickerErrorToken!).catch(() => {});}} : {}),
    hasNext: !!page?.nextCursor, hasPrevious: snapshot.pickerPrevious,
    onNext: () => {if (page) void controller.next(kind, page.token).catch(() => {});},
    onPrevious: () => {if (page) void controller.previous(kind, page.token).catch(() => {});},
    loading: !page && snapshot.busy, error: snapshot.pickerError, pending: snapshot.busy, result, testID: `search-destination-${kind}`,
    onViewDestination: () => onOpenChange(false), onResultDismiss: () => onOpenChange(false),
  };
  return {rows, props};
}
