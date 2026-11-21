import {useEffect, useState, useSyncExternalStore} from 'react';
import type {Library} from '../../../../../../packages/client-core/src';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import type {NativeDestinationPins} from '../../../screens/destinations/pins';

export type PinEditorRow = Readonly<{id: string; name: string; pinned: boolean; moveable: boolean; toggle: () => void; move: (by: -1 | 1) => void}>;
export type PinEditorProps = {controller: NativeDestinationPins; libraries: readonly Library[]; onClose: () => void};

export function usePinEditor({controller, libraries, onClose}: PinEditorProps) {
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const [dragging, setDragging] = useState(false);
  useEffect(() => {void controller.load();}, [controller]);
  const byId = new Map(libraries.map(library => [library.id, library])), pinned = state.libraries?.pinnedLibraryIds ?? [];
  const ordered = state.libraries ? [...pinned.flatMap(id => {
    const library = byId.get(id);
    return library ? [library] : [];
  }), ...libraries.filter(library => !pinned.includes(library.id))] : [];
  const libraryRows = ordered.map(library => {
    const selected = pinned.includes(library.id);
    return {id: library.id, name: library.name, pinned: selected, moveable: selected,
      toggle: () => {void controller.pinLibrary(library.id, !selected);}, move: (by: -1 | 1) => {void controller.moveLibrary(library.id, by);}};
  });
  const resources = state.resources?.order ?? [], resourceKey = (entry: typeof resources[number]) => JSON.stringify([entry.kind, entry.id]);
  const resourceRows = resources.map(entry => ({id: resourceKey(entry), name: entry.name, pinned: true, moveable: true,
    toggle: () => {void controller.unpinResource(entry);}, move: (by: -1 | 1) => {void controller.moveResource(entry, by);}}));
  return {
    libraryRows, resourceRows, busy: state.busy, dragging, setDragging, hasLibraries: !!state.libraries, ready: state.phase === 'ready',
    loading: !state.libraries && (state.phase === 'idle' || state.phase === 'loading'),
    error: state.error === undefined ? undefined : presentError(state.error, 'library', {operation: 'save'}).body,
    placeLibrary: (id: string, before?: string) => {void controller.placeLibrary(id, before);},
    placeResource: (id: string, before?: string) => {
      const entry = resources.find(value => resourceKey(value) === id);
      const target = before === undefined ? undefined : resources.find(value => resourceKey(value) === before);
      if (entry && (before === undefined || target)) void controller.placeResource(entry, target);
    },
    close: (open: boolean) => {if (!open && !controller.getSnapshot().busy) onClose();},
  };
}
