/** The admin scope's `viewerFence` is the server's own hash (presentation
 * identity plus session epoch), not the home document's fence, so comparing the
 * two never matches. LibraryManagementService already rejects a changed viewer
 * fence on every read; this only confirms the scan acts on the library the menu
 * addressed and surfaces a genuine read failure instead of a false scope change. */
export type LibraryScanRead = Readonly<{
  phase: string;
  data: Readonly<{library: Readonly<{id: string}>}> | null;
  error?: Readonly<{code: string}> | null;
}>;
export type LibraryScanReadiness = Readonly<{ok: true}> | Readonly<{ok: false; code: string}>;

export function libraryScanReadiness(selected: LibraryScanRead, libraryId: string): LibraryScanReadiness {
  if (selected.phase === 'ready' && selected.data && selected.data.library.id === libraryId) return {ok: true};
  return {ok: false, code: selected.error?.code ?? 'library_management_unavailable'};
}
