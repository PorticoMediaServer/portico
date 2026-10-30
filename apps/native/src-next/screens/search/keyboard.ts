/** Authored tv.html5 / tv-more12 input inventory and order, not fixture data. */
export const nativeTVKeyboardRows: readonly (readonly string[])[] = Object.freeze([
  'abcdef', 'ghijkl', 'mnopqr', 'stuvwx', 'yz1234', '567890',
].map(row => Object.freeze([...row])));
export type NativeTVKeyboardKey = string | 'space' | 'delete' | 'clear';
export function applyNativeTVKeyboardKey(value: string, key: NativeTVKeyboardKey, rows: readonly (readonly string[])[] = nativeTVKeyboardRows): string {
  if (key === 'clear') return '';
  if (key === 'delete') return [...value].slice(0, -1).join('');
  if (key === 'space') return value + ' ';
  if (!rows.some(row => row.includes(key))) throw new Error('native_keyboard_key_not_drawn');
  return value + key;
}
