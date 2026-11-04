import React, {useSyncExternalStore} from 'react';
import {Text} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {DetailService} from '../../../../../packages/client-core/src/detail';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';
import {refreshMetadataPath} from '../../../../../packages/client-core/src/presentation/entry-actions';
import type {SelectionKind} from '../../../../../packages/client-core/src/selection-model';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeShellRuntime} from '../../shell/runtime';
import {selectedItemsLabel} from '../../shell/selection-display';
import {Artwork} from '../../kit/artwork';
import {BulkEditor, DeleteMediaDialog, RatingDialog} from '../../kit/selection';
import {useTheme} from '../../kit/theme';
import {NativeLoadedDestinations} from '../search/loaded-destinations';
import {NativeDestinationPicker} from '../destinations/DestinationPicker';
import {NativeBulkMedia} from '../destinations/bulk-media';
import {nativeDestinationLifetime} from '../destinations/lifetime';
import {nativeScreenServices} from '../native-screen-services';
import {askNativeDownload} from '../downloads/request';
import {nativeDownloadRequest} from './download-request';
import {viewerQueriesFor} from '../../shell/freshness';

const t = defaultI18n.t;
const leaves = ['movie', 'episode', 'song', 'audiobook_file'];
/** The page a title action was chosen on: it ends the action when the menu's opening ends. */
export type NativeTitleActionLifetime = Readonly<{signal: AbortSignal; isCurrent: () => boolean}>;
/** Whether a title action can be offered for this entry; the menu asks before drawing a row. */
export type NativeTitleActionsOffer = Readonly<{rate: boolean; collection: boolean; edit: boolean; remove: boolean; refresh: boolean; download: boolean}>;

type Rating = Readonly<{phase: 'loading' | 'ready' | 'saving' | 'result' | 'uncertain' | 'error'; value: number | null | undefined; error?: string}>;
type Open = Readonly<{entry: ContentEntry; lifetime: NativeTitleActionLifetime; stop: () => void; abort: () => void}> & (
  | Readonly<{kind: 'rate'; detail: DetailService; rating: Rating}>
  | Readonly<{kind: 'collection'; destinations: NativeLoadedDestinations}>
  | Readonly<{kind: 'edit' | 'delete'; bulk: NativeBulkMedia}>);

export type NativeTitleActionsHandle = Readonly<{
  /** Whether the viewer's device and role can run these on a title; the Detail grant is the caller's. */
  offer: (entry: ContentEntry) => NativeTitleActionsOffer;
  rate: (entry: ContentEntry, lifetime: NativeTitleActionLifetime) => void;
  addToCollection: (entry: ContentEntry, lifetime: NativeTitleActionLifetime) => void;
  editMetadata: (entry: ContentEntry, lifetime: NativeTitleActionLifetime) => void;
  deleteTitle: (entry: ContentEntry, lifetime: NativeTitleActionLifetime) => void;
  refreshMetadata: (entry: ContentEntry, lifetime: NativeTitleActionLifetime) => Promise<void>;
  download: (entry: ContentEntry, lifetime: NativeTitleActionLifetime) => void;
  subscribe: (listener: () => void) => () => void;
  getSnapshot: () => number;
  render: (runtime: NativeShellRuntime) => React.ReactNode;
  dispose: () => void;
}>;

const unavailable = () => Object.assign(new Error('native_title_action_unavailable'), {code: 'unavailable'});

/** The single-title actions the shared menu offers beyond Home's own controller: Rate…, Add to
 * collection…, Edit metadata…, Delete…, Refresh metadata and Download…. Each runs on the machinery
 * the selection bars already use for explicit titles (the destination picker, the bulk editor and
 * delete dialog, the Detail rating write), fenced to the menu's opening and the viewer's authority,
 * and re-reads the title's own Detail grant before writing. One action is open at a time. */
