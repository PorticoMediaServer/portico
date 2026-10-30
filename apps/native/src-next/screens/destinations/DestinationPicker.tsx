import React, {useState, useSyncExternalStore} from 'react';
import {Platform, View} from 'react-native';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';
import {CollectionPicker, PlaylistPicker, type PickerEntry, type PickerResult} from '../../kit/selection';
import type {NativeLoadedDestinations} from '../search/loaded-destinations';

/** The drawn playlist/collection picker (select.html frames 20–22), mounted on the
 * bounded destination controller: the same controller, chosen destination and
 * completed prefix stay through a retry, so an uncertain write is never cleared. */
export type NativeDestinationPickerProps = Readonly<{
  controller: NativeLoadedDestinations;
  kind: 'playlist' | 'collection';
  open: boolean;
  selectionLabel: string;
  count: number;
  artwork: (path?: string) => React.ReactNode;
  onOpenChange: (open: boolean) => void;
}>;

export function NativeDestinationPicker({controller, kind, open, selectionLabel, count, artwork, onOpenChange}: NativeDestinationPickerProps) {
  useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const [creating, setCreating] = useState(false), [name, setName] = useState('');
  const snapshot = controller.snapshot();
  // Rows are only the canonical Home picker page that is open: its own count, grant,
  // already-in fact, revision and artwork. Nothing is defaulted when the server omits it.
  const page = snapshot.picker && snapshot.picker.kind === kind ? snapshot.picker : undefined;
  const selected = snapshot.chosen?.kind === kind ? snapshot.chosen.id : undefined;
  // select.html 18: a destination row carries its own 2 by 2 mosaic when the server
  // published more than one cover. One path keeps the existing single artwork.
  const mosaic = (paths: readonly (string | undefined)[]) => {
    const sources = paths.filter((path): path is string => !!path).slice(0, 4);
    const first = artwork(sources[0]);
    // The drawn 2 by 2 cells are the phone/tablet picker's 48 px frame; TV keeps its own
    // 76 px single artwork until its cell geometry has its own proof. No artwork at all
    // (a caller that draws none) keeps today's empty cell.
    if (sources.length < 2 || Platform.isTV || first == null) return first;
    return <View style={{flex: 1, flexDirection: 'row', flexWrap: 'wrap'}}>
      {Array.from({length: 4}, (_, index) => <View key={index} style={{width: '50%', height: '50%', alignItems: 'center', justifyContent: 'center', overflow: 'hidden'}}>
        {sources[index] ? <View style={{transform: [{scale: 0.5}]}}>{artwork(sources[index])}</View> : null}
      </View>)}
    </View>;
  };
  const entries: PickerEntry[] = (page?.rows ?? []).map(row => {
    // One canonical artwork choice per actual artwork entry: two URLs on one member are
    // not two mosaic cells.
    const paths = row.artwork.map(art => art.posterUrl ?? art.backdropUrl).filter((path): path is string => !!path);
    return {id: row.id, title: row.name, size: row.count, canAdd: row.canAdd,
      ...(row.alreadyIn === undefined ? {} : {alreadyIn: row.alreadyIn}), artwork: mosaic(paths)};
  });
  const outcome = snapshot.collectionOutcome;
  const destination = kind === 'playlist'
    ? {id: selected ?? '', title: snapshot.playlist.playlist?.name ?? '', size: snapshot.playlist.playlist?.entryCount}
    : {id: selected ?? '', title: snapshot.collection.resource?.name ?? '', size: snapshot.collection.resource?.entryCount};
  // Only known titles and their coded reason are shown; an unresolved id is never
  // printed. The completed prefix and the original receipts survive a retry.
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
  const loading = !page && snapshot.busy;
  const error = snapshot.pickerError;
  const props = {
    open, onOpenChange, count, selectionLabel, entries, query: '', onQueryChange: () => {}, showFind: false,
    creating, onCreatingChange: setCreating, name, onNameChange: setName,
    onCreateAndAdd: (value: string) => {void controller.create(value).catch(() => {});},
    onAdd: (id: string) => {if (!page) return; void controller.choose(kind, id, page.token).catch(() => {});},
    onRetry: () => {void controller.retry().catch(() => {});},
    ...(snapshot.pickerCanRetry && snapshot.pickerErrorToken ? {onRetryError: () => {void controller.retryPicker(snapshot.pickerErrorToken!).catch(() => {});}} : {}),
    hasNext: !!page?.nextCursor,
    hasPrevious: snapshot.pickerPrevious,
    onNext: () => {if (page) void controller.next(kind, page.token).catch(() => {});},
    onPrevious: () => {if (page) void controller.previous(kind, page.token).catch(() => {});},
    loading, error, pending: snapshot.busy, result, testID: `search-destination-${kind}`,
    onViewDestination: () => onOpenChange(false), onResultDismiss: () => onOpenChange(false),
  };
  return kind === 'playlist' ? <PlaylistPicker {...props}/> : <CollectionPicker {...props}/>;
}
