import {useLayoutEffect, useRef, useState} from 'react';
import type {ContentEntry, ContentProjection, LibraryContentService, LibraryContentSnapshot} from '@core/library-content.ts';
import type {BrowseSelectionScope} from '@core/selection-model/index.ts';
import type {WebShellPlaybackRuntime} from '../../platform/playback/runtime';
import type {ScreenContext} from '../detail/context';
import {createCatalogueDocumentSelection, type CatalogueDocumentSelection, type CatalogueDocumentSource} from './document-selection';

/** Merged shelves in the base projection's order. An appended page replaces that
 * section by id; a section only the appended read knows keeps its own place after
 * the base ones. */
export function discoverySections(snapshot: LibraryContentSnapshot) {
  const base = snapshot.projection?.sections ?? [], replacement = new Map(snapshot.appended.map(section => [section.id, section]));
  const merged = base.map(section => replacement.get(section.id) ?? section);
  for (const section of snapshot.appended) if (!base.some(known => known.id === section.id)) merged.push(section);
  return merged;
}
/** The current row object for each logical title, in display order. */
export function discoveryEntries(snapshot: LibraryContentSnapshot): readonly ContentEntry[] {
  const entries: ContentEntry[] = [], seen = new Set<string>();
  for (const section of discoverySections(snapshot)) for (const entry of section.entries) {
    const key = JSON.stringify([entry.libraryId ?? '', entry.kind, entry.id]);
    if (seen.has(key)) continue;
    seen.add(key); entries.push(entry);
  }
  return entries;
}

/** The Discover pivot's explicit selection. The shelves are bounded and
 * heterogeneous, so there is no whole-library query to select: membership is the
 * exact admitted cards that are mounted, and a replacement projection retires it.
 *
 * The source's key names the exact admission. A replacement projection bumps a
 * local generation even when its facts read the same, so identical-fact
 * replacements still retire; a further page of the same admission keeps the
 * generation and only admits its new entries. A projection for another library,
 * view or entity, or one that is not ready, is never exposed. */
export function discoveryDocumentSource(input: Readonly<{service: LibraryContentService; route: Readonly<{view?: string; entityId?: string; libraryId: string}>; isCurrent: () => boolean}>) {
  const {service, route, isCurrent} = input;
  let seenProjection: ContentProjection | null | undefined, generation = 0;
  const generationOf = (projection: ContentProjection | null) => {
    if (seenProjection !== projection) {seenProjection = projection; generation++;}
    return generation;
  };
  const source: CatalogueDocumentSource = {
    subscribe: listener => service.subscribe(listener),
    getSnapshot() {
      if (!isCurrent()) return null;
      const snapshot = service.getSnapshot(), projection = snapshot.projection;
      if (!projection || snapshot.phase !== 'ready') return null;
      const scope = projection.scope;
      if (scope.libraryId !== route.libraryId || (scope.view ?? '') !== (route.view ?? '') || (scope.entityId ?? '') !== (route.entityId ?? '')) return null;
      return {
        key: JSON.stringify([generationOf(projection), scope.serverId, scope.libraryId, scope.view ?? '', scope.entityId ?? '', scope.viewerFence ?? '', discoverySections(snapshot).map(section => section.id)]),
        revision: JSON.stringify([scope.serverId, scope.libraryId, scope.viewerFence ?? '', projection.revision]),
        entries: discoveryEntries(snapshot),
      };
    },
  };
  return source;
}

export type DiscoverySelectionInput = Readonly<{
  context: ScreenContext;
  service: LibraryContentService;
  route: Readonly<{view?: string; entityId?: string; libraryId: string}>;
  principal: Omit<BrowseSelectionScope, 'viewId'>;
  metadataFence: () => string | undefined;
  playback?: WebShellPlaybackRuntime;
}>;
type Built = Readonly<{owners: readonly unknown[]; selection: CatalogueDocumentSelection}>;

/** Built once per committed screen lifetime and authority owner set, never during
 * render: a replaced screen, viewer, library or playback owner retires its own
 * windows, and the previous selection is never returned under new inputs. */
export function useDiscoverySelection(input: DiscoverySelectionInput | undefined): CatalogueDocumentSelection | undefined {
  const [built, setBuilt] = useState<Built>();
  const context = input?.context, service = input?.service, playback = input?.playback, metadataFence = input?.metadataFence;
  const libraryId = input?.route.libraryId, view = input?.route.view ?? '', entityId = input?.route.entityId ?? '';
  const serverId = input?.principal.serverId, authority = input?.principal.authority, accountId = input?.principal.accountId, profileId = input?.principal.profileId;
  const owners = [context, service, libraryId, view, entityId, serverId, authority, accountId, profileId, metadataFence, playback] as const;
  const current = built && owners.every((owner, index) => owner === built.owners[index]) && owners.length === built.owners.length ? built.selection : undefined;
  // The effect keeps the input it was admitted with; the explicit owner list below
  // is its exact dependency set, so a changed screen, viewer or playback owner
  // rebuilds and the old one is retired.
  const admittedInput = useRef(input);
  useLayoutEffect(() => {admittedInput.current = input;});
  useLayoutEffect(() => {
    const captured = admittedInput.current;
    if (!captured) {setBuilt(undefined); return;}
    const capturedOwners = [context, service, libraryId, view, entityId, serverId, authority, accountId, profileId, metadataFence, playback];
    const selection = createCatalogueDocumentSelection(captured.context,
      discoveryDocumentSource({service: captured.service, route: captured.route, isCurrent: () => captured.context.isCurrent()}),
      captured.principal, captured.metadataFence, captured.playback);
    selection.start();
    setBuilt({owners: capturedOwners, selection});
    return () => {selection.dispose(); setBuilt(held => held?.selection === selection ? undefined : held);};
  }, [context, service, libraryId, view, entityId, serverId, authority, accountId, profileId, metadataFence, playback]);
  return current;
}
