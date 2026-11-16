import type {MetadataSettings, MetadataSettingsChange} from '../../../../../../packages/client-core/src/metadata-settings';

/** Required unchanged fields (and the artwork limit, on a server that has one) accompany one edited row at the original revision. */
export function metadataChange(settings: MetadataSettings, patch: Partial<Omit<MetadataSettingsChange, 'expectedRevision'>>): MetadataSettingsChange {
  return {...(settings.localArtworkLimitBytes !== undefined ? {localArtworkLimitBytes: settings.localArtworkLimitBytes} : {}), includeAdult: settings.includeAdult, ...patch, expectedRevision: settings.revision};
}
