import test from 'node:test';
import assert from 'node:assert/strict';
import {parsePlaybackHistory} from '../src/playback-history.ts';

const session=(id='session-1',patch:Record<string,unknown>={})=>({id,viewer:'justin',authority:'local',accountId:'account',profileId:'profile',title:'Movie',mediaKind:'movie',libraryName:'Films',itemId:'item',startedAt:'2026-01-01T00:00:00.100Z',endedAt:'2026-01-01T00:00:00.160Z',durationMs:60000,deliveryMode:'direct',deliveryStrategy:'direct-play',qualityMode:'original',state:'ended',...patch});

test('one session this app cannot read is left out and the rest of the page is kept',()=>{
 const page=parsePlaybackHistory({items:[session('one'),session('bad',{endedAt:'2026-01-01T00:00:00.050Z'}),null,session('one',{title:'Again'}),session('two',{state:'hibernating',futureField:1})],nextCursor:'next',futureField:true});
 assert.deepEqual(page.items.map(item=>[item.id,item.title,item.state]),[['one','Movie','ended'],['two','Movie','hibernating']]);
 assert.equal(page.nextCursor,'next');
});

test('the size of the page, its cursor and its outer shape still refuse the whole page',()=>{
 assert.throws(()=>parsePlaybackHistory({items:Array.from({length:201},(_,i)=>session('s'+i)),nextCursor:''}));
 assert.throws(()=>parsePlaybackHistory({items:[session()],nextCursor:7}));
 assert.throws(()=>parsePlaybackHistory({items:[session()],nextCursor:'x'.repeat(201)}));
 assert.throws(()=>parsePlaybackHistory({items:{},nextCursor:''}));
 assert.throws(()=>parsePlaybackHistory(null));
});
