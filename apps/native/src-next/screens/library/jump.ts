/** The letters the server's title index offers, in card order, and the one the
 * first visible card belongs to (F-LIB-49: the sheet opens on that letter). */
export type JumpLetter = Readonly<{key: string; index: number}>;

export function libraryJumpLetters(positionIndex: Readonly<Record<string, number>> | undefined): readonly JumpLetter[] {
  return Object.freeze(Object.entries(positionIndex ?? {})
    .map(([key, index]) => Object.freeze({key, index}))
    .sort((a, b) => a.index - b.index || a.key.localeCompare(b.key)));
}

export function libraryJumpLetter(letters: readonly JumpLetter[], firstVisible: number): string | undefined {
  let found: string | undefined;
  for (const letter of letters) {
    if (letter.index > firstVisible) break;
    found = letter.key;
  }
  return found ?? letters[0]?.key;
}

/**
 * LB-6: the A–Z rail is drawn for any title order the server indexed, A–Z or Z–A, filtered or
 * not (a tab's own filter included), as web draws it: the server publishes the letters' places
 * for exactly the list on screen. Nothing it can jump to (no letter inside the list), no rail.
 */
export function libraryAlphabetRail(positionIndex: Readonly<Record<string, number>> | undefined, total: number | undefined,
  sort: Readonly<{field: string}> | undefined): boolean {
  if (!positionIndex || total === undefined || sort?.field !== 'title') return false;
  return Object.values(positionIndex).some(index => Number.isInteger(index) && index >= 0 && index < total);
}
