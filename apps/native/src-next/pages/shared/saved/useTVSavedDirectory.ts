import React from 'react';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {Library} from '../../../../../../packages/client-core/src/index';
import type {PersonalResource} from '../../../../../../packages/client-core/src/personal-saved';
import type {PlaylistCard, PlaylistListFilter} from '../../../../../../packages/client-core/src/saved';
import {useFocusRegistry, type FocusEdges} from '../../../kit/focus';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeSavedViewDestination} from '../../../screens/saved/view-model';
import type {NativeDestinationActivationPort} from '../../../screens/destinations/focus';
import {savedCollectionCount, savedViewCount} from '../../../screens/saved/presentation';

const t = defaultI18n.t;

export type SavedTVDirectoryHandle = Readonly<{scrollTop: () => void; atTop: () => boolean}>;
export type NativeSavedTVDirectoryView = 'playlists' | 'collections' | 'views';

/** The TV landing's three directory bodies (collections.html 15–17). The TV
 * landing owner renders the Saved tabs above this; it registers no route and owns
 * no destination. */
export type NativeSavedTVDirectoryProps = Readonly<{
  view: NativeSavedTVDirectoryView;
  destination: NativeSavedViewDestination;
  runtime: NativeShellRuntime;
  libraries: readonly Library[];
  enabled: boolean;
  activation: NativeDestinationActivationPort;
  regionId: string;
  edges?: FocusEdges;
  pageRef?: React.Ref<SavedTVDirectoryHandle>;
  select: (route: Readonly<{view: NativeSavedTVDirectoryView; sort?: string; direction?: 'asc' | 'desc'; filter?: PlaylistListFilter}>) => void;
  openPlaylist: (card: PlaylistCard) => void;
  openResource: (resource: PersonalResource) => void;
}>;

