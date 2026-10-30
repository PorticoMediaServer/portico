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
