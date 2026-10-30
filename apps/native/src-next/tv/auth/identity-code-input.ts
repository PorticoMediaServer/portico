/** Frame 2a's alphabet, exactly as drawn: A–Z, the dash, then 0–9. A symbol outside
 * this set can never match the server's code, so no other key is offered. */
export const identityCodeCharacters = [...'ABCDEFGHIJKLMNOPQRSTUVWXYZ-1234567890'];
export const identityCodeRows = Array.from({length: Math.ceil(identityCodeCharacters.length / 9)}, (_, row) => identityCodeCharacters.slice(row * 9, row * 9 + 9));

/** Spaces and dashes are ignored; capitals are forced; only the first 12 symbols count. */
export function appendIdentityCode(code: string, key: string): string {
  const raw = code.replace(/[^A-Z0-9]/g, '');
  if (key === 'clear') return '';
  if (key === 'delete') return raw.slice(0, -1);
  // The drawn dash and Space are separators: they never count toward the twelve symbols.
  if (key === 'space' || key === '-') return raw;
  if (raw.length >= 12) return raw;
  return (raw + key.toUpperCase()).replace(/[^A-Z0-9]/g, '').slice(0, 12);
}

/** Dashes are redrawn after the fourth and eighth symbols, as the field shows them. */
export function formatIdentityCode(code: string): string {
  const raw = code.replace(/[^A-Z0-9]/g, '');
  return raw.match(/.{1,4}/g)?.join('-') ?? '';
}

/** The exact next focus the frame names when the keyboard appears, derived from the
 * rows it actually drew: V sits in the S–Z row, never in an assumed row number. */
const entryRow = identityCodeRows.findIndex(row => row.includes('V'));
export const identityCodeEntry = Object.freeze({region: `tv.identity-code.keyboard.${entryRow}`, child: 'V'});
