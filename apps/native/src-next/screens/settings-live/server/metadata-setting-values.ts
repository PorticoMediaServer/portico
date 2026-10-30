import type {MetadataLibrarySettings, MetadataSettings, MetadataSettingsChange} from '../../../../../../packages/client-core/src/metadata-settings';

/** Required unchanged fields accompany one edited row at the original revision. */
export function metadataChange(settings: MetadataSettings, patch: Partial<Omit<MetadataSettingsChange, 'expectedRevision'>>): MetadataSettingsChange {
  return {localArtworkLimitBytes: settings.localArtworkLimitBytes, includeAdult: settings.includeAdult, ...patch, expectedRevision: settings.revision};
}

export function episodeThumbnailLibrary(settings: MetadataSettings, libraryId: string): MetadataLibrarySettings | undefined {
  return settings.librariesComplete ? settings.libraries?.find(library => library.id === libraryId && library.managed && (library.kind === 'tv' || library.kind === 'anime')) : undefined;
}

export function episodeThumbnailChange(settings: MetadataSettings, libraryId: string, mode: string): MetadataSettingsChange | undefined {
  const library = episodeThumbnailLibrary(settings, libraryId);
  if (!library || (mode !== 'provider_then_made' && mode !== 'provider' && mode !== 'off')) return;
  return metadataChange(settings, {libraries: [{id: library.id, expectedRevision: library.revision, episodeThumbnailMode: mode}]});
}
