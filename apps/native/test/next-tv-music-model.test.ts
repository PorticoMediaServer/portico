import {DetailService} from '../../../packages/client-core/src/detail.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';
import {readListeningSimilar} from '../../../packages/client-core/src/library-content.ts';
import * as readingBooksOpen from '../src-next/screens/read/reading-books-open.ts';
import * as musicPaging from '../src-next/screens/destinations/music/paging.ts';
import {presentError} from '../../../packages/client-core/src/presentation/errors.ts';
import * as titleRelated from '../src-next/screens/watch/title-related.ts';

const song = (id: string, track: number, duration: number, title: string) => ({id, libraryId: 'music', kind: 'song', title, duration, trackNumber: track, artist: {id: 'artist-1', name: 'Kendrick Lamar'}, album: {id: 'album-1', name: 'DAMN.'}});
const albumEntry = {id: 'album-1', libraryId: 'music', kind: 'album', title: 'DAMN.', artist: {id: 'artist-1', name: 'Kendrick Lamar'}, year: 2017, posterUrl: '/v1/items/a/art/poster'};
const artistEntry = {id: 'artist-1', libraryId: 'music', kind: 'artist', title: 'Kendrick Lamar', country: 'US', activeBeginYear: 2004, genres: ['Hip-Hop']};

class Builder {
  body: any;
  constructor() { this.body = {}; }
  where(field: string, operator: string, value: string) { this.body.query = {field, operator, value}; return this; }
  sort(field: string, direction: string) { this.body.sort = [{field, direction}]; return this; }
  cursor(value: string) { this.body.cursor = value; return this; }
  limit(value: number) { this.body.limit = value; return this; }
  build() { return {pivot: 'songs', ...this.body}; }
}

type Fixture = {songs?: readonly any[]; total?: number; similar?: readonly any[]; albums?: readonly any[]; maximumLimit?: number; pageSize?: number; current?: () => boolean; onPage?: (start: number) => void; revision?: (start: number) => string; lowerBound?: boolean; reuseRows?: boolean; browseError?: (start:number) => Error | undefined;waitPage?:(start:number)=>Promise<void>|undefined;nextCursor?:(start:number,next:string)=>string;albumPageSize?:number;onAlbumPage?:(start:number)=>void; similarError?: () => Error | undefined; capabilitiesError?: () => Error | undefined; artistDetails?:any;bySound?:boolean;similarResponse?:unknown;waitSimilar?:Promise<void>;onSimilar?:()=>void;entityMissing?:boolean;entityRevision?:{catalog:number;viewer:number};waitEntity?:Promise<void>;onEntity?:()=>void};
async function controller(entry: any, options: Fixture = {}) {
  const songs = options.songs ?? [], total = options.total ?? songs.length, similar = options.similar ?? [], albums = options.albums ?? [];
  const calls:any[]=[],browseBodies:any[]=[],reads:string[]=[];
  const similarScope={serverId:'server',authority:'local' as const,accountId:'account',profileId:'profile',controllerId:'v1',controllerEpoch:'v1',commandLaneId:'v1'};
  const revision=options.entityRevision??{catalog:7,viewer:3};
  const services:FakeLibraryContentService[]=[];
  class FakeLibraryContentService {
    projection:any;disposed=false;
    constructor(){this.projection=options.entityMissing?null:{entity:options.artistDetails??entry,scope:{serverId:'server',libraryId:'music',libraryKind:'music',view:entry.kind,entityId:entry.id,viewerFence:'real-projection-viewer'},revision};services.push(this);}
    select=async()=>{options.onEntity?.();await options.waitEntity;};
    getSnapshot=()=>({phase:this.disposed?'error':'ready',projection:this.projection});
    dispose=()=>{this.disposed=true;};
  }
  const playback = {cataloguePlayback: () => ({playSegments: (segments: any, start: any) => {calls.push({segments, options: start}); return Promise.resolve({id: 'queue'});}})};
  const api = {request: async (path:any,_method?:any,body?:any)=>{
    reads.push(String(path));
    if (String(path).startsWith('cap:')) {
      const failure = options.capabilitiesError?.();
      if (failure) throw failure;
      return {pivots: [{id: 'songs'}], fields: [{id: 'memberOf'}], sorts: [{id: 'groupOrder'}], queryLimits: {maximumLimit: options.maximumLimit ?? 200}};
    }
    if (String(path).startsWith('browse:')) {
      const start = body?.cursor ? Number(body.cursor) : 0;
      browseBodies.push(JSON.parse(JSON.stringify(body)));
      await options.waitPage?.(start);
      const failure = options.browseError?.(start);
      if (failure) throw failure;
      const pageSize = options.pageSize ?? songs.length;
      const rows = songs.slice(start, start + pageSize).map(row=>options.reuseRows?row:({...row}));
      const nextCursor = start + pageSize < total ? String(start + pageSize) : '';
      options.onPage?.(start);
      return {pivot: 'songs', applied: {query: null, sort: []}, pageInfo: {start, total, revision: options.revision?.(start) ?? 'r', hasMore: nextCursor !== '', nextCursor:options.nextCursor?.(start,nextCursor)??nextCursor, ...(options.lowerBound ? {totalIsLowerBound: true} : {})}, entries: rows};
    }
    if (String(path).includes('albums')) {const u=new URL(path,'https://server.test'),start=Number(u.searchParams.get('cursor')??0),limit=options.albumPageSize??100;options.onAlbumPage?.(start);return {albums:albums.slice(start,start+limit),nextCursor:start+limit<albums.length?String(start+limit):''};}
    if (String(path).includes('similar')) {
      options.onSimilar?.();await options.waitSimilar;
      const failure = options.similarError?.();
      if (failure) throw failure;
      return options.similarResponse??{scope:{serverId:'server',authority:'local',accountId:'account',profileId:'profile'},data:{itemId:entry.id,libraryId:'music',revision,bySound:options.bySound??true,entries:similar}};
    }
    if (String(path).includes('/personal-state')) return {favorite: true};
    throw new Error(`unexpected ${path}`);
  }};
  const {NativeTVMusicController} = await screenModule(new URL('../src-next/screens/destinations/music/model.ts', import.meta.url), {
    '../../watch/title-related': titleRelated,
    './paging': musicPaging,
    '../../../../../../packages/client-core/src/detail': {DetailService},
    react: {default: {}},
    '../../../shell/bootstrap': {default: {}},
    '../../../shell/playback': {default: {}},
    './lifetime': {default: {}, nativeDestinationLifetime: () => ({scope: {serverId: 'server', viewerId: 'viewer'}, current: () => true})},
    '../../../../../../packages/client-core/src/library-content': {default: {}, readListeningSimilar,validateContentEntry:(value:any)=>value,LibraryContentService: FakeLibraryContentService},
    '../../../../../../packages/client-core/src/browse': {default: {}, BrowseQueryBuilder: Builder, browseCapabilitiesPath: (library: string, pivot: string) => `cap:${library}:${pivot}`, browsePath: (library: string) => `browse:${library}`,
      parseBrowseCapabilities: (value: any) => value, parseBrowseResult: (value: any) => value},
    '../../../../../../packages/client-core/src/container-state': {default: {}, getContainerState: async (_api: any, _kind: any, _id: any) => ({favorite: true}), setContainerFlag: async () => ({favorite: false})},
    '../../../../../../packages/contracts/src/routes': {default: {}, encodedRoutePath: (id: string, values: {id: string}) => `/v1/${id}/${values.id}`},
    '../../../../../../packages/client-core/src/server-messages': {default: {}, unreadableServerResponse: 'unreadable'},
  });
  const value = new NativeTVMusicController({api, entry,similarScope,lifetime:{scope:{serverId:'server',viewerId:'opaque-content-scope'},current: () => options.current?.() ?? true}, playback});
  return {value,calls,browseBodies,reads,replaceProjection:()=>{const service=services.at(-1)!;service.projection={...service.projection,revision:{catalog:8,viewer:3}};}};
}

