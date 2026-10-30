/** The authored short, sparse and long lockups use the real title, never file-kind guesses. */
export function readBookTitleStyle(title: string, openingInsteadOfDescription = false): 'standard' | 'long' | 'sparse' {
  const length = Array.from(title.trim()).length;
  return length > 22 ? 'long' : openingInsteadOfDescription && length > 12 ? 'sparse' : 'standard';
}
