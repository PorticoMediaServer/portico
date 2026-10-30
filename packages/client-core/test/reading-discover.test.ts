import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi, ReadingError, type Viewer} from '../src/index.ts';
import {routePath} from '../../contracts/src/index.ts';

const viewer: Viewer = {accountId: 'account', profileId: 'profile', serverId: 'server', authority: 'local', role: 'member'};
const item = {id: 'book', libraryId: 'library', title: 'Stored title', sourceId: 'asset', format: 'pdf', inventoryAvailable: false};
const response = (value: unknown) => new Response(JSON.stringify(value), {headers: {'Content-Type': 'application/json'}});
const coded = (code: string) => (error: unknown) => error instanceof ReadingError && error.code === code;

test('Discover uses captured authority and the generated route, retaining unavailable items and exact titles', async () => {
  const calls: Array<{url: URL; headers: Headers}> = [];
  const api = new HttpLocalApi('https://server.example', 'original-token', async (input, init) => {
    calls.push({url: new URL(String(input)), headers: new Headers(init?.headers)});
    return response({rows: [{key: 'recent', items: [item]}]});
  });
  const client = api.reading({viewer, isCurrent: () => true});
  const out = await client.discover({libraryId: 'library', limit: 2});
  assert.equal(calls[0].url.pathname, routePath('get_reading_discover'));
  assert.equal(calls[0].url.search, '?libraryId=library&limit=2');
  assert.equal(calls[0].headers.get('Authorization'), 'Bearer original-token');
  assert.deepEqual(out.rows[0].items.map(row => ({...row})), [item]);
  assert.ok(Object.isFrozen(out.rows[0].items));
  api.setAccessToken('successor-token');
  await assert.rejects(client.discover(), coded('cancelled'));
  assert.equal(calls.length, 1);
  client.dispose();
});

test('Discover rejects invented rows, duplicate or out-of-scope items and oversized pages', async () => {
  for (const rows of [
    [{key: 'series', items: [item]}],
    [{key: 'recent', items: [item, item]}],
    [{key: 'recent', items: [{...item, libraryId: 'other'}]}],
    [{key: 'recent', items: [item, {...item, id: 'second'}]}],
  ]) {
    const api = new HttpLocalApi('https://server.example', 'token', async () => response({rows}));
    const client = api.reading({viewer, isCurrent: () => true});
    await assert.rejects(client.discover({libraryId: 'library', limit: 1}), coded('invalid_document'));
    client.dispose();
  }
});

test('Discover refuses invalid limits before IO and cancels an in-flight retired owner', async () => {
  let calls = 0, release!: (value: Response) => void;
  const api = new HttpLocalApi('https://server.example', 'token', async () => {
    calls++;
    return new Promise<Response>(resolve => { release = resolve; });
  });
  const client = api.reading({viewer, isCurrent: () => true});
  for (const limit of [0, 101, 1.5, NaN]) await assert.rejects(client.discover({limit}), coded('invalid_request'));
  assert.equal(calls, 0);
  const pending = client.discover();
  client.dispose();
  await assert.rejects(pending, coded('cancelled'));
  release(response({rows: [{key: 'recent', items: [item]}]}));
});

const seriesId='s'.repeat(43);
const seriesItem={...item,series:{id:seriesId,name:'Stored series',source:'owner'}};
const summary={id:seriesId,libraryId:'library',name:'Stored series',count:48,readCount:7,preview:[seriesItem]};

test('Discover opts into exact series counts with bounded real member previews',async()=>{
 let captured:URL|undefined;
 const api=new HttpLocalApi('https://server.example','token',async input=>{
  captured=new URL(String(input));
  return response({rows:[{key:'recent',items:[item]},{key:'series',items:[],series:[summary]}]});
 });
 const client=api.reading({viewer,isCurrent:()=>true});
 const out=await client.discover({libraryId:'library',seriesLimit:2});
 assert.equal(captured?.searchParams.get('seriesLimit'),'2');
 assert.equal(out.rows[1].series?.[0].count,48);
 assert.equal(out.rows[1].series?.[0].readCount,7);
 assert.equal(out.rows[1].series?.[0].preview[0].inventoryAvailable,false);
 await assert.rejects(client.discover({libraryId:'library'}),coded('invalid_document'));
 client.dispose();
});

test('Discover refuses mixed series scope, impossible counts and forged member identity',async()=>{
 for(const invalid of [
  {...summary,libraryId:'other'}, {...summary,readCount:49}, {...summary,count:0},
  {...summary,preview:[seriesItem,seriesItem]},
  {...summary,preview:[{...seriesItem,series:{...seriesItem.series,id:'x'.repeat(43)}}]},
 ]){
  const api=new HttpLocalApi('https://server.example','token',async()=>response({rows:[{key:'recent',items:[]},{key:'series',items:[],series:[invalid]}]}));
  const client=api.reading({viewer,isCurrent:()=>true});
  await assert.rejects(client.discover({libraryId:'library',seriesLimit:2}),coded('invalid_document'));
  client.dispose();
 }
 let calls=0;
 const api=new HttpLocalApi('https://server.example','token',async()=>{calls++;return response({rows:[]});});
 const client=api.reading({viewer,isCurrent:()=>true});
 for(const options of [{seriesLimit:1},{libraryId:'library',seriesLimit:0},{libraryId:'library',seriesLimit:21}])await assert.rejects(client.discover(options),coded('invalid_request'));
 assert.equal(calls,0);client.dispose();
});

// R-09 (O-159): a 1.0 client reads a "1.1" Reading page. A book of a format this client
// does not know is left out alone; the rest of the row and page still show.
test('R-09: Discover and items leave out a book of an unknown format and keep the rest', async () => {
  const future = {...item, id: 'future', format: 'djvu'};
  const api = new HttpLocalApi('https://server.example', 'token', async input =>
    new URL(String(input)).pathname === routePath('get_reading_discover')
      ? response({rows: [{key: 'recent', items: [future, item]}]})
      : response({items: [item, future], nextCursor: ''}));
  const client = api.reading({viewer, isCurrent: () => true});
  const out = await client.discover({libraryId: 'library'});
  assert.deepEqual(out.rows[0].items.map(row => row.id), ['book']);
  const page = await client.list({libraryId: 'library'});
  assert.deepEqual(page.items.map(row => row.id), ['book']);
  client.dispose();
});