test('album page reads its own songs, total, duration and similar albums', async () => {
  const {value} = await controller(albumEntry, {songs: [song('song-1', 1, 180, 'BLOOD.'), song('song-2', 2, 185, 'DNA.')],
    similar: [{kind: 'album', id: 'album-2', libraryId: 'music', title: 'Rumours'}, {kind: 'song', id: 'song-9', libraryId: 'music', title: 'Dreams'}]});
  await value.load();
  const state = value.getSnapshot();
  assert.equal(state.phase, 'ready');
  assert.equal(state.songs.length, 2);
  assert.equal(state.songTotal, 2);
  assert.equal(state.songDurationSeconds, 365);
  assert.deepEqual(state.similar.map((entry: any) => entry.id), ['album-2']);
  assert.equal(state.favorite, true);
  value.dispose();
});

test('artist page reads albums, top songs and similar artists from the server', async () => {
  const {value} = await controller(artistEntry, {songs: [song('song-1', 1, 180, 'DNA.')], total: 4,
    albums: [{id: 'album-1', libraryId: 'music', title: 'Abbey Road', albumArtistId: 'artist-1', albumArtist: 'Kendrick Lamar', year: 2017, posterUrl: '/x'}],
    similar: [{kind: 'artist', id: 'artist-2', libraryId: 'music', title: 'J. Cole'}]});
  await value.load();
  const state = value.getSnapshot();
  assert.equal(state.phase, 'ready');
  assert.equal(state.songTotal, 4);
  assert.equal(state.albums.length, 1);
  assert.equal(state.albums[0]?.artist?.name, 'Kendrick Lamar');
  assert.deepEqual(state.similar.map((entry: any) => entry.id), ['artist-2']);
  value.dispose();
});

test('artist page reads its real entity details for the Details block', async () => {
  const details = {...artistEntry, country: 'US', activeBeginYear: 2004, activeEndYear: 2017, artistType: 'Group',
    overview: 'An American rapper.', overviewSource: 'Wikipedia'};
  const {value} = await controller(artistEntry, {songs: [song('song-1', 1, 180, 'DNA.')], artistDetails: details});
  await value.load();
  await new Promise(resolve => setTimeout(resolve, 0));
  assert.equal(value.getSnapshot().phase, 'ready');
  assert.equal(value.getSnapshot().artistDetails?.artistType, 'Group');
  assert.equal(value.getSnapshot().artistDetails?.overview, 'An American rapper.');
  assert.equal(value.getSnapshot().artistDetails?.country, 'US');
  value.dispose();
});

test('album songs respect the server page cap and continue with the cursor', async () => {
  const many = Array.from({length: 150}, (_, index) => song(`song-${index}`, index + 1, 180, `Track ${index + 1}`));
  const {value, browseBodies} = await controller(albumEntry, {songs: many, total: 150, maximumLimit: 100, pageSize: 100});
  await value.load();
  assert.equal(value.getSnapshot().songs.length,100,'the initial page stays bounded');
  await value.nextSongs(value.getSnapshot().songPage);
  const state = value.getSnapshot();
  assert.equal(state.phase, 'ready');
  assert.equal(state.songs.length, 150);
  assert.equal(state.songTotal, 150);
  assert.equal(state.songDurationSeconds, undefined,'a partial page does not invent whole-album duration');
  assert.deepEqual(browseBodies.map(body => body.limit), [100, 100]);
  assert.deepEqual(browseBodies.map(body => body.cursor ?? null), [null, '100']);
  assert.deepEqual(browseBodies[0], {pivot: 'songs', query: {field: 'memberOf', operator: 'equals', value: 'album-1'},
    sort: [{field: 'groupOrder', direction: 'asc'}], limit: 100});
  value.dispose();
});

test('a plain songs failure publishes a safe code and retries the same page', async () => {
  let fail = true;
  const {value} = await controller(albumEntry, {songs: [song('song-1', 1, 180, 'Come Together')],
    browseError: () => fail ? new Error('transport') : undefined});
  await value.load();
  assert.equal(value.getSnapshot().phase, 'error');
  assert.equal((value.getSnapshot().error as any).code, 'music_album_songs_unavailable');
  assert.equal((value.getSnapshot().error as any).musicRead.read, 'songs');
  assert.equal((value.getSnapshot().error as any).musicRead.method, 'POST');
  fail = false;
  await value.load();
  assert.equal(value.getSnapshot().phase, 'ready');
  assert.equal(value.getSnapshot().songs.length, 1);
  value.dispose();
});

test('a failed More like this read keeps its own stage code', async () => {
  const {value} = await controller(albumEntry, {songs: [song('song-1', 1, 180, 'Come Together')],
    similarError: () => new Error('rail failed')});
  await value.load();
  assert.equal(value.getSnapshot().phase,'ready');
  assert.equal(value.getSnapshot().songs.length,1);
  assert.equal((value.getSnapshot().similarError as any).code,'music_album_similar_unavailable');
  assert.equal((value.getSnapshot().similarError as any).musicRead.read,'similar');
  assert.equal((value.getSnapshot().similarError as any).musicRead.method,'GET');
  value.dispose();
});

test('a failed capability read keeps its own stage code', async () => {
  const {value} = await controller(albumEntry, {songs: [song('song-1', 1, 180, 'Come Together')],
    capabilitiesError: () => new Error('capabilities failed')});
  await value.load();
  assert.equal(value.getSnapshot().phase, 'error');
  assert.equal((value.getSnapshot().error as any).code, 'music_album_capabilities_unavailable');
  assert.equal((value.getSnapshot().error as any).musicRead.read, 'capabilities');
  assert.equal((value.getSnapshot().error as any).musicRead.method, 'GET');
  value.dispose();
});

test('the music failure copy names the title and offers a real retry, never Home', () => {
  const presented = presentError(Object.assign(new Error('x'), {code: 'music_album_songs_unavailable'}), 'detail');
  assert.equal(presented.title, 'This title couldn’t load');
  assert.equal(presented.action, 'try-again');
  assert.notEqual(presented.body, 'Something went wrong. Try again.');
});

test('a retired album lifetime sends no second cursor page', async () => {
  const many = Array.from({length: 150}, (_, index) => song(`song-${index}`, index + 1, 180, `Track ${index + 1}`));
  let live = true;
  const {value, browseBodies} = await controller(albumEntry, {songs: many, total: 150, maximumLimit: 100, pageSize: 100,
    current: () => live, onPage: start => {if (start === 0) live = false;}});
  await value.load();
  assert.equal(browseBodies.length, 1);
  assert.notEqual(value.getSnapshot().phase, 'ready');
  value.dispose();
});

test('album membership follows every server page in canonical order', async () => {
  const many = [song('song-a', 1, 180, 'Disc one'), song('song-b', 2, 181, 'Disc one, two'), song('song-c', 1, 182, 'Disc two'), song('song-d', 2, 183, 'Disc two, two'), song('song-e', 3, 184, 'Disc two, three')];
  const {value, browseBodies} = await controller(albumEntry, {songs: many, total: 5, pageSize: 2, maximumLimit: 2});
  const ids = await value.albumMembership();
  assert.deepEqual(ids, ['song-a', 'song-b', 'song-c', 'song-d', 'song-e']);
  assert.deepEqual(browseBodies.map(body => body.cursor ?? null), [null, '2', '4']);
  assert.deepEqual(browseBodies.map(body => body.limit), [2, 2, 2]);
  assert.deepEqual(browseBodies[0].query, {field: 'memberOf', operator: 'equals', value: 'album-1'});
  assert.deepEqual(browseBodies[0].sort, [{field: 'groupOrder', direction: 'asc'}]);
  value.dispose();
});

test('album membership refuses a source that changes between pages instead of returning a subset', async () => {
  const many = Array.from({length: 4}, (_, index) => song(`song-${index}`, index + 1, 180, `Track ${index + 1}`));
  const {value} = await controller(albumEntry, {songs: many, total: 4, pageSize: 2, maximumLimit: 2, revision: start => start === 0 ? 'r1' : 'r2'});
  await assert.rejects(value.albumMembership(), (error: any) => error.code === 'album_membership_changed');
  value.dispose();
});

