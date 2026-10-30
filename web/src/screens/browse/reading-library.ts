/** Content kind 'book' also describes audiobooks. Only the actual authorized
 * library kind selects the Read library surface. */
export const isReadingLibrary = (kind: string): boolean => kind === 'book' || kind === 'comic';
