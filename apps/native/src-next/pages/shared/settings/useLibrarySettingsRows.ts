import {useEffect, useRef, useState} from 'react';
import type {LibraryAdminDocument, LibrarySettings} from '../../../../../../packages/client-core/src/administration';
import {patchLibrarySettingsBinding, readLibrarySettingsBinding} from '../../../../../../packages/client-core/src/presentation/settings-bindings';
import {settingsSpec, type SettingsRow as SpecRow} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import type {defaultI18n} from '../../../../../../packages/i18n/src';
import {libraryNumberValue, libraryRowOptions, libraryRowVisible} from '../../../screens/settings-live/owner-libraries/library-settings-model';

const libraryPanel = settingsSpec.find(section => section.scope === 'server' && section.id === 'libraries')?.panels?.library;
export type LibrarySettingsRowsProps = {tab: 'general' | 'metadata' | 'scanning'; document: LibraryAdminDocument; busy: boolean; t: typeof defaultI18n.t; save: (settings: Partial<LibrarySettings>) => void; fetchLyrics?: (current: () => boolean) => Promise<void>};

/** A library's settings tab: the spec's groups and rows for this library, projected through the shared library writer;
 * Custom choices reveal their detail rows; a number opens its own editor. Moved from the old rows as it was. */
export function useLibrarySettingsRows({tab, document, busy, save, fetchLyrics}: LibrarySettingsRowsProps) {
  const [editing, setEditing] = useState<SpecRow>(), [text, setText] = useState(''), [error, setError] = useState<string>();
  const [custom, setCustom] = useState<ReadonlySet<string>>(new Set());
  const mounted = useRef(false);
  useEffect(() => {mounted.current = true; return () => {mounted.current = false;};}, []);
  const read = (id: string) => readLibrarySettingsBinding(id, document)?.current;
  const change = (row: SpecRow, value: unknown) => {
    if (busy) return;
    if (value === 'custom') {
      setCustom(before => new Set([...before, row.id]));
      // A Custom preview still needs the preview runner; editing spacing alone
      // must not leave the saved preview mode Off.
      if (row.id === 'libraries.scrub' && read(row.id) === 'off') {
        try {setError(undefined); save(patchLibrarySettingsBinding(row.id, 'standard', document));}
        catch (problem) {setError(presentError(problem, 'library', {operation: 'save'}).body);}
      }
      return;
    }
    setCustom(before => {const next = new Set(before); next.delete(row.id); return next;});
    try {setError(undefined); save(patchLibrarySettingsBinding(row.id, value, document));}
    catch (problem) {setError(presentError(problem, 'library', {operation: 'save'}).body);}
  };
  const rowVisible = (row: SpecRow) => {
    // MetadataAgentChoice already owns this setting on the native page.
    if (row.id === 'libraries.titlesFrom') return false;
    if (row.control.type === 'action') return row.id === 'libraries.fetchLyrics' && !!fetchLyrics && document.libraryKind === 'music' && document.eligibleControls.includes('libraries.lyrics') && document.settings.lyricsSource === 'lrclib';
    if (!libraryRowVisible(row.id, document) || read(row.id) === undefined) return false;
    if (tab === 'general' && row.id !== 'libraries.collections') return false;
    if (row.id === 'libraries.analysisSteps') return custom.has('libraries.scanDepth') || read('libraries.scanDepth') === 'custom';
    if (['libraries.scrubSpacing', 'libraries.scrubWidth', 'libraries.scrubMax'].includes(row.id)) return custom.has('libraries.scrub') || read('libraries.scrub') === 'custom';
    return true;
  };
  const fact = (row: SpecRow) => readLibrarySettingsBinding(row.id, document)!;
  const disabled = (row: SpecRow) => {
    const condition = row.enableWhen, actual = condition ? read(condition.id) : undefined;
    return busy || !!condition && ((condition.in !== undefined && !condition.in.includes(actual as never)) || (condition.notIn !== undefined && condition.notIn.includes(actual as never)));
  };
  const options = (row: SpecRow) => {
    const all = libraryRowOptions(row, document), current = custom.has(row.id) ? 'custom' : fact(row).current;
    return {all, current, offered: [...all.map(option => ({value: String(option.value), label: option.label, hint: option.hint,
      disabled: option.locked || option.value === 'custom' && row.id !== 'libraries.scanDepth' && row.id !== 'libraries.scrub'})),
      ...(all.some(option => String(option.value) === String(current)) ? [] : [{value: String(current), label: undefined, hint: undefined, disabled: true}])]};
  };
  const groups = (libraryPanel?.tabs?.find(value => value.id === tab)?.groups ?? []).map((group, index) => ({key: index, title: group.title,
    rows: group.rows.flatMap(row => [row, ...(row.children ?? [])]).filter(rowVisible)})).filter(group => group.rows.length);
  const number = editing ? libraryNumberValue(text, editing) : undefined;
  return {groups, change, fact, disabled, options, error, busy, editing, setEditing, text, setText, number,
    fetch: () => {if (!busy && mounted.current) void fetchLyrics?.(() => mounted.current);},
    saveNumber: () => {if (editing && number !== undefined) {change(editing, number); setEditing(undefined);}}};
}