test('album membership refuses a lower bound or a short server page', async () => {
  const many = [song('song-0', 1, 180, 'A'), song('song-1', 2, 181, 'B')];
  const bounded = await controller(albumEntry, {songs: many, total: 9, pageSize: 2, maximumLimit: 2, lowerBound: true});
  await assert.rejects(bounded.value.albumMembership(), (error: any) => error.code === 'album_membership_incomplete');
  bounded.value.dispose();
  const short = await controller(albumEntry, {songs: many, total: 3, pageSize: 2, maximumLimit: 2});
  await assert.rejects(short.value.albumMembership(), (error: any) => error.code === 'album_membership_incomplete');
  short.value.dispose();
});

test('album membership refuses a non-song or duplicate member instead of a leaf subset', async () => {
  const mixed = await controller(albumEntry, {songs: [song('song-0', 1, 180, 'A'), {...song('movie-1', 2, 181, 'B'), kind: 'movie'}], total: 2});
  await assert.rejects(mixed.value.albumMembership(), (error: any) => error.code === 'album_membership_incomplete');
  mixed.value.dispose();
  const duplicate = await controller(albumEntry, {songs: [song('song-0', 1, 180, 'A'), song('song-0', 2, 181, 'A again')], total: 2});
  await assert.rejects(duplicate.value.albumMembership(), (error: any) => error.code === 'album_membership_incomplete');
  duplicate.value.dispose();
});

test('album membership stops sending when the destination retires', async () => {
  const many = Array.from({length: 4}, (_, index) => song(`song-${index}`, index + 1, 180, `Track ${index + 1}`));
  let live = true;
  const {value, browseBodies} = await controller(albumEntry, {songs: many, total: 4, pageSize: 2, maximumLimit: 2, current: () => live,
    onPage: start => {if (start === 0) live = false;}});
  await assert.rejects(value.albumMembership(), (error: any) => error.code === 'native_music_retired');
  assert.equal(browseBodies.length, 1);
  value.dispose();
});

test('artist All songs follows the artist filter to completion in server order', async () => {
  const many = [song('song-a', 1, 180, 'A'), song('song-b', 2, 181, 'B'), song('song-c', 3, 182, 'C')];
  const {value, browseBodies} = await controller(artistEntry, {songs: many, total: 3, pageSize: 2, maximumLimit: 2});
  await value.loadAllSongs();
  assert.equal(value.getSnapshot().allSongs.songs.length,2);
  await value.nextAllSongs(value.getSnapshot().allSongs);
  assert.equal(value.getSnapshot().allSongs.phase, 'ready');
  assert.deepEqual(value.getSnapshot().allSongs.songs.map((entry: any) => entry.id), ['song-a', 'song-b', 'song-c']);
  assert.equal(value.getSnapshot().allSongs.total, 3);
  assert.deepEqual(browseBodies[0].query, {field: 'artist', operator: 'equals', value: 'Kendrick Lamar'});
  assert.deepEqual(browseBodies[0].sort, [{field: 'title', direction: 'asc'}]);
  assert.deepEqual(browseBodies.map(body => body.cursor ?? null), [null, '2']);
  value.dispose();
});

test('artist All songs refuses a short page instead of calling it all', async () => {
  const many = [song('song-a', 1, 180, 'A'), song('song-b', 2, 181, 'B')];
  const {value} = await controller(artistEntry, {songs: many, total: 3, pageSize: 2, maximumLimit: 2});
  await value.loadAllSongs();
  await value.nextAllSongs(value.getSnapshot().allSongs);
  assert.equal(value.getSnapshot().allSongs.phase, 'refresh-required');
  assert.equal((value.getSnapshot().allSongs.error as any).code, 'stale_continuation');
  value.dispose();
});

test('play starts the album or artist container through the shared queue player', async () => {
  const {value, calls} = await controller(albumEntry, {songs: [song('song-1', 1, 180, 'BLOOD.')]});
  await value.load();
  await value.play('song-1');
  await value.play(undefined, true);
  assert.equal(calls.length, 2);
  assert.deepEqual(calls[0].segments[0].source, {container: {kind: 'album', id: 'album-1'}});
  assert.deepEqual(calls[0].segments[0].anchor, {itemId: 'song-1'});
  assert.deepEqual(calls[1].segments[0].order, {mode: 'shuffle'});
  assert.equal(typeof calls[0].options.isCurrent, 'function');
  assert.ok(calls[0].options.signal instanceof AbortSignal);
  value.dispose();
});


test('Sonic Similar requests sound explicitly and preserves the server fallback flag under the real entity revision',async()=>{
 for(const bySound of [true,false]){
  const f=await controller(albumEntry,{songs:[song('song-1',1,180,'Track')],similar:[{kind:'album',id:'other',libraryId:'music',title:'Other'}],bySound,revision:()=> 'opaque-browse-revision'});
  await f.value.load();assert.equal(f.value.getSnapshot().phase,'ready');assert.equal(f.value.getSnapshot().similarBySound,bySound);
  assert(f.reads.some(path=>path.includes('/similar?')&&new URL(path,'https://server.test').searchParams.get('by')==='sound'));
  assert.deepEqual(f.value.getSnapshot().similarProjection.revision,{catalog:7,viewer:3});f.value.dispose();
 }
});
test('wrong original scope, item, library or accepted entity revision cannot publish recommendations or block own songs',async()=>{
 const scope={serverId:'server',authority:'local',accountId:'account',profileId:'profile'},data={itemId:'album-1',libraryId:'music',revision:{catalog:7,viewer:3},bySound:true,entries:[{kind:'album',id:'other',libraryId:'music',title:'Other'}]};
 for(const response of [{scope:{...scope,profileId:'other'},data},{scope:{...scope,serverId:'other'},data},{scope,data:{...data,itemId:'other'}},{scope,data:{...data,libraryId:'other'}},{scope,data:{...data,revision:{catalog:8,viewer:3}}},{scope,data:{...data,revision:{catalog:7,viewer:4}}}]){
  const f=await controller(albumEntry,{songs:[song('song-1',1,180,'Track')],similarResponse:response});await f.value.load();
  assert.equal(f.value.getSnapshot().phase,'ready');assert.equal(f.value.getSnapshot().songs.length,1);assert.deepEqual(f.value.getSnapshot().similar,[]);assert.ok(f.value.getSnapshot().similarError);f.value.dispose();
 }
});
test('a missing entity projection withholds Similar instead of manufacturing a revision from browse',async()=>{
 const f=await controller(albumEntry,{songs:[song('song-1',1,180,'Track')],entityMissing:true});await f.value.load();
 assert.equal(f.reads.some(path=>path.includes('similar')),false);assert.equal(f.value.getSnapshot().phase,'ready');assert.equal(f.value.getSnapshot().songs.length,1);assert.ok(f.value.getSnapshot().similarError);f.value.dispose();
});
test('retained same-ID recommendation actions retire with their original projection and destination',async()=>{
 let current=true;const f=await controller(albumEntry,{similar:[{kind:'album',id:'other',libraryId:'music',title:'Other'}],current:()=>current});await f.value.load();
 const first=f.value.getSnapshot(),entry=first.similar[0];assert.equal(f.value.similarCurrent(first,entry),true);assert.equal(f.value.contains(entry),true);
 f.replaceProjection();assert.equal(f.value.similarCurrent(first,entry),false);assert.equal(f.value.contains(entry),false);
 await f.value.load();assert.equal(f.value.similarCurrent(first,entry),false);assert.equal(f.value.contains(entry),false);
 const next=f.value.getSnapshot();assert.equal(f.value.similarCurrent(next,next.similar[0]),true);current=false;assert.equal(f.value.similarCurrent(next,next.similar[0]),false);f.value.dispose();
});
test('held Similar responses cannot cross destination or entity-projection retirement',async()=>{
 for(const retireProjection of [false,true]){
  let release!:()=>void,started!:()=>void,current=true;const held=new Promise<void>(resolve=>{release=resolve;}),entered=new Promise<void>(resolve=>{started=resolve;});
  const f=await controller(artistEntry,{waitSimilar:held,onSimilar:started,current:()=>current,similar:[{kind:'artist',id:'other',libraryId:'music',title:'Other'}]});
  const loading=f.value.load();await entered;if(retireProjection)f.replaceProjection();else current=false;release();await loading;
  assert.deepEqual(f.value.getSnapshot().similar,[]);f.value.dispose();
 }
});

