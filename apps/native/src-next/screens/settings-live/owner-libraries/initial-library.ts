import type {NativeOwnerLibrariesController} from './controller';

/** Preserve the existing directory-before-detail sequence for an owner deep link. */
export async function loadInitialOwnerLibrary(controller: NativeOwnerLibrariesController, libraryId?: string): Promise<void> {
  await controller.refresh();
  if (libraryId) await controller.load(libraryId);
}