export function createNativeTitleActions(context: NativeShellBootstrapContext): NativeTitleActionsHandle {
  const lifetimeOf = nativeDestinationLifetime(context), services = nativeScreenServices(context);
  const listeners = new Set<() => void>();
  let revision = 0, open: Open | undefined, disposed = false;
  const emit = () => {revision++; for (const listener of [...listeners]) listener();};
  const owner = () => context.session()?.viewer.role === 'owner';
  const live = (lifetime: NativeTitleActionLifetime) => !disposed && lifetimeOf.current() && !lifetime.signal.aborted && lifetime.isCurrent();
  const close = (expected?: Open) => {
    if (!open || expected && open !== expected) return;
    const closing = open; open = undefined; closing.stop(); closing.abort();
    if (closing.kind === 'rate') closing.detail.dispose();
    else if (closing.kind === 'collection') closing.destinations.dispose();
    else closing.bulk.dispose();
    emit();
  };
  /** A later menu, a route change or a new authority ends whichever action is open. */
  const check = () => {if (open && !live(open.lifetime)) close(open);};
  const offs = [context.navigation.subscribe(check), context.subscribeAuthority(check)];
  const begin = (entry: ContentEntry, lifetime: NativeTitleActionLifetime, build: (api: Pick<Open, 'entry' | 'lifetime'>) => Omit<Open, 'entry' | 'lifetime' | 'stop' | 'abort'> | undefined, request?: AbortController) => {
    if (!live(lifetime) || !entry.libraryId) {request?.abort(); return;}
    close();
    const built = build({entry, lifetime});
    if (!built) {request?.abort(); return;}
    const abort = () => {if (open === next) close(next);};
    lifetime.signal.addEventListener('abort', abort, {once: true});
    const next = Object.freeze({...built, entry, lifetime, stop: () => lifetime.signal.removeEventListener('abort', abort), abort: () => request?.abort()}) as Open;
    open = next; emit();
    return next;
  };
  const report = (error: unknown, expected: Open) => {if (open === expected && live(expected.lifetime)) services.reportError(error, 'detail');};
  const optionsFor = (lifetime: NativeTitleActionLifetime) => {
    // The caller's opening plus this host's own: closing the action retires every request under it.
    const controller = new AbortController();
    lifetime.signal.addEventListener('abort', () => controller.abort(), {once: true});
    return {controller, options: {queries: viewerQueriesFor(context.api, lifetimeOf.scope), api: context.api, scope: lifetimeOf.scope, requestId: services.requestId, signal: controller.signal, isCurrent: () => !controller.signal.aborted && live(lifetime)}};
  };
  const patchRating = (expected: Open, change: Partial<Rating>) => {
    if (open !== expected || expected.kind !== 'rate') return;
    const next = Object.freeze({...expected, rating: Object.freeze({...expected.rating, ...change})}) as Open;
    open = next; emit();
    return next;
  };
  const ratingOutcome = (expected: Open, detail: DetailService) => {
    const snapshot = detail.getSnapshot();
    if (snapshot.pending.length || snapshot.mutationError) {
      patchRating(expected, {phase: 'uncertain', error: presentError({code: snapshot.mutationError?.code ?? 'rating-unconfirmed'}, 'detail', {operation: 'action'}).body});
    } else patchRating(expected, {phase: 'result', value: snapshot.data?.personal.rating ?? null, error: undefined});
  };
  const handle: NativeTitleActionsHandle = {
    offer: entry => {
      const viewer = context.session()?.viewer, leaf = leaves.includes(entry.kind) && entry.available !== false;
      const isOwner = viewer?.role === 'owner';
      return Object.freeze({rate: !!entry.libraryId, collection: leaf && !!entry.libraryId, edit: isOwner && leaf && !!entry.libraryId, remove: isOwner && leaf && !!entry.libraryId,
        refresh: isOwner && refreshMetadataPath(entry.kind, entry.id) !== undefined, download: nativeDownloadRequest(entry) !== undefined});
    },
    rate: (entry, lifetime) => {
      const started = begin(entry, lifetime, () => ({kind: 'rate', rating: {phase: 'loading', value: undefined},
        detail: new DetailService({queries: viewerQueriesFor(context.api, lifetimeOf.scope), api: context.api, scope: lifetimeOf.scope, requestId: services.requestId})}));
      if (!started || started.kind !== 'rate') return;
      const detail = started.detail;
      void (async () => {
        try {
          // Rate needs no related rows: ask as the title page does (a film's page holds Detail without them), so it shares that read.
          await detail.select({libraryId: entry.libraryId!, itemId: entry.id}, {related: entry.kind === 'movie' ? 'none' : 'all'});
          if (open !== started || !live(lifetime)) return;
          const state = detail.getSnapshot(), data = state.data;
          // The detail is matched to the title by server, library, item and kind, and must still grant rating.
          if (state.phase !== 'ready' || !data || data.scope.libraryId !== entry.libraryId || data.scope.itemId !== entry.id || data.item.kind !== entry.kind
            || !data.actions.some(action => action.id === 'rating' && action.enabled)) throw Object.assign(new Error('rating-not-offered'), {code: state.error?.code ?? 'rating-not-offered'});
          patchRating(started, {phase: 'ready', value: data.personal.rating, error: undefined});
        } catch (error) {
          if (open === started && live(lifetime)) patchRating(started, {phase: 'error', value: undefined,
            error: presentError(error as {code: string}, 'detail', {operation: 'action'}).body});
        }
      })();
    },
    addToCollection: (entry, lifetime) => {
      const request = optionsFor(lifetime);
      const started = begin(entry, lifetime, () => {
        try {
          return {kind: 'collection', destinations: new NativeLoadedDestinations({...request.options, viewerFence: context.authority().viewerFence}, [entry])};
        } catch (error) {services.reportError(error, 'detail'); return undefined;}
      }, request.controller);
      if (!started || started.kind !== 'collection') return;
      started.destinations.subscribe(emit);
      void started.destinations.load('collection').catch(error => report(error, started));
    },
    editMetadata: (entry, lifetime) => bulk('edit', entry, lifetime),
    deleteTitle: (entry, lifetime) => bulk('delete', entry, lifetime),
    refreshMetadata: async (entry, lifetime) => {
      const path = refreshMetadataPath(entry.kind, entry.id);
      if (!path || !owner() || !live(lifetime)) return;
      // The same call the web title page makes. Nothing is written once the opening has ended.
      await context.api.request(path, 'POST', {}, lifetime.signal);
      if (live(lifetime)) services.notice(t('next.selection.saved'));
    },
    download: (entry, lifetime) => {
      const request = nativeDownloadRequest(entry);
      if (request && live(lifetime)) askNativeDownload({...request, lifetime});
    },
    subscribe: listener => {listeners.add(listener); return () => {listeners.delete(listener);};},
    getSnapshot: () => revision,
    render: runtime => <NativeTitleActionSurfaces handle={handle} runtime={runtime} view={() => open}
      rate={{onRate: value => {
        const current = open;
        if (!current || current.kind !== 'rate' || current.rating.phase !== 'ready' || !live(current.lifetime)) return;
        const saving = patchRating(current, {phase: 'saving', error: undefined});
        if (!saving || saving.kind !== 'rate') return;
        void saving.detail.mutate({action: 'rating', value}).then(() => {if (open === saving) ratingOutcome(saving, saving.detail);})
          .catch(error => {if (open === saving && live(saving.lifetime)) patchRating(saving, {phase: 'uncertain', error: presentError(error, 'detail', {operation: 'action'}).body});});
      }, onRetry: () => {
        const current = open;
        if (!current || current.kind !== 'rate' || current.rating.phase !== 'uncertain' || !live(current.lifetime)) return;
        const saving = patchRating(current, {phase: 'saving', error: undefined});
        if (!saving || saving.kind !== 'rate') return;
        void saving.detail.retryMutation().then(() => {if (open === saving) ratingOutcome(saving, saving.detail);})
          .catch(error => {if (open === saving && live(saving.lifetime)) patchRating(saving, {phase: 'uncertain', error: presentError(error, 'detail', {operation: 'action'}).body});});
      }}} close={close}/>,
    dispose: () => {if (disposed) return; close(); disposed = true; for (const off of offs) off(); listeners.clear();},
  };
  function bulk(kind: 'edit' | 'delete', entry: ContentEntry, lifetime: NativeTitleActionLifetime) {
    const request = optionsFor(lifetime);
    const started = begin(entry, lifetime, () => {
      if (!owner()) {services.reportError(unavailable(), 'detail'); return undefined;}
      try {return {kind, bulk: new NativeBulkMedia(request.options, [entry])};}
      catch (error) {services.reportError(error, 'detail'); return undefined;}
    }, request.controller);
    if (!started || started.kind === 'rate' || started.kind === 'collection') return;
    started.bulk.subscribe(emit);
    void (kind === 'edit' ? started.bulk.openEditor() : started.bulk.openDelete()).catch(error => report(error, started));
  }
  return Object.freeze(handle);
}