test('held entity details never delay the artist pages own songs and releases',async()=>{
 let release!:()=>void,arrived!:()=>void;
 const held=new Promise<void>(resolve=>{release=resolve;}),entered=new Promise<void>(resolve=>{arrived=resolve;});
 const f=await controller(artistEntry,{songs:[song('song-1',1,180,'Track')],albums:[{id:'album-9',libraryId:'music',title:'LP'}],
   waitEntity:held,onEntity:arrived,similar:[{kind:'artist',id:'other',libraryId:'music',title:'Other'}]});
 const loading=f.value.load();
 await entered;
 const early=f.value.getSnapshot();
 assert.equal(early.phase,'ready');
 assert.equal(early.songs.length,1);
 assert.deepEqual(early.albums.map((value:any)=>value.id),['album-9']);
 assert.equal(early.artistDetails,undefined);
 assert.deepEqual(early.similar,[]);
 assert.equal(f.browseBodies.length,1,'the artist song request completed before the projection');
 release();await loading;
 const done=f.value.getSnapshot();
 assert.equal(done.artistDetails?.id,'artist-1');
 assert.equal(done.similar.length,1);
 assert.equal(done.similarError,undefined);
 f.value.dispose();
});

test('a late entity projection and Similar cannot cross a retired route',async()=>{
 let release!:()=>void,arrived!:()=>void,current=true;
 const held=new Promise<void>(resolve=>{release=resolve;}),entered=new Promise<void>(resolve=>{arrived=resolve;});
 const f=await controller(artistEntry,{songs:[song('song-1',1,180,'Track')],waitEntity:held,onEntity:arrived,current:()=>current,
   similar:[{kind:'artist',id:'other',libraryId:'music',title:'Other'}]});
 const loading=f.value.load();
 await entered;
 current=false;release();await loading;
 const retired=f.value.getSnapshot();
 assert.equal(retired.artistDetails,undefined);
 assert.deepEqual(retired.similar,[]);
 assert.equal(f.reads.some(path=>path.includes('similar')),false,'a retired route makes no Similar request');
 f.value.dispose();
});

test('the renderer keeps one page per exact route object and navigation revision',async()=>{
 let factory!:(context:any)=>any; let made:any[]=[], navBacks=0, current=true;
 class FakeController {
  options:any;loads=0;disposed=false;
  constructor(options:any){this.options=options;made.push(this);}
  getSnapshot=()=>({phase:'idle',songs:[],albums:[],similar:[],favorite:false,allSongs:{phase:'idle',songs:[]}});
  subscribe=()=>()=>{};contains=()=>true;similarCurrent=()=>true;
  load(){this.loads++;return Promise.resolve();}
  albumMembership=async()=>[];
  dispose(){this.disposed=true;}
 }
 let pickerRenders=0, pickerDisposals=0;
 const picker={render:(runtime:any)=>{pickerRenders++;return {type:'PickerOutlet',props:{runtime}};},dispose:()=>{pickerDisposals++;},openAlbum:()=>{}};
 await screenModule(new URL('../src-next/screens/destinations/music/register.tsx',import.meta.url),{
  './selection': {createNativeMusicSelection: () => ({dispose() {}, render() {return null;}})}, '../../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', Artwork: 'Artwork'},
  react:{default:React},'react-native':{Platform:{isTV:false}},
  '../../../shell/route-registry':{nativeShellRoutes:{registerRoute:(_kind:string,value:any)=>{factory=value;}}},
  '../lifetime':{nativeDestinationLifetime:()=>({scope:{serverId:'server',viewerId:'viewer'},current:()=>current})},
  '../entry-actions':{createNativeEntryActions:()=>({menu:()=>{},dispose:()=>{},isMenuOpen:()=>false})},
  './model':{NativeTVMusicController:FakeController},
  './TVMusicScreens':{TVAlbumScreen:'TVAlbumScreen',TVArtistScreen:'TVArtistScreen'},
  './PhoneMusicScreens':{PhoneAlbumScreen:'PhoneAlbumScreen',PhoneArtistScreen:'PhoneArtistScreen'},
  './MusicPlaylistPicker':{createMusicPlaylistPicker:()=>picker},
  '../../watch/download-request':{nativeDownloadRequest:()=>undefined},
  '../../downloads/request':{askNativeDownload:()=>{}},
  '../../native-screen-services':{nativeScreenServices:()=>({requestId:async()=> '00000000-0000-4000-8000-000000000001'})},
  '../../selection/native-selection':{catalogueSelectionSource:(source:unknown)=>source,createNativeSelection:()=>({render:()=>null,dispose(){}})},
 });
 const viewer={serverId:'server',authority:'local',accountId:'account',profileId:'profile'};
 const routeA={kind:'music',entry:artistEntry},routeB={kind:'music',entry:artistEntry};
 const navigation:any={route:routeA,revision:1};
 const context={api:{request:async()=>{throw new Error('rendering must not read')}},session:()=>({viewer}),
  authority:()=>({serverId:'server',accountId:'account',profileId:'profile'}),current:()=>current,
  navigation:{getSnapshot:()=>navigation,open:()=>{},back:()=>{navBacks++;}}};
 const renderer=factory(context);
 const first=renderer.render(routeA,{});
 assert.equal(first.type,React.Fragment,'a non-TV page and its route-owned picker share one fragment');
 const firstPage=first.props.children[0], firstPicker=first.props.children[1];
 assert.equal(firstPage.type,'PhoneArtistScreen');
 assert.equal(firstPicker.type,'PickerOutlet','the same page picker is mounted for a non-TV route');
 assert.equal(pickerRenders,1);
 assert.equal(made.length,1);
 assert.equal(firstPage.props.controller,made[0]);
 assert.deepEqual(made[0].options.similarScope,{serverId:'server',authority:'local',accountId:'account',profileId:'profile',controllerId:'v1',controllerEpoch:'v1',commandLaneId:'v1'});
 assert.equal(made[0].options.lifetime.current(),true);
 const reused=renderer.render(routeA,{});
 assert.equal(reused.props.children[0].props.controller,made[0],'the same route object and revision reuse the one page');
 assert.equal(pickerRenders,2,'one page still has exactly one picker outlet per render');
 navigation.revision=2;
 const second=renderer.render(routeA,{});
 assert.equal(made.length,2);
 assert.equal(second.props.children[0].props.controller,made[1]);
 firstPage.props.onBack();
 assert.equal(navBacks,0,'the retired revision cannot navigate');
 second.props.children[0].props.onBack();
 assert.equal(navBacks,1);
 assert.equal(renderer.render(routeB,{}),null,'a different route object is not this page');
 navigation.route=routeB;
 const third=renderer.render(routeB,{});
 assert.equal(made.length,3,'a replaced route object opens a live page, never the dead one');
 assert.equal(third.props.children[0].props.controller,made[2]);
 current=false;
 assert.equal(renderer.render(routeB,{}),null);
 renderer.dispose();
 assert.ok(made[2].disposed);
 assert.ok(pickerDisposals>=2,'each retired page disposes its own picker');
});

