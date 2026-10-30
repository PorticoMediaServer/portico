import type {NativeDestinationActivation} from '../../kit/focus/destination-entry';
export type {NativeDestinationActivation} from '../../kit/focus/destination-entry';
export {createDestinationEntryFocus} from '../../kit/focus/destination-entry';
import {authorityKey, type ShellAuthority} from '../../shell/model';
import {tvFocusIds} from '../../shell/tv-focus';

/** Assembly owns navigation visibility and intent; a renderer never guesses it
 * from Home state or resets a retained page merely because it renders again. */
export type NativeDestinationActivationPort = Readonly<{
  subscribe: (listener: () => void) => () => void;
  getSnapshot: () => NativeDestinationActivation;
}>;

export function nativeDestinationFocusIds(authority: ShellAuthority, focusScope?: string) {
  const prefix = `tv.${authorityKey(authority)}${focusScope ? `.${JSON.stringify(focusScope)}` : ''}`, rail = tvFocusIds(authority).rail;
  return {
    rail,
    searchKeyboard: `${prefix}.search.keyboard`,
    searchKeyboardRow: (row: number) => `${prefix}.search.keyboard.row.${row}`,
    searchResults: `${prefix}.search.results`,
    searchChips: `${prefix}.search.chips`, searchTop: `${prefix}.search.top`,
    searchLanding: `${prefix}.search.landing`, searchRecent: `${prefix}.search.recent`,
    /** One region per drawn result section, so each keeps its own row memory. */
    searchSection: (groupId: string) => `${prefix}.search.results.section.${groupId}`,
    savedTabs: `${prefix}.saved.tabs`, savedToolbar: `${prefix}.saved.toolbar`,
    savedGrid: `${prefix}.saved.grid`,
    libraryTabs: (libraryId: string) => `${prefix}.library.${JSON.stringify(libraryId)}.tabs`,
    libraryToolbar: (libraryId: string) => `${prefix}.library.${JSON.stringify(libraryId)}.toolbar`,
    libraryGrid: (libraryId: string, tabId: string) => `${prefix}.library.${JSON.stringify(libraryId)}.grid.${JSON.stringify(tabId)}`,
    libraryCard: (libraryId: string, id: string) => `${prefix}.library.${JSON.stringify(libraryId)}.card.${id}`,
  };
}