function NativeTitleActionSurfaces({handle, runtime, view, rate, close}: {handle: NativeTitleActionsHandle; runtime: NativeShellRuntime; view: () => Open | undefined;
  rate: Readonly<{onRate: (value: number | null) => void; onRetry: () => void}>; close: (expected?: Open) => void}) {
  useSyncExternalStore(handle.subscribe, handle.getSnapshot);
  const theme = useTheme(), current = view();
  if (!current) return null;
  const entry = current.entry, title = entry.displayTitle ?? entry.title;
  const label = selectedItemsLabel({count: 1, types: [{kind: entry.kind as SelectionKind, count: 1}]});
  const artwork = (path?: string) => {const source = path ? runtime.artwork(path) : undefined; return source ? <Artwork source={source} width={48} height={48}/> : undefined;};
  const header = <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{label}</Text>;
  if (current.kind === 'rate') return <RatingDialog title={title} value={current.rating.value} phase={current.rating.phase} error={current.rating.error}
    onRate={rate.onRate} onRetry={rate.onRetry} onClose={() => close(current)}/>;
  if (current.kind === 'collection') return <NativeDestinationPicker controller={current.destinations} kind="collection" open selectionLabel={label} count={1} artwork={artwork}
    onOpenChange={open => {if (!open && !current.destinations.snapshot().busy) close(current);}}/>;
  const state = current.bulk.getSnapshot();
  if (current.kind === 'delete' && state.deletion) return <DeleteMediaDialog open owner ownerHeader={header} selectionCount={1} preview={state.deletion.preview ?? null}
    loading={state.deletion.phase === 'loading'} error={state.deletion.error} result={state.deletion.result} confirmation={state.deletion.confirmation}
    onConfirmationChange={value => current.bulk.setDeletion({confirmation: value})} removeFiles={state.deletion.removeFiles}
    onRemoveFilesChange={value => current.bulk.setDeletion({removeFiles: value})} onDelete={() => {void current.bulk.deleteNow();}}
    pending={state.deletion.phase === 'saving'} renderArtwork={() => undefined} onOpenChange={open => {if (!open) close(current);}} testID="watch-title-delete-media"/>;
  if (current.kind === 'edit') {
    const summary: readonly {id: string; text: string}[] = Object.entries(state.changes).flatMap(([field, change]) => {
      const spec = state.preview?.schema.find(candidate => candidate.field === field);
      if (!spec) return [];
      if ('value' in change) return [{id: field, text: `${spec.label}: ${change.value || t('next.selection.native.notSet')}`}];
      const parts: string[] = [];
      if (change.add.length) parts.push(`+${defaultI18n.list(change.add)}`);
      if (change.remove.length) parts.push(`−${defaultI18n.list(change.remove)}`);
      return parts.length ? [{id: field, text: `${spec.label}: ${parts.join(' · ')}`}] : [];
    });
    return <BulkEditor open title={t('next.selection.native.action.edit')} count={1} saveLabel={t('action.save')} schema={state.preview?.schema ?? []} values={state.values}
      changes={state.changes} onChangesChange={current.bulk.setChanges} lockEdited={state.lockEdited} onLockEditedChange={current.bulk.setLockEdited}
      onSave={() => {void current.bulk.save();}} pending={state.phase === 'saving'} error={state.error} result={state.result}
      resultEntries={[{id: entry.id, title, artwork: artwork(entry.posterUrl)}]} onRetry={ids => {void current.bulk.retry(ids);}} singleItemFields={state.singleItemFields}
      fieldLabel={field => field.label} summary={summary} ownerHeader={header} onOpenChange={open => {if (!open) close(current);}} testID="watch-title-bulk-editor"/>;
  }
  return null;
}
