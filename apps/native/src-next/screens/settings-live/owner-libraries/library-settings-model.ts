import type {LibraryAdminDocument} from '../../../../../../packages/client-core/src/administration';
import type {SettingsOption, SettingsRow} from '../../../../../../packages/client-core/src/presentation/settings-spec';

/** Eligibility comes from the library document; kind checks only narrow it. */
export function libraryRowVisible(id: string, document: Pick<LibraryAdminDocument, 'libraryKind' | 'eligibleControls'>): boolean {
  if (!document.eligibleControls.includes(id)) return false;
  const kind = document.libraryKind;
  if (id === 'libraries.collections') return kind === 'movie';
  if (id === 'libraries.lyrics' || id === 'libraries.acoustid') return kind === 'music';
  if (id === 'libraries.episodeNumbering' || id === 'libraries.episodeThumbs') return kind === 'tv' || kind === 'anime';
  return true;
}

export function libraryRowOptions(row: SettingsRow, document: Pick<LibraryAdminDocument, 'libraryKind' | 'availableProviders'>): readonly SettingsOption[] {
  const kind = document.libraryKind === 'tv' || document.libraryKind === 'anime' ? 'show' : document.libraryKind === 'audiobook' ? 'book' : document.libraryKind;
  return (row.control.options ?? []).filter(option => {
    if (row.id === 'libraries.titlesFrom') return document.availableProviders.some(provider => provider.id === option.value && provider.mediaKinds.includes(kind));
    if (row.id === 'libraries.localTags' && option.value === 'supplement') return document.libraryKind === 'music' || document.libraryKind === 'audiobook';
    return true;
  });
}

export function libraryNumberValue(text: string, row: Pick<SettingsRow, 'control'>): number | undefined {
  if (!/^\d+$/.test(text.trim())) return;
  const value = Number(text), {min = 0, max = Number.MAX_SAFE_INTEGER, step = 1} = row.control;
  if (!Number.isSafeInteger(value) || value < min || value > max || (value - min) % step !== 0) return;
  return value;
}

export function libraryListState(phase: string, count: number): 'loading' | 'empty' | 'rows' | 'none' {
  if (count > 0) return 'rows';
  return phase === 'ready' ? 'empty' : phase === 'idle' || phase === 'loading' ? 'loading' : 'none';
}
