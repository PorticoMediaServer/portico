/** Pointer drops and keyboard moves use the same complete stable-ID order. */
export function movedLibraryIds(libraries: readonly {id: string}[], id: string, to: number): readonly string[] | undefined {
  const ids = libraries.map(library => library.id), from = ids.indexOf(id);
  if (from < 0 || !Number.isInteger(to) || to < 0 || to >= ids.length || from === to) return undefined;
  ids.splice(to, 0, ...ids.splice(from, 1));
  return ids;
}