test('a non-TV album mounts its own picker outlet once beside the page',async()=>{
 let factory!:(context:any)=>any; let made:any[]=[], pickerRenders=0, pickerDisposals=0;
 class FakeController {
  options:any;loads=0;disposed=false;
  constructor(options:any){this.options=options;made.push(this);}
  getSnapshot=()=>({phase:'ready',songs:[],albums:[],similar:[],favorite:false,allSongs:{phase:'idle',songs:[]}});
  subscribe=()=>()=>{};contains=()=>true;similarCurrent=()=>true;
  load(){this.loads++;return Promise.resolve();}
  albumMembership=async()=>[{id:'song-1'}];
  dispose(){this.disposed=true;}
 }
 const picker={render:(runtime:any)=>{pickerRenders++;return {type:'PickerOutlet',props:{runtime}};},dispose:()=>{pickerDisposals++;},openAlbum:()=>{}};
 await screenModule(new URL('../src-next/screens/destinations/music/register.tsx',import.meta.url),{
  './selection': {createNativeMusicSelection: () => ({dispose() {}, render() {return null;}})}, '../../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', Artwork: 'Artwork'},
  react:{default:React},'react-native':{Platform:{isTV:false}},
  '../../../shell/route-registry':{nativeShellRoutes:{registerRoute:(_kind:string,value:any)=>{factory=value;}}},
  '../lifetime':{nativeDestinationLifetime:()=>({scope:{serverId:'server',viewerId:'viewer'},current:()=>true})},
  '../entry-actions':{createNativeEntryActions:()=>({menu:()=>{},dispose:()=>{},isMenuOpen:()=>false})},
  './model':{NativeTVMusicController:FakeController},
  './TVMusicScreens':{TVAlbumScreen:'TVAlbumScreen',TVArtistScreen:'TVArtistScreen'},
  './PhoneMusicScreens':{PhoneAlbumScreen:'PhoneAlbumScreen',PhoneArtistScreen:'PhoneArtistScreen'},
  './MusicPlaylistPicker':{createMusicPlaylistPicker:()=>picker},
  '../../watch/download-request':{nativeDownloadRequest:()=>undefined},
  '../../downloads/request':{askNativeDownload:()=>{}},
  '../../native-screen-services':{nativeScreenServices:()=>({requestId:async()=> '00000000-0000-4000-8000-000000000001'})},
  '../../selection/native-selection':{catalogueSelectionSource:(source:unknown)=>source,createNativeSelection:()=>({render:()=>null,dispose(){}})},
 });
 const viewer={serverId:'server',authority:'local',accountId:'account',profileId:'profile'};
 const route={kind:'music',entry:{...albumEntry,id:'album-1',kind:'album'}};
 const navigation:any={route,revision:1};
 const context={api:{request:async()=>{throw new Error('rendering must not read')}},session:()=>({viewer}),
  authority:()=>({serverId:'server',accountId:'account',profileId:'profile'}),current:()=>true,
  navigation:{getSnapshot:()=>navigation,open:()=>{},back:()=>{}}};
 const renderer=factory(context);
 const tree=renderer.render(route,{});
 assert.equal(tree.type,React.Fragment);
 assert.equal(tree.props.children[0].type,'PhoneAlbumScreen');
 assert.equal(tree.props.children[1].type,'PickerOutlet','the album page picker is the only outlet');
 assert.equal(pickerRenders,1,'exactly one picker per route render');
 renderer.dispose();
 assert.equal(pickerDisposals,1,'route disposal retires the picker');
});


test('album and All songs cross twenty-one small pages with bounded chunks and exact Previous windows',async()=>{
 for(const entry of [albumEntry,artistEntry]){
  const songs=Array.from({length:42},(_,i)=>song(`song-${i}`,i+1,180,`Track ${i}`));
  const f=await controller(entry,{songs,maximumLimit:2,pageSize:2});
  const all=entry.kind==='artist';if(all)await f.value.loadAllSongs();else await f.value.load();
  const page=()=>all?f.value.getSnapshot().allSongs:f.value.getSnapshot().songPage;
  const next=(p:any)=>all?f.value.nextAllSongs(p):f.value.nextSongs(p);
  const previous=(p:any)=>all?f.value.previousAllSongs(p):f.value.previousSongs(p);
  assert.equal(f.browseBodies.length,1);const seen=new Set<string>();
  while(true){const p=page();for(const row of p.items)seen.add(row.id);assert(p.items.length<=6);assert.equal(p.total,42);if(!p.canNext)break;const before=p;const a=next(before),b=next(before);assert.equal(a,b,'duplicate tail commands share the original operation');await a;}
  assert.equal(seen.size,42);assert.equal(f.browseBodies.length,21);assert.equal(page().offset,36);
  const last=page();await next(last);assert.equal(f.browseBodies.length,21,'terminal cursor never loops');
  await previous(page());assert.equal(page().offset,34);assert.deepEqual(page().items.map((s:any)=>s.id),songs.slice(34,40).map(s=>s.id));
  f.value.dispose();
 }
});

test('301 releases stay bounded, preserve role/order and omit invented album counts',async()=>{
 const albums=Array.from({length:301},(_,i)=>({id:`album-${i}`,libraryId:'music',title:`Release ${i}`,role:i%2?'ep':'album'}));
 const pages:number[]=[];const f=await controller(artistEntry,{albums,onAlbumPage:start=>pages.push(start)});await f.value.load();
 assert.deepEqual(pages,[0]);const seen=new Map<string,any>();
 while(true){const p=f.value.getSnapshot().releases;for(const a of p.items)seen.set(a.id,a);assert(p.items.length<=300);assert.equal(p.total,undefined);if(!p.canNext)break;await f.value.nextReleases(p);}
 assert.deepEqual([...seen.keys()],albums.map(a=>a.id));assert.deepEqual(pages,[0,100,200,300]);assert.equal(seen.get('album-300').role,'album');
 await f.value.previousReleases(f.value.getSnapshot().releases);assert.equal(f.value.getSnapshot().releases.offset,0);f.value.dispose();
});

test('failed continuation keeps rows and retries its captured cursor while replacement and revision changes refuse old work',async()=>{
 let fail=true;let live=true;
 const songs=Array.from({length:6},(_,i)=>song(`song-${i}`,i+1,180,`Track ${i}`));
 const f=await controller(albumEntry,{songs,maximumLimit:2,pageSize:2,current:()=>live,browseError:start=>start===2&&fail?new TypeError('offline'):undefined});
 await f.value.load();const first=f.value.getSnapshot().songPage;const offered=first.items[0];
 await f.value.nextSongs(first);const error=f.value.getSnapshot().songPage;
 assert.equal(error.phase,'error');assert.equal(error.items[0],offered);assert.equal(f.value.contains(offered),true);
 fail=false;await f.value.retrySongs(error);assert.deepEqual(f.browseBodies.map(b=>b.cursor??null),[null,'2','2']);
 const replacement=f.value.getSnapshot().songPage;await f.value.nextSongs(first);assert.equal(f.browseBodies.length,3,'retired section command cannot append');
 live=false;await f.value.nextSongs(replacement);assert.equal(f.browseBodies.length,3);assert.equal(f.value.contains(offered),false);f.value.dispose();
 const revision=await controller(albumEntry,{songs,maximumLimit:2,pageSize:2,revision:start=>start?'r2':'r1'});await revision.value.load();await revision.value.nextSongs(revision.value.getSnapshot().songPage);assert.equal(revision.value.getSnapshot().songPage.phase,'refresh-required');revision.value.dispose();
});

test('repeated cursors and retired in-flight section responses cannot append',async()=>{
 const songs=Array.from({length:6},(_,i)=>song(`song-${i}`,i+1,180,`Track ${i}`));
 const repeat=await controller(artistEntry,{songs,maximumLimit:2,pageSize:2,nextCursor:(start,next)=>start===2?'2':next});await repeat.value.loadAllSongs();await repeat.value.nextAllSongs(repeat.value.getSnapshot().allSongs);assert.equal(repeat.value.getSnapshot().allSongs.phase,'refresh-required');assert.equal(repeat.browseBodies.length,2);repeat.value.dispose();
 let release!:()=>void,entered!:()=>void;const held=new Promise<void>(r=>{release=r;}),started=new Promise<void>(r=>{entered=r;});
 const f=await controller(albumEntry,{songs,maximumLimit:2,pageSize:2,waitPage:start=>{if(start===2){entered();return held;}}});await f.value.load();const next=f.value.nextSongs(f.value.getSnapshot().songPage);await started;await f.value.load();const refreshed=f.value.getSnapshot().songPage;release();await next;assert.equal(f.value.getSnapshot().songPage,refreshed);assert.equal(refreshed.items.length,2);f.value.dispose();
});

