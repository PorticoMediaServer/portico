// Browser regression fixture: real App/Shell/Home/PlaybackHost, synthetic service boundary.
import {createRoot} from 'react-dom/client';
import {useEffect, useRef, useState} from 'react';
import {App} from '../../src/App';
import {router} from '../../src/router';
import {createMemoryHistory} from '@tanstack/react-router';
import {createOwnerActivity} from '../../src/shell/Activity';
import {HomeService} from '../../../packages/client-core/src/home-service';
import type {ShellBindings} from '../../src/shell/bindings';
import '../../src/kit/tokens.css';
import '../../src/kit/fonts.css';
import '../../src/bootstrap.css';

const proof = {requests: 0, activityRequests: 0, plays: 0, more: 0, signOut: 0, probeMounts: 0, probeUnmounts: 0, errors: [] as string[]};
function RetainedState() {
  const [value, setValue] = useState(0), reference = useRef({id: 'original'});
  useEffect(() => {proof.probeMounts++;return()=>{proof.probeUnmounts++;};},[]);
  return <button type="button" data-retained-state={value} data-retained-ref={reference.current.id} onClick={()=>setValue(v=>v+1)} style={{position:'fixed',width:1,height:1,overflow:'hidden',clipPath:'inset(50%)'}}>Test retained state {value}</button>;
}
const viewer = {serverId: 'synthetic-server', authority: 'local', accountId: 'test-account', profileId: 'test-profile', role: 'owner'};
const document = {serverId: viewer.serverId, viewerFence: 'synthetic-viewer', spotlight: [],
  layout: {revision: 1, rowOrder: [], hiddenRowIds: []}, revision: {catalog: 1, viewer: 1}, generatedAt: new Date().toISOString(),
  rows: Array.from({length: 6}, (_, row) => ({id: `row-${row}`, viewerFence: 'synthetic-viewer', title: `Test row ${row + 1}`, kind: 'recent', artworkShape: 'poster',
    entries: Array.from({length: 8}, (_, index) => ({id: `movie-${row}-${index}`, kind: 'movie', title: `Test title ${row + 1}.${index + 1}`, libraryId: 'movies', available: true,
      navigation: {view: 'item', entityId: `movie-${row}-${index}`}, playback: {itemId: `movie-${row}-${index}`}, canSelect: true,
      homeActions: {play: true, queue: false, playlist: false, myList: false, watched: false, played: false, favorite: false, rating: false, download: false, collection: false, details: false, editMetadata: false, refreshMetadata: false, delete: false, state: {watchlisted: false, favorite: false, watched: false, rating: null, personalRevision: 0, containerRevision: 0}}})),
    total: 8, start: 0, limit: 20, hasMore: false, nextCursor: '', revision: {catalog: 1, viewer: 1}}))};
let streamCount = 2, failActivity = false;
const hints = new Map<string, Set<() => void>>();
const events = {on: (type: string, fn: () => void) => {const list=hints.get(type)??new Set();list.add(fn);hints.set(type,list);return()=>{list.delete(fn);};}, onResync: () => () => {}, onAny: () => () => {}, getStatus: () => ({connected:true}), subscribeStatus: () => () => {}};
const api = {requestRaw: async () => {
  proof.activityRequests++;
  if(failActivity) return {status: 503, headers: {}, body: {code: 'busy'}};
  const session = (id: string) => ({id,user:{id:'test-account',username:'test-user'},profile:{id:'test-profile',name:'Test profile'},device:{id:'test-device',name:'Test browser'},kind:'vod',state:'playing',startedAt:'2026-10-22T00:00:00Z',problem:false,converting:false,positionMs:1000,decision:{}});
  return {status:200,headers:{},body:{scope:{serverId:viewer.serverId,viewerFence:'a'.repeat(64)},observedAt:new Date().toISOString(),serverName:'Synthetic test server',streams:{count:streamCount,convertingCount:0,overflow:{count:Math.max(0,streamCount-3),convertingCount:0},items:Array.from({length:Math.min(3,streamCount)},(_,i)=>session(String(i)))},scans:[],tasks:{count:0,items:[]},update:{available:false,state:'unconfigured',current:''},health:{degraded:false},majorIssues:[]}};
}, request: async () => {proof.requests++; return structuredClone(document);}, getRouteSnapshot: () => ({phase: 'ready'}), subscribeRoute: () => () => {}};
const service = new HomeService(api as any);
await service.load();
const activity = createOwnerActivity({api, events, epoch: 1, facts:{serverName:'Synthetic test server',identity:{capabilities:{manageServer:true}}}, session:()=>({viewer,sessionFamilyId:'synthetic-family'}),isCurrent:()=>true} as any, {t:()=>'',locale:'en-US',timeZone:'UTC'} as any, ()=>{void router.navigate({to:'/dashboard'});},()=>{},destination=>{void destination;});
const defaultLibraries = [{id:'movies',name:'Movies',kind:'movie'},{id:'tv',name:'TV Shows',kind:'tv'},{id:'anime',name:'Anime',kind:'anime'},{id:'music',name:'Music',kind:'music'},{id:'audio',name:'Audiobooks',kind:'audiobook'},...Array.from({length:10},(_,i)=>({id:'library-'+i,name:'Test library '+i,kind:'movie'}))];
const titleCase = new URLSearchParams(window.location.search).get('libraryTitles');
const libraries = titleCase === 'short' ? defaultLibraries.slice(0, 2).map((library, i) => ({...library, name: ['A', 'B'][i]}))
  : titleCase === 'long' ? defaultLibraries.slice(0, 3).map(library => ({...library, name: `Test ${library.name} library with an intentionally long complete title for the desktop navigation overflow regression`}))
  : defaultLibraries;
const bindings = {api, events, scopeKey: 'synthetic-viewer', shell: {
  identity: {viewer, capabilities:{manageServer:true}, username: 'test-user', serverName: 'Synthetic test server', profileName: 'Test profile', profiles: [], canManageProfiles: false, accountFacts: [],
    libraries, liveAvailable: true, unread: 3, showNotificationDot: true},
  OwnerActivity: activity.OwnerActivity, dockVisible: false, onSignOut: () => {proof.signOut++;}, overlays: <RetainedState />,
}, home: {service, artwork: {foreground: () => undefined, ambient: () => undefined},
  time: {locale: 'en-US', timeZone: 'UTC'}, reducedMotion: true,
  effects: {play: async () => {proof.plays++;}, playChannel: async () => {}, openEntry() {}, openSeeAll() {}, openMore: () => {proof.more++;}, setMyList: async () => {}, reportError: (error: unknown) => proof.errors.push(String(error))},
  selection: {toolbar: null, card: () => undefined, channel: () => undefined, collection: () => undefined, registerWindow: () => undefined, registerSpotlight: () => undefined, moreAvailable: () => true, moreExpanded: () => false}}, playback: undefined,
} as unknown as ShellBindings;
(window as any).sizeShellProof = {proof, bindings, service, viewer, router, setStreamCount:(count:number,error=false)=>{streamCount=count;failActivity=error;for(const fn of hints.get('admin.sessions')??[])fn();}};
router.update({history: createMemoryHistory({initialEntries: ['/']})});
createRoot(window.document.getElementById('root')!).render(<App binding={bindings} />);
