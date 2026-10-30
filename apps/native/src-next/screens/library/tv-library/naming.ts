import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {NativeDestinationActivation} from '../../destinations/focus';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import {authorityKey} from '../../../shell/model';
import type {ShellAuthority} from '../../../shell/model';
import {createNativeTVLibraryNaming} from '../../../tv/library/save-view';
import type {NativeLibraryActions} from '../LibraryScreen';
import type {NativeLibraryDestination, NativeLibraryViewDraft} from '../model';
import type {NativeSaveViewSnapshot} from '../save-view';

export type NativeTVLibraryNaming = ReturnType<typeof createNativeTVLibraryNaming>;

/** F-LIB-52's own panel: one per authority and captured library, never a
 * process-wide naming surface shared by two viewers. */
export const nativeTVNamingPanelId = (authority: ShellAuthority, libraryId: string): string =>
  `tv.${authorityKey(authority)}.library.${JSON.stringify(libraryId)}.naming`;

/** Ruling 37.2 enters the name keyboard on its first key, which is the shared
 * keyboard's own row 0 / 'a' address. */
export const nativeTVNamingFirstKey = (keyboardId: string): Readonly<{regionId: string; childId: string}> =>
  Object.freeze({regionId: `${keyboardId}.row.0`, childId: 'a'});

/** The one rule behind both a bare key press and the shared keyboard's
 * onChangeText: the keyboard must be open and the value must change inside the
 * server's 200-character name bound. A refused edit returns undefined and the
 * typed name survives (ruling 37.5). */
export function nativeTVNameEdit(form: NativeSaveViewSnapshot, keyboard: NativeDestinationActivation, next: string): string | undefined {
  if (!form.open || form.busy || form.nameLocked || !keyboard.visible) return undefined;
  if (next === form.name) return undefined;
  if (Array.from(next).length > 200) return undefined;
  return next;
}

export type NativeTVLibraryNamingStore = Readonly<{
  subscribe: (listener: () => void) => () => void;
  getSnapshot: () => NativeTVLibraryNaming | undefined;
  /** One opening at a time, against the destination it captured its definition
   * from. An in-flight accepted save keeps its own receipt owner. */
  open: (destination: NativeLibraryDestination, draft: NativeLibraryViewDraft) => boolean;
  close: () => boolean;
  dispose: () => void;
}>;

/** The TV Save view opening. It borrows the genuine staged definition and the
 * canonical §38 conditions suggestion, and is retired by the same authority and
 * route fence that owns the importing module. */
export function createNativeTVLibraryNamingStore(context: NativeShellBootstrapContext): NativeTVLibraryNamingStore {
  let naming: NativeTVLibraryNaming | undefined, disposed = false;
  const listeners = new Set<() => void>();
  const emit = () => {for (const listener of [...listeners]) listener();};
  const retire = () => {
    const held = naming;
    naming = undefined;
    held?.dispose();
  };
  const drop = () => {
    if (!naming) return;
    retire();
    emit();
  };
  // The borrowed form disposes itself on an authority or route change; the
  // store then stops handing out a retired (closed) opening.
  const check = () => {if (naming && !naming.controller.getSnapshot().open) drop();};
  const offs = [context.navigation.subscribe(check), context.subscribeAuthority(check)];
  return Object.freeze({
    subscribe: (listener: () => void) => {
      if (disposed) return () => {};
      listeners.add(listener);
      return () => {listeners.delete(listener);};
    },
    getSnapshot: () => disposed ? undefined : naming,
    open: (destination: NativeLibraryDestination, draft: NativeLibraryViewDraft) => {
      if (disposed) return false;
      // A receipt already accepted for this opening is not replaceable.
      if (naming && naming.controller.getSnapshot().busy) return false;
      if (!context.current()) return false;
      retire();
      try {
        naming = createNativeTVLibraryNaming(context, destination, draft, defaultI18n, () => {
          // The receipt closes the form itself; the mounted panel reports that
          // close so the surface keeps its drawn exit before it is dropped.
        });
        // F-LIB-52: the drawn opening already holds its first key, so the panel
        // mounts in the state the frame draws rather than turning it on later.
        naming.input.openKeyboard();
      } catch {naming = undefined;}
      emit();
      return !!naming;
    },
    close: () => {
      if (disposed || !naming) return false;
      return naming.close();
    },
    dispose: () => {
      if (disposed) return;
      disposed = true;
      for (const off of offs.splice(0)) off();
      drop();
      listeners.clear();
    },
  });
}

/** TV keeps ONE Save view entry. The toolbar control opens the F-LIB-52 name
 * keyboard; a stale call carrying another library's destination is ignored
 * rather than redirected into a sheet this platform does not draw. */
export function nativeTVLibraryActions(actions: NativeLibraryActions, naming: NativeTVLibraryNamingStore,
  destination: NativeLibraryDestination): NativeLibraryActions {
  return {...actions, saveView: (actual: NativeLibraryDestination, draft: NativeLibraryViewDraft) => {
    if (actual !== destination) return;
    naming.open(destination, draft);
  }};
}
