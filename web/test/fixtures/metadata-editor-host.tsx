import {createRoot} from 'react-dom/client';
import {HomeSelectionController, homeSelectionContext, type HomeSelectionOptions} from '@core/home-selection/index.ts';
import type {HomeDocument, HomeEntry, HomeRow} from '@core/home.ts';
import type {HomeService} from '@core/home-service.ts';
import {IconSprite} from '../../src/kit/Icon';
import {SelectionSurfaces, type SelectionRendererSource} from '../../src/shell/SelectionSurfaces';
import '../../src/kit/tokens.css';
import '../../src/kit/fonts.css';
import '../../src/bootstrap.css';

// Presentation-only DEV server responses. The real controller validates the preview and opens
// the shipped renderer. No account/authentication, network mutation or playback is installed.
if (!import.meta.env.DEV) throw new Error('Metadata specimens require development.');
const fence = 'a'.repeat(64), revision = 'b'.repeat(64);
const entry = {id: 'dune', libraryId: 'library', kind: 'movie', title: 'Dune', available: true,
  homeActions: {play: false, queue: false, playlist: false, collection: false, myList: false, favorite: false, watched: false, played: false, rating: false, editMetadata: true, refreshMetadata: false, delete: false, download: false, details: false, selection: {play: false, queue: false, playlist: false, collection: false, myList: false, favorite: false, watched: false, download: false, edit: true, refresh: false, delete: false}, state: {watchlisted: false, favorite: false, watched: false, rating: null, personalRevision: 0, containerRevision: 0}}} as unknown as HomeEntry;
const row = {id: 'recent', viewerFence: fence, entries: [entry], total: 1, start: 0, limit: 1, hasMore: false, revision: {catalog: 1, viewer: 1}} as unknown as HomeRow;
const homeDocument = {serverId: 'server', viewerFence: fence, rows: [row], spotlight: [], layout: {revision: 1, rowOrder: ['recent'], hiddenRowIds: []}, revision: {catalog: 1, viewer: 1}} as unknown as HomeDocument;
const fieldValues: Record<string, string> = {title: 'Dune', sortTitle: 'Dune', originalTitle: 'Dune', metadataLanguage: 'English', year: '2021', releaseDate: '2021-10-22', contentRating: 'PG-13', edition: '', studio: 'Legendary Entertainment', country: 'United States', tagline: 'Beyond fear, destiny awaits.', description: 'Paul Atreides, a brilliant and gifted young man born into a great destiny beyond his understanding, must travel to the most dangerous planet in the universe to ensure the future of his family and his people.'};
const schema = Object.keys(fieldValues).map(field => ({field, label: field === 'metadataLanguage' ? 'web.libraries.metadataLanguage' : `web.metadata.field.${field}`, group: field === 'year' ? 'numbers' : 'general', type: field === 'description' ? 'multiline' : field === 'year' ? 'integer' : field === 'releaseDate' ? 'date' : 'text', maxLength: field === 'description' ? 20000 : 300, ...(field === 'year' ? {min: 0, max: 9999} : {}), bulk: false}));
const response = {serverId: 'server', viewerFence: fence, target: {kind: 'item', id: 'dune'}, libraryId: 'library', revision, schema, artworkRoles: ['poster', 'backdrop'], snapshot: {fields: Object.fromEntries(Object.entries(fieldValues).map(([field, value]) => [field, {value, automaticValue: value, source: field === 'sortTitle' ? 'manual' : 'tmdb', locked: field === 'sortTitle'}])), identity: {provider: 'tmdb', id: '438631', status: 'matched', revision: 1, locked: false}, relationshipLocks: {credit: false}, relationships: [], artwork: []}, candidates: [], history: [], artwork: {candidates: [], jobs: []}, cascades: []};
const api = {async request<T>(path: string, method?: string, body?: unknown): Promise<T> {
  if (path.endsWith('/metadata/bulk/preview') && method === 'POST') {
    const targets = (body as {targets: {kind: string; id: string; libraryId: string}[]}).targets;
    return {serverId: 'server', viewerFence: fence, contentKind: 'movie', kind: 'item', total: 1, schema: [], singleItemFields: schema, excludedFields: schema, fields: {}, targets: targets.map(target => ({...target, expectedRevision: revision}))} as T;
  }
  if (path === '/v1/metadata/item/dune' && method === 'GET') return response as T;
  throw new Error('This specimen does not perform mutations or other network requests.');
}};
const context = homeSelectionContext({serverId: 'server', authority: 'local', accountId: 'owner', profileId: 'profile', viewId: 'home'}, homeDocument);
const options: HomeSelectionOptions = {api, events: {on: () => () => {}}, home: {getSnapshot: () => ({phase: 'ready', document: homeDocument, paging: new Set()}), subscribe: () => () => {}} as unknown as HomeService,
  context, environment: {platform: 'desktop-web', isOwner: true, canEditMetadata: true}, isCurrent: candidate => JSON.stringify(candidate) === JSON.stringify(context), requestId: async () => crypto.randomUUID(),
  playback: {play: async () => {throw Error('No specimen playback');}, enqueue: async () => {throw Error('No specimen queue');}, playChannel: async () => {throw Error('No specimen channel');}},
  routes: {openEntry: () => {}, openCollection: () => {}, openDestination: () => {}}, reportError: error => {throw error;}};
const controller = new HomeSelectionController(options);
const source: SelectionRendererSource = {controller, anchor: {current: null}, artwork: {foreground: () => undefined} as unknown as SelectionRendererSource['artwork'], owner: true, editorAudience: {authority: 'owner'}, dockVisible: () => false, reportError: options.reportError,
  metadata: {api, scope: {serverId: 'server', viewerId: 'owner'}, current: snapshot => options.isCurrent(snapshot.context) && snapshot.editor?.preview?.viewerFence === fence}};
controller.registerWindow({id: 'window', row, sectionOrder: 0});
controller.selectLoaded();
createRoot(document.getElementById('root')!).render(<><IconSprite/><SelectionSurfaces source={source}/></>);
void controller.openEditor();
window.addEventListener('pagehide', () => controller.dispose(), {once: true});
