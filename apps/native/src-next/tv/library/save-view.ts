import {savedViewSummary, type SavedViewSummaryText} from '../../../../../packages/client-core/src/presentation/saved-view-summary';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {nativeDestinationLifetime} from '../../screens/destinations/lifetime';
import {nativeScreenServices} from '../../screens/native-screen-services';
import {nativeLibraryCanSave, type NativeLibraryDestination, type NativeLibraryViewDraft} from '../../screens/library/model';
import {NativeLibrarySaveView} from '../../screens/library/save-view';
import {createNativeTVNameInput} from './keyboard-state';

/** One TV naming opening, borrowing the actual staged definition and canonical
 * UI-language suggestion. No PersonalResource.summary write or ambient query read. */
export function createNativeTVLibraryNaming(context: NativeShellBootstrapContext, destination: NativeLibraryDestination,
  draft: NativeLibraryViewDraft, text: SavedViewSummaryText, saved: (id: string) => void) {
  const lifetime = nativeDestinationLifetime(context), navigation = context.navigation.getSnapshot(), source = destination.getSnapshot();
  if (navigation.route.kind !== 'shell' || navigation.destination.kind !== 'library' || navigation.destination.libraryId !== source.library.id
    || !nativeLibraryCanSave(source, draft) || !source.capabilities) throw new Error('native_library_view_unavailable');
  const definition = destination.captureSavedView(draft), common = nativeScreenServices(context);
  const suggestion = savedViewSummary(definition, source.capabilities, text)?.suggestedName ?? '';
  let disposed = false;
  const cleanups: (() => void)[] = [];
  const current = () => !disposed && lifetime.current() && context.navigation.getSnapshot() === navigation
    && context.libraries().some(library => library.id === source.library.id && library.kind === source.library.kind);
  const controller = new NativeLibrarySaveView({api: context.api, scope: lifetime.scope, definition, requestId: common.requestId,
    current, saved: id => {dispose(); saved(id);}});
  controller.setName(suggestion);
  const input = createNativeTVNameInput(controller);
  const dispose = () => {
    if (disposed) return;
    disposed = true; cleanups.splice(0).forEach(stop => stop()); input.dispose(); controller.dispose();
  };
  const check = () => {if (!current()) dispose();};
  cleanups.push(context.navigation.subscribe(check), context.subscribeAuthority(check));
  return Object.freeze({controller, input, dispose, close: () => {
    if (controller.getSnapshot().busy) return false;
    dispose(); return true;
  }});
}