test('All songs admits only live exact offered entries beyond Top songs and retires them before replacement',async()=>{
 const songs=Array.from({length:6},(_,i)=>song(`song-${i}`,i+1,180,`Track ${i}`));
 const f=await controller(artistEntry,{songs,maximumLimit:3,pageSize:3});await f.value.load();const top=f.value.getSnapshot().songs[0];
 await f.value.loadAllSongs();await f.value.nextAllSongs(f.value.getSnapshot().allSongs);const sixth=f.value.getSnapshot().allSongs.songs[5];
 assert.equal(f.value.contains(sixth),true);assert.equal(f.value.contains({...sixth}),false);assert.equal(f.value.contains(top),true);
 await f.value.loadAllSongs();assert.equal(f.value.contains(sixth),false);f.value.dispose();assert.equal(f.value.contains(top),false);
});

test('the real All songs row invokes the borrowed menu once and its captured membership retires',async()=>{
 const songs=Array.from({length:6},(_,i)=>song(`song-${i}`,i+1,180,`Track ${i}`));
 const f=await controller(artistEntry,{songs,pageSize:3,maximumLimit:3,albums:[{id:'offered-album',libraryId:'music',title:'Original release'}]});await f.value.load();await f.value.loadAllSongs();await f.value.nextAllSongs(f.value.getSnapshot().allSongs);
 let opens=0,commandCurrent:()=>boolean=()=>false;
 const borrowed={subscribe:()=>()=>{},isOpen:()=>false,open:async(_entry:any,current:()=>boolean)=>{opens++;commandCurrent=current;}};
 const {createNativeEntryActions}=await screenModule(new URL('../src-next/screens/destinations/entry-actions.ts',import.meta.url),{
  react:{default:{}},'../native-screen-services':{nativeScreenServices:()=>({reportError:()=>{throw Error('unexpected error');}})},
  '../read/reading-books-open':readingBooksOpen,
  '../watch':{nativeWatchEntryMenu:()=>borrowed},'./lifetime':{nativeDestinationLifetime:()=>({current:()=>true})},
 });
 const actions=createNativeEntryActions({navigation:{getSnapshot:()=>({revision:1}),subscribe:()=>()=>{}},subscribeAuthority:()=>()=>{}},
  {current:()=>true,contains:f.value.contains,subscribe:f.value.subscribe});
 const screens=await tvMusicScreens();
 let navigations=0;const render=()=>screens.TVArtistScreen({controller:f.value,entry:artistEntry,runtime:{text:{},ambient:()=>undefined,artwork:()=>undefined,reportError:()=>{}},actions,openEntry:()=>{navigations++;}});const tree=render();
 const nodes:any[]=[];function walk(node:any){if(!node)return;if(Array.isArray(node)){node.forEach(walk);return;}if(typeof node!=='object')return;if(typeof node.type==='function'){walk(node.type(node.props));return;}nodes.push(node);walk(node.props?.children);}walk(tree);
 const sixth=nodes.find(n=>n.type==='TrackRow'&&n.props.row.id==='song-5');assert(sixth);sixth.props.onMore();assert.equal(opens,1);assert.equal(commandCurrent(),true);
 const old=f.value.getSnapshot().allSongs.songs[5];actions.menu({...old});assert.equal(opens,1);
 await f.value.loadAllSongs();assert.equal(commandCurrent(),false);sixth.props.onMore();assert.equal(opens,1);
 const card=nodes.find(n=>n.type==='SquareCard'&&n.props.title==='Original release');assert(card);card.props.onMore();assert.equal(opens,2);assert.equal(commandCurrent(),true);
 await f.value.load();assert.equal(commandCurrent(),false);card.props.onMore();card.props.onNavigate();assert.equal(opens,2,'old equal-ID release cannot adopt successor membership');assert.equal(navigations,0);
 nodes.length=0;walk(render());const fresh=nodes.find(n=>n.type==='SquareCard'&&n.props.title==='Original release');fresh.props.onMore();fresh.props.onNavigate();assert.equal(opens,3);assert.equal(navigations,1);
 actions.dispose();f.value.dispose();
});


// Keep pagination callbacks on the real window/scroller; native measurement is
// outside this shallow fixture, so its viewport context remains unmounted.
async function musicWindows(react: any) {
 const windowing = await import('../src-next/screens/destinations/music/music-window.ts');
 return screenModule(new URL('../src-next/screens/destinations/music/MusicWindow.tsx', import.meta.url), {
  react, 'react-native': {View: 'View'}, './music-window': windowing,
  'react-native-reanimated': {default: {ScrollView: 'ScrollView'}, runOnJS: (fn: any) => fn,
   useAnimatedScrollHandler: (fn: any) => (event: any) => fn(event.nativeEvent), useSharedValue: (value: any) => ({value})},
 });
}
const windowHooks = {useMemo: (fn: any) => fn(), useState: (initial: any) => [typeof initial === 'function' ? initial() : initial, () => {}],
 useRef: (value: any) => ({current: value}), useCallback: (fn: any) => fn, useId: () => 'test-gradient',
 createContext: () => ({Provider: 'Provider'}), useContext: () => undefined, useLayoutEffect() {}};