export function useTVSavedDirectory({view, destination, runtime, enabled, activation, regionId, edges, select}: NativeSavedTVDirectoryProps) {
  const registry = useFocusRegistry();
  const [creating, setCreating] = React.useState(false), [busy, setBusy] = React.useState(false), [problem, setProblem] = React.useState<string>();
  const [draft, setDraft] = React.useState<{name: string; summary: string; visibility: 'private' | 'server'}>();
  const mounted = React.useRef(true), flight = React.useRef<object | undefined>(undefined), creation = React.useRef<{scope: object; revision: number} | undefined>(undefined);
  const scope = React.useMemo(() => ({view, destination}), [view, destination]), currentScope = React.useRef(scope); currentScope.current = scope;
  const snapshot = React.useSyncExternalStore(destination.subscribe, destination.getSnapshot);
  const saved = snapshot.saved, personal = snapshot.personal;
  const playlists = view === 'playlists' ? (saved?.projection?.sections ?? []).flatMap(section => section.entries.filter((entry): entry is PlaylistCard => entry.kind === 'playlist')) : [];
  const resources = view === 'playlists' ? [] : personal?.resources ?? [];
  const filters = view === 'playlists' ? saved?.projection?.filters ?? [] : [];
  const sorts = view === 'playlists' ? saved?.projection?.sorts ?? [] : [];
  const error = snapshot.error ?? saved?.error ?? personal?.error, loading = snapshot.phase === 'loading' && !saved?.projection && !personal?.resources.length;
  const count = view === 'playlists' ? (saved?.projection?.sections[0]?.totalCount ?? playlists.length) : resources.length;
  const countText = view === 'playlists' ? t('next.saved.playlistCount', {count}) : view === 'collections' ? savedCollectionCount(count) : savedViewCount(count);
  const gridId = `${regionId}.cards`, toolbarId = `${regionId}.toolbar`, moreId = `${regionId}.more`;
  const createEnabled = enabled && snapshot.phase === 'ready' && !error && !loading && (view === 'playlists' ? saved?.projection?.actions?.includes('create_playlist') === true : view === 'collections');
  const interactive = enabled && !creating;
  const createId = `saved-tv-new-${view}`;
  const restoreTarget = React.useRef<{scope: object; revision: number; childId: string} | undefined>(undefined);
  const returnFocusRef = React.useMemo(() => ({current: {requestTVFocus: () => {const target = restoreTarget.current;
    if (target && mounted.current && currentScope.current === target.scope && activation.getSnapshot().visible && activation.getSnapshot().revision === target.revision)
      registry.focus({regionId: gridId, childId: target.childId});}}}), [activation, gridId, registry]);
  const hasToolbar = view === 'playlists' && !!sorts.length;
  const hasMore = !!(view === 'playlists' ? saved?.pagination.next.length : personal?.nextCursor);
  const entries = view === 'playlists' ? playlists : resources;
  const firstId = createEnabled ? createId : entries[0] ? tileId(view, entries[0].id) : undefined;
  const start = edges?.up ? {regionId: edges.up, childId: view} : firstId ? {regionId: gridId, childId: firstId} : undefined;
  const current = React.useCallback((target: NonNullable<typeof creation.current>) => mounted.current && currentScope.current === target.scope
    && activation.getSnapshot().visible && activation.getSnapshot().revision === target.revision, [activation]);
  React.useLayoutEffect(() => {mounted.current = true; return () => {mounted.current = false; creation.current = undefined; flight.current = undefined;};}, []);
  React.useLayoutEffect(() => {
    const retire = () => {const target = creation.current; if (target && !current(target)) {creation.current = undefined; flight.current = undefined; setCreating(false); setBusy(false);}};
    retire(); return activation.subscribe(retire);
  }, [activation, scope, current]);
  const renderedCreation = creation.current, renderedActivation = activation.getSnapshot();
  const openCreate = () => {
    if (!createEnabled || currentScope.current !== scope || !activation.getSnapshot().visible
      || activation.getSnapshot().revision !== renderedActivation.revision || flight.current) return;
    creation.current = {scope, revision: activation.getSnapshot().revision}; restoreTarget.current = {...creation.current, childId: createId}; setCreating(true);
    if (!personal?.retryPending) {setDraft(undefined); setProblem(undefined);}
  };
  const closeCreate = () => {if (!flight.current && creation.current === renderedCreation) {creation.current = undefined; setCreating(false);}};
  const create = (input: {name: string; summary: string; visibility: 'private' | 'server'}) => {
    const target = renderedCreation;
    if (!target || creation.current !== target || !current(target) || flight.current) return;
    const live = destination.getSnapshot();
    if (live.error ?? live.saved?.error ?? live.personal?.error) return;
    if (view === 'playlists' && !live.saved?.projection?.actions?.includes('create_playlist')) return;
    const ticket = {}; flight.current = ticket; setBusy(true); setProblem(undefined); setDraft(input);
    const held = () => flight.current === ticket && creation.current === target && current(target);
    const work = async () => {
      if (view === 'playlists') await destination.mutateSaved({action: 'create', name: input.name, ...(input.summary ? {summary: input.summary} : {})});
      else {
        const ok = live.personal?.retryPending ? await destination.retryPersonalMutation()
          : await destination.mutatePersonal({action: 'create', kind: 'collection', name: input.name, summary: input.summary, visibility: input.visibility});
        if (!ok) throw destination.getSnapshot().personal?.mutationError ?? Object.assign(new Error('unavailable'), {code: 'unavailable'});
      }
      if (!held()) return;
      const updated = destination.getSnapshot(), newId = view === 'playlists' ? updated.saved?.lastReceipt?.playlistId : updated.personal?.result?.resourceId;
      creation.current = undefined; setCreating(false); setDraft(undefined);
      // The server's directory sort/filter may keep a new item off this page. Never guess an ID.
      const arrived = view === 'playlists' ? updated.saved?.projection?.sections.some(section => section.entries.some(entry => entry.kind === 'playlist' && entry.id === newId))
        : updated.personal?.resources.some(resource => resource.id === newId);
      restoreTarget.current = {...target, childId: arrived && newId ? tileId(view, newId) : createId};
    };
    void work().catch(failure => {if (held()) setProblem(presentError(failure, 'saved', {operation: 'save'}).body);})
      .finally(() => {if (flight.current === ticket) {flight.current = undefined; if (current(target)) setBusy(false);}});
  };
  const next = () => {
    if (interactive && currentScope.current === scope && activation.getSnapshot().visible) {
      void destination.next(view === 'playlists' ? saved?.pagination.next[0]?.sectionId : undefined).catch(runtime.reportError);
    }
  };
  return {saved, personal, playlists, resources, filters, sorts, error, loading, count, countText, gridId, toolbarId, moreId,
    createEnabled, interactive, createId, returnFocusRef, hasToolbar, hasMore, entries, firstId, start,
    creating, busy, problem, draft, openCreate, closeCreate, create, next,
    scrollCurrent: () => currentScope.current === scope,
    retry: () => {void destination.retry().catch(runtime.reportError);},
    choose: ({sort, direction, filter}: {sort?: string; direction?: 'asc' | 'desc'; filter?: PlaylistListFilter}) => select({
      view: 'playlists', ...(sort !== undefined ? {sort} : {}), ...(direction !== undefined ? {direction} : {}),
      ...(filter !== undefined ? {filter} : {}),
    }),
  };
}

export function tileId(view: NativeSavedTVDirectoryView, id: string) {return `saved-tv-${view}-${id}`;}
