import type {AdminLibrary} from '../../../contracts/src/next/api.ts';
import type {LibraryAdminDocument, LibrarySettings, MetadataProviderId} from '../administration.ts';
import type {ContentScope} from '../library-content.ts';
import type {InventoryConfig, InventorySource, ScanTier} from '../library-inventory.ts';
import {parseMetadataAgent} from './metadata-agent.ts';
import {unreadableServerResponse} from '../server-messages.ts';

const object = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v);

/** The owner's rebuilt read. Keep the exact options so a narrow edit preserves its other choices. */
export function ownerLibrary(raw: unknown, id: string): AdminLibrary {
  const metadata = parseMetadataAgent(raw);
  if (!object(raw) || raw.id !== id || metadata.libraryId !== id || typeof raw.name !== 'string' || !object(raw.options) || !Array.isArray(raw.folders)) throw new Error(unreadableServerResponse);
  for (const folder of raw.folders) {
    if (!object(folder) || typeof folder.id !== 'string' || !folder.id || typeof folder.path !== 'string' || typeof folder.available !== 'boolean') throw new Error(unreadableServerResponse);
  }
  return raw as unknown as AdminLibrary;
}

/** Only settings the rebuilt read actually supplies enter the existing row model. */
export function libraryDocument(library: AdminLibrary): LibraryAdminDocument {
  const metadata = parseMetadataAgent(library), options = library.options;
  const settings: {-readonly [K in keyof LibrarySettings]?: LibrarySettings[K]} = {}, eligible = ['libraries.name', 'libraries.type'];
  if (['files', 'basic', 'complete'].includes(options.depth)) {
    settings.scanDepth = options.depth === 'files' ? 'file_list_only' : options.depth as 'basic' | 'complete';
    eligible.push('libraries.scanDepth');
  }
  if (typeof options.allowDelete === 'boolean') {settings.allowMediaDeletion = options.allowDelete; eligible.push('libraries.allowDelete');}
  if (typeof options.watch === 'boolean') {settings.monitorChanges = options.watch; eligible.push('libraries.watch');}
  if (library.kind === 'movies' && typeof options.groupSeries === 'boolean') {settings.groupCollections = options.groupSeries; eligible.push('libraries.collections');}
  if (library.kind === 'music') {
    // The route defines omitted/empty lyrics as files only.
    settings.lyricsSource = options.lyrics === 'lrclib' ? 'lrclib' : 'files';
    eligible.push('libraries.lyrics');
  }
  if (options.segmentDetection && ['server', 'off', 'standard', 'thorough'].includes(options.segmentDetection)) settings.segmentDetection = options.segmentDetection as LibrarySettings['segmentDetection'];
  const mediaKind = metadata.libraryKind === 'tv' || metadata.libraryKind === 'anime' ? 'show' : metadata.libraryKind === 'audiobook' ? 'book' : metadata.libraryKind;
  settings.providers = [{mediaKind, provider: metadata.agent as MetadataProviderId, apiKeySet: false, language: metadata.language ?? '', region: metadata.region ?? ''}];
  eligible.push('libraries.titlesFrom');
  if (typeof library.language === 'string') eligible.push('libraries.language');
  if (typeof library.region === 'string') eligible.push('libraries.region');
  return Object.freeze({serverLibrary: library, libraryId: library.id, libraryName: library.name, libraryKind: metadata.libraryKind,
    revision: metadata.revision, digest: library.revision, eligibleControls: Object.freeze(eligible), actions: ['rename', 'scan', 'remove'] as const,
    // Legacy controls require LibrarySettings; eligibility gates every omitted field. No invented defaults or inert values.
    settings: Object.freeze(settings) as LibrarySettings, analysisMatrix: {costClasses: [], operations: []}, sources: libraryFolders(library, {serverId: '', viewerId: ''}).sources, enumerations: {},
    availableProviders: metadata.agents.map(p => ({id: p.id as MetadataProviderId, name: p.name, mediaKinds: [mediaKind], supportsApiKey: false, supportsLocale: true, attributionNote: ''}))});
}

/** Folders are members of the same versioned library; no file-inventory read is needed. */
export function libraryFolders(library: AdminLibrary, scope: ContentScope): InventoryConfig {
  const revision = Number(library.revision);
  const sources = library.folders.map(folder => Object.freeze({id: folder.id, libraryId: library.id, kind: folder.source ? 'remote' : 'local',
    name: folder.path.split(/[\\/]/).filter(Boolean).at(-1) ?? folder.path, path: folder.path, resolvedPath: folder.path,
    health: folder.available ? 'ready' : 'unavailable', enabled: true, generation: revision, identityConfirmed: true,
    lastCompleteAt: folder.lastScanAt ?? '', lastProgressAt: '', nextScanAt: '',
    // A display label is not an editable folder name; the four folder settings are not on this server yet.
    eligibleControls: Object.freeze(['libraries.folderPath']), ...(folder.source ? {remoteSource: folder.source} : {}),
  }) as InventorySource);
  return Object.freeze({eligibleSourceControls: Object.freeze(['libraries.folderPath']), scope: {serverId: scope.serverId, viewerFence: scope.viewerId}, libraryId: library.id, revision, sources: Object.freeze(sources),
    policy: {tier: library.options.depth === 'files' ? 'file_list_only' : library.options.depth as ScanTier, revision, operations: []}});
}