async function phoneSurface(value:any,entry:any,tablet=false){
 const hooks={...windowHooks,createElement:(type:any,props:any,...children:any[])=>({type,props:{...props,children}}),useSyncExternalStore:(_off:any,get:any)=>get(),useRef:(value:any)=>({current:value})};
 const react={default:hooks,...hooks};const {artistReleaseGroups}=await import('../../../packages/client-core/src/presentation/library-tabs.ts');
 const controls=await screenModule(new URL('../src-next/screens/destinations/music/MusicSectionControls.tsx',import.meta.url),{'../../../../../../packages/client-core/src/presentation/errors':{presentError},react,'react-native':{View:'View'},'../../../../../../packages/i18n/src':{defaultI18n:{t:(key:string)=>key}},'../../../kit/controls':{Button:'Button'},'./MusicErrorView':{MusicErrorView:'MusicErrorView'}});
 const module=await screenModule(new URL('../src-next/screens/destinations/music/PhoneMusicScreens.tsx',import.meta.url),{
  './MusicWindow': await musicWindows(react), './use-playing-item': {usePlayingItem: () => undefined}, '../../../kit/selection': {SelectionCircle: 'SelectionCircle'},
  'react-native-svg': {default:'Svg',Defs:'Defs',LinearGradient:'LinearGradient',Rect:'Rect',Stop:'Stop'},
  react,'react-native':{ScrollView:'ScrollView',Text:'Text',View:'View'},'react-native-safe-area-context':{useSafeAreaInsets:()=>({top:0,bottom:0,left:0,right:0})},
  '../../../../../../packages/i18n/src':{defaultI18n:{t:(key:string)=>key,number:String}},
  '../../../../../../packages/client-core/src/presentation/kit-model':{cardText:(e:any)=>({title:e.title,caption:[],detail:[]})},
  '../../../../../../packages/client-core/src/presentation/library-tabs':{artistReleaseGroups},
  '../../../kit/artwork':{AmbientArt:'AmbientArt',Artwork:'Artwork',ArtistCircle:'ArtistCircle'},'../../../kit/controls':{Button:'Button',Chip:'Chip',Focusable:'Focusable'},
  '../../../kit/cards':{SquareCard:'SquareCard',CircleCard:'CircleCard'},'../../../kit/Icon':{Icon:'Icon'},'../../../kit/browse/presentation':{browseType:()=>({}),alpha:()=>''},
  '../../../kit/theme':{useTheme:()=>({platform:tablet?'tablet':'phone',colors:{}})},'./MusicErrorView':{MusicErrorView:'MusicErrorView'},'./MusicFormatBadges':{MusicFormatBadges:'MusicFormatBadges'},
  './MusicSectionControls':{MusicSectionControls:'MusicSectionControls',musicNearEnd:controls.musicNearEnd},
 });
 let menus=0,navigations=0;const render=()=>{
  const props={controller:value,entry,runtime:{text:{},ambient:()=>undefined,artwork:()=>undefined,reportError:()=>{}},actions:{menu:(e:any)=>{if(value.contains(e))menus++;}},openEntry:()=>{navigations++;},onBack:()=>{}};
  const tree=entry.kind==='album'?module.PhoneAlbumScreen(props):module.PhoneArtistScreen(props),nodes:any[]=[];
  function walk(node:any){if(!node)return;if(Array.isArray(node)){node.forEach(walk);return;}if(typeof node!=='object')return;if(typeof node.type==='function'){walk(node.type(node.props));return;}nodes.push(node);walk(node.props?.children);}walk(tree);return nodes;
 };
 return {render,counts:()=>({menus,navigations})};
}
const flushMusic=async()=>{for(let n=0;n<24;n++)await Promise.resolve();};
test('phone and tablet album scroll use only the original offered page and preserve real later tracks',async()=>{
 for(const tablet of [false,true]){
  const songs=Array.from({length:6},(_,i)=>song(`song-${i}`,i+1,180,`Track ${i}`));const f=await controller(albumEntry,{songs,pageSize:2,maximumLimit:2});await f.value.load();
  const surface=await phoneSurface(f.value,albumEntry,tablet),scroll=surface.render().find(n=>n.type==='ScrollView'&&!n.props.horizontal&&n.props.onScroll);
  assert(scroll);scroll.props.onScroll({nativeEvent:{contentOffset:{x:0,y:700},layoutMeasurement:{width:400,height:400},contentSize:{width:400,height:1000}}});await flushMusic();
  assert.equal(f.value.getSnapshot().songs.length,4);assert.deepEqual(f.browseBodies.map(b=>b.cursor??null),[null,'2']);
  await f.value.load();const count=f.browseBodies.length;scroll.props.onScroll({nativeEvent:{contentOffset:{x:0,y:700},layoutMeasurement:{width:400,height:400},contentSize:{width:400,height:1000}}});await flushMusic();assert.equal(f.browseBodies.length,count,'old scroll does not adopt the replaced page');f.value.dispose();
 }
});
test('phone/tablet release cards and horizontal tail retain the exact original offered membership',async()=>{
 for(const tablet of [false,true]){
  const albums=Array.from({length:5},(_,i)=>({id:`release-${i}`,libraryId:'music',title:`Release ${i}`,role:i%2?'single':'album'}));
  const f=await controller(artistEntry,{albums,albumPageSize:2});await f.value.load();const surface=await phoneSurface(f.value,artistEntry,tablet),nodes=surface.render(),card=nodes.find(n=>n.type==='SquareCard'&&n.props.title==='Release 0');assert(card);
  card.props.onMore();card.props.onNavigate();assert.deepEqual(surface.counts(),{menus:1,navigations:1});
  const scroll=nodes.find(n=>n.type==='ScrollView'&&n.props.horizontal&&n.props.onScroll);assert(scroll);scroll.props.onScroll({nativeEvent:{contentOffset:{x:500,y:0},layoutMeasurement:{width:400,height:200},contentSize:{width:900,height:200}}});await flushMusic();assert.equal(f.value.getSnapshot().albums.length,4);
  await f.value.load();card.props.onMore();card.props.onNavigate();assert.deepEqual(surface.counts(),{menus:1,navigations:1});f.value.dispose();
 }
});


test('continuation retains the originally dispatched artist query and endpoint rather than reading mutated route metadata',async()=>{
 const original={...artistEntry};const songs=Array.from({length:4},(_,i)=>song(`song-${i}`,i+1,180,`Track ${i}`));
 const f=await controller(original,{songs,pageSize:2,maximumLimit:2});await f.value.loadAllSongs();
 original.title='another artist';original.libraryId='another library';await f.value.nextAllSongs(f.value.getSnapshot().allSongs);
 assert.deepEqual(f.browseBodies.map(body=>body.query.value),['Kendrick Lamar','Kendrick Lamar']);assert.equal(f.reads.filter(path=>path==='browse:music').length,2);f.value.dispose();
});

async function tvMusicScreens(){
 const hooks={...windowHooks,createElement:(type:any,props:any,...children:any[])=>({type,props:{...props,children}}),useSyncExternalStore:(_off:any,get:any)=>get(),useMemo:(fn:any)=>fn(),useRef:(value:any)=>({current:value}),useCallback:(fn:any)=>fn,useId:()=> 'real-test-gradient'};
 const {artistReleaseGroups}=await import('../../../packages/client-core/src/presentation/library-tabs.ts');
 const screens=await screenModule(new URL('../src-next/screens/destinations/music/TVMusicScreens.tsx',import.meta.url),{
  '../../../kit/states': {SkeletonBlock: () => null},
  '../../../kit/selection': {SelectionCircle: 'SelectionCircle'}, '../../../tv/layout/TVShelf': {TVShelf: 'TVShelf'},
  react:{default:hooks,...hooks},'react-native':{ScrollView:'ScrollView',Text:'Text',View:'View'},'react-native-svg':{default:'Svg',Defs:'Defs',LinearGradient:'LinearGradient',Rect:'Rect',Stop:'Stop'},
  '../../../../../../packages/i18n/src':{defaultI18n:{t:(key:string,args?:unknown)=>args?`${key}:${JSON.stringify(args)}`:key,number:String}},
  '../../../../../../packages/client-core/src/presentation/kit-model':{cardText:(e:any)=>({title:e.title,caption:[],detail:[]})},
  '../../../../../../packages/client-core/src/presentation/library-tabs':{artistReleaseGroups},
  '../../../kit/artwork':{AmbientArt:'AmbientArt',Artwork:'Artwork',ArtistCircle:'ArtistCircle'},'../../../kit/controls':{Button:'Button',Chip:'Chip',Focusable:'Focusable'},
  '../../../kit/cards':{SquareCard:'SquareCard'},'../../../kit/browse':{TrackRow:'TrackRow'},'../../../kit/browse/presentation':{browseType:()=>({})},
  '../../../kit/focus':{FocusItem:'FocusItem',FocusRegion:'FocusRegion'},'../../../kit/theme':{useTheme:()=>({colors:{}})},
  '../../../tv/layout/TVArtFocus':{TVArtFocus:'TVArtFocus'},'./MusicErrorView':{MusicErrorView:'MusicErrorView'},'./MusicFormatBadges':{MusicFormatBadges:'MusicFormatBadges'},
  './MusicSectionControls':{MusicSectionControls:'MusicSectionControls',musicNearEnd:()=>true},
  // Element-tree walks render every row the viewport list would mount.
  './MusicWindow':{MusicScrollView:({children}:any)=>children,MusicWindow:({items,renderItem}:any)=>items.map(renderItem)},
 });
 return screens;
}

test('invalid offered cursor preserves rows and refuses continuation until the actual section Refresh resets it',async()=>{
 let fail=true;const songs=Array.from({length:4},(_,i)=>song(`song-${i}`,i+1,180,`Track ${i}`));
 const f=await controller(albumEntry,{songs,pageSize:2,maximumLimit:2,browseError:start=>start&&fail?Object.assign(new Error('rejected'),{code:'invalid_cursor'}):undefined});
 await f.value.load();const offered=f.value.getSnapshot().songPage;await f.value.nextSongs(offered);
 const invalid=f.value.getSnapshot().songPage;assert.equal(invalid.phase,'refresh-required');assert.equal(invalid.items,offered.items);
 fail=false;await f.value.retrySongs(invalid);await f.value.nextSongs(invalid);assert.equal(f.browseBodies.length,2,'no rejected-cursor retry or implicit page one');
 const surface=await phoneSurface(f.value,albumEntry);const controls=surface.render().find(n=>n.type==='MusicSectionControls');assert(controls);
 controls.props.refresh();await f.value.load();assert.deepEqual(f.browseBodies.map(b=>b.cursor??null),[null,'2',null]);
 await f.value.nextSongs(f.value.getSnapshot().songPage);assert.deepEqual(f.browseBodies.map(b=>b.cursor??null),[null,'2',null,'2']);f.value.dispose();
});

