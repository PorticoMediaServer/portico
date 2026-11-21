import {useEffect, useRef, useState} from 'react';
import type {LibraryAdminDocument, LibrarySettings} from '../../../../../../packages/client-core/src/administration';
import {patchLibrarySettingsBinding, readLibrarySettingsBinding} from '../../../../../../packages/client-core/src/presentation/settings-bindings';
import {type SettingsRow as SpecRow} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {libraryNumberValue, libraryRowVisible} from '../../../screens/settings-live/owner-libraries/library-settings-model';

export type Props = {tab: 'general' | 'metadata' | 'scanning'; document: LibraryAdminDocument; busy: boolean; t: typeof defaultI18n.t; save: (settings: Partial<LibrarySettings>) => void; fetchLyrics?: (current: () => boolean) => Promise<void>};

/** Spec copy and control types, projected through the shared library writer. */
export function useOwnerLibrarySettingsRows({tab, document, busy, save, fetchLyrics}: Props) {
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
  return {editing, setEditing, text, setText, error, custom, mounted, read, change, rowVisible,
    number: editing ? libraryNumberValue(text, editing) : undefined};
}
