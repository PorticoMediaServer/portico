// Offline execution of the existing enrollment suite when generated workspace
// packages are absent. The production coordinator and download parsers run
// unchanged; only native/catalogue/route-generation boundaries are fixtures.
import {offlineModule} from './offline-module.mjs';
import {NativeModules, NativeEventEmitter, Platform} from '../react-native-stub.mjs';

const words = {serviceText: id => id};
const core = await offlineModule(new URL('../../../../packages/client-core/src/downloads.ts', import.meta.url), {
  '../../contracts/src/types.ts': {canonicalQualityLadder: [{id: 'original'}]},
  '../../contracts/src/routes.ts': {
    routePrefix: () => '/v1/downloads/artifacts/',
    encodedRoutePath: (_name, params) => '/v1/downloads/artifacts/' + params.grant,
  },
  './presentation/service-text.ts': words,
});
const requests = await offlineModule(new URL('../../../../packages/client-core/src/download-requests.ts', import.meta.url), {
  '../../contracts/src/types.ts': {canonicalQualityLadder: [{id: 'original'}]},
  '../../contracts/src/routes.ts': {routePath: name => {if (name !== 'list_download_requests') throw new Error(name); return '/v1/downloads/requests';}},
  './downloads.ts': core,
});
const contracts = {decodeGetReadingSessionsSessionIdProgressResponse: value => value, decodePutReadingSessionsSessionIdProgressResponse: value => value};
const boundary = {NativeModules, NativeEventEmitter, Platform};
const crypto = {verify: async () => true, random: n => new Uint8Array(n), sha256: async () => new Uint8Array(32)};
const reading = await offlineModule(new URL('../../platform/offline-reading.ts', import.meta.url), {
  'react-native': boundary, '../../../packages/contracts/src': contracts,
  '../../../packages/client-core/src/reading': {ReadingError: class extends Error {}, parseReadingManifest: value => value},
  '../src-next/platform/server-connections': {nativeRouteCrypto: crypto},
});
const implementation = await offlineModule(new URL('../../platform/offline-downloads.ts', import.meta.url), {
  './offline-reading': reading, '../../../packages/contracts/src': contracts, 'react-native': boundary,
  '../src-next/platform/server-connections': {nativeRouteCrypto: crypto},
  '../../../packages/client-core/src/index': {...core, ...requests, DownloadsService: class {}},
  '../../../packages/client-core/src/presentation/service-text': words,
});
await offlineModule(new URL('../container-enrollment.test.ts', import.meta.url), {
  '../../../packages/client-core/src/downloads.ts': core,
  '../../../packages/client-core/src/download-requests.ts': requests,
  '../platform/offline-downloads.ts': implementation,
});