function renderedMusicNodes(tree:any):any[]{
 const nodes:any[]=[];function walk(node:any){if(!node)return;if(Array.isArray(node)){node.forEach(walk);return;}if(typeof node!=='object')return;if(typeof node.type==='function'){walk(node.type(node.props));return;}nodes.push(node);walk(node.props?.children);}walk(tree);return nodes;
}

test('the retained real album TrackRow cannot borrow a More action after that section requires a revision refresh',async()=>{
 const songs=Array.from({length:4},(_,i)=>song(`song-${i}`,i+1,180,`Track ${i}`));
 const f=await controller(albumEntry,{songs,pageSize:2,maximumLimit:2,revision:start=>start?'r2':'r1'});await f.value.load();
 let opens=0,current:()=>boolean=()=>false;const borrowed={subscribe:()=>()=>{},isOpen:()=>false,open:async(_entry:any,proof:()=>boolean)=>{opens++;current=proof;}};
 const {createNativeEntryActions}=await screenModule(new URL('../src-next/screens/destinations/entry-actions.ts',import.meta.url),{
  react:{default:{}},'../native-screen-services':{nativeScreenServices:()=>({reportError:()=>{throw Error('unexpected error');}})},
  '../read/reading-books-open':readingBooksOpen,
  '../watch':{nativeWatchEntryMenu:()=>borrowed},'./lifetime':{nativeDestinationLifetime:()=>({current:()=>true})},
 });
 const actions=createNativeEntryActions({navigation:{getSnapshot:()=>({revision:1}),subscribe:()=>()=>{}},subscribeAuthority:()=>()=>{}},{current:()=>true,contains:f.value.contains,subscribe:f.value.subscribe});
 const screens=await tvMusicScreens();const nodes=renderedMusicNodes(screens.TVAlbumScreen({controller:f.value,entry:albumEntry,runtime:{text:{},ambient:()=>undefined,artwork:()=>undefined,reportError:()=>{}},actions,openEntry:()=>{}}));
 const row=nodes.find(n=>n.type==='TrackRow');assert(row);row.props.onMore();assert.equal(opens,1);assert.equal(current(),true);
 const first=f.value.getSnapshot().songs[0];await f.value.nextSongs(f.value.getSnapshot().songPage);
 assert.equal(f.value.getSnapshot().songs[0],first,'retain displayed row and native host');assert.equal(f.value.contains(first),false);assert.equal(current(),false);
 row.props.onMore();assert.equal(opens,1,'original More cannot adopt stale album section membership');actions.dispose();f.value.dispose();
});

test('controller and actual album/artist header omit lower-bound totals and retain exact offered counts',async()=>{
 const screens=await tvMusicScreens();
 for(const entry of [albumEntry,{...artistEntry,albumCount:7}])for(const lowerBound of [true,false]){
  const f=await controller(entry,{songs:[song('song-1',1,180,'Track')],total:10,pageSize:1,maximumLimit:1,lowerBound});await f.value.load();
  assert.equal(f.value.getSnapshot().songTotal,lowerBound?undefined:10);
  const Screen=entry.kind==='album'?screens.TVAlbumScreen:screens.TVArtistScreen;
  const nodes=renderedMusicNodes(Screen({controller:f.value,entry,runtime:{text:{},ambient:()=>undefined,artwork:()=>undefined,reportError:()=>{}},actions:{menu(){},isMenuOpen:()=>false},openEntry:()=>{}}));
  const text=JSON.stringify(nodes.filter(n=>n.type==='Text').map(n=>n.props.children));
  assert.equal(text.includes(entry.kind==='album'?'title.songCount:':'music.onServer:'),!lowerBound,'header uses only an exact offered count');f.value.dispose();
 }
 const f=await controller({...albumEntry,count:37},{songs:[song('song-1',1,180,'Track')],total:10,lowerBound:true});await f.value.load();assert.equal(f.value.getSnapshot().songTotal,37,'exact entity count remains usable');f.value.dispose();
});

test('controller focus and blur callbacks from a retired song section cannot pin or unpin its successor using the same immutable rows',async()=>{
 const songs=Array.from({length:5},(_,i)=>Object.freeze(song(`song-${i}`,i+1,180,`Track ${i}`)));
 const f=await controller(albumEntry,{songs,pageSize:1,maximumLimit:1,reuseRows:true});await f.value.load();const old=f.value.getSnapshot().songPage,first=old.items[0];
 const oldFocus=()=>f.value.focusSong(first,old),oldBlur=()=>f.value.blurSong(first,old);
 await f.value.load();await f.value.nextSongs(f.value.getSnapshot().songPage);await f.value.nextSongs(f.value.getSnapshot().songPage);
 oldFocus();await f.value.nextSongs(f.value.getSnapshot().songPage);assert.equal(f.value.getSnapshot().songPage.offset,1);
 await f.value.previousSongs(f.value.getSnapshot().songPage);const original=f.value.getSnapshot().songPage;
 f.value.focusSong(first,original);oldBlur();const count=f.browseBodies.length;await f.value.nextSongs(original);assert.equal(f.browseBodies.length,count);
 f.value.blurSong(first,original);await f.value.nextSongs(original);assert.equal(f.browseBodies.length,count+1);f.value.dispose();
});


test('the exact album entity count remains in the actual header throughout next, retry and previous loading and settlement',async()=>{
 const screens=await tvMusicScreens();
 for(const lowerBound of [true,false]){
  let hold=false,fail=true,entered:()=>void=()=>{},release:()=>void=()=>{};
  const entry={...albumEntry,count:37},songs=Array.from({length:10},(_,i)=>song(`song-${i}`,i+1,180,`Track ${i}`));
  const f=await controller(entry,{songs,pageSize:1,maximumLimit:1,lowerBound,
   waitPage:()=>hold?new Promise<void>(r=>{release=r;entered();}):undefined,
   browseError:start=>start===1&&fail?new TypeError('offline'):undefined});await f.value.load();
  const observed:(number|undefined)[]=[];const off=f.value.subscribe(()=>observed.push(f.value.getSnapshot().songTotal));
  const assertHeader=()=>{
   assert.equal(f.value.getSnapshot().songTotal,37);
   const nodes=renderedMusicNodes(screens.TVAlbumScreen({controller:f.value,entry,runtime:{text:{},ambient:()=>undefined,artwork:()=>undefined,reportError:()=>{}},actions:{menu(){},isMenuOpen:()=>false},openEntry:()=>{}}));
   assert(nodes.filter(n=>n.type==='Text').flatMap(n=>n.props.children).flat(Infinity).filter(value=>typeof value==='string').join(' ').includes('title.songCount:{"count":37}'));
  };
  const exercise=async(command:()=>Promise<void>,settled='ready')=>{
   hold=true;const started=new Promise<void>(r=>{entered=r;});const task=command();await started;
   assert.equal(f.value.getSnapshot().songPage.phase,'loading');assertHeader();release();await task;hold=false;
   assert.equal(f.value.getSnapshot().songPage.phase,settled);assertHeader();
  };
  await exercise(()=>f.value.nextSongs(f.value.getSnapshot().songPage),'error');fail=false;
  await exercise(()=>f.value.retrySongs(f.value.getSnapshot().songPage));
  await exercise(()=>f.value.nextSongs(f.value.getSnapshot().songPage));
  await exercise(()=>f.value.nextSongs(f.value.getSnapshot().songPage));
  assert.equal(f.value.getSnapshot().songPage.canPrevious,true);
  await exercise(()=>f.value.previousSongs(f.value.getSnapshot().songPage));
  assert(observed.length>=10);assert(observed.every(count=>count===37));off();f.value.dispose();
 }
});
