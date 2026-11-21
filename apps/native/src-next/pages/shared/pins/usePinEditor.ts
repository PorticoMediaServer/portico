import {useEffect, useState, useSyncExternalStore} from 'react';
import type {Library} from '../../../../../../packages/client-core/src';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import type {NativeDestinationPins} from '../../../screens/destinations/pins';

/** Edit pins (ruling 39, frame 25): the profile's libraries (pinned ones first, in their order, then the rest) and its
 * pinned Saved items, each written at once when toggled, moved or dropped. Moved from the old sheet as it was. */
export function usePinEditor({controller, libraries}: {controller: NativeDestinationPins; libraries: readonly Library[]}) {
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const [dragging, setDragging] = useState(false);
  useEffect(() => {void controller.load();}, [controller]);
  const byId = new Map(libraries.map(library => [library.id, library])), pinned = state.libraries?.pinnedLibraryIds ?? [];
  const ordered = state.libraries ? [...pinned.map(id => byId.get(id)).filter((value): value is Library => !!value), ...libraries.filter(library => !pinned.includes(library.id))] : [];
  const libraryRows = ordered.map(library => ({id: library.id, name: library.name, pinned: pinned.includes(library.id), moveable: pinned.includes(library.id),
    toggle: () => {void controller.pinLibrary(library.id, !pinned.includes(library.id));}, move: (by: -1 | 1) => {void controller.moveLibrary(library.id, by);}}));
  const resources = state.resources?.order ?? [], resourceKey = (entry: typeof resources[number]) => JSON.stringify([entry.kind, entry.id]);
  const resourceRows = resources.map(entry => ({id: resourceKey(entry), name: entry.name, pinned: true, moveable: true,
    toggle: () => {void controller.unpinResource(entry);}, move: (by: -1 | 1) => {void controller.moveResource(entry, by);}}));
  return {state, dragging, setDragging, libraryRows, resourceRows,
    error: state.error === undefined ? undefined : presentError(state.error, 'library', {operation: 'save'}).body,
    placeLibrary: (id: string, before?: string) => {void controller.placeLibrary(id, before);},
    placeResource: (id: string, before?: string) => {
      const entry = resources.find(value => resourceKey(value) === id), target = before === undefined ? undefined : resources.find(value => resourceKey(value) === before);
      if (entry && (before === undefined || target)) void controller.placeResource(entry, target);
    },
    noneSaved: !resources.length && state.phase === 'ready'};
}
