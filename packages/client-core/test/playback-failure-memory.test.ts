import test from 'node:test';
import {shouldRetireAccountSession} from '../src/profile-management.ts';
import assert from 'node:assert/strict';
import {tmdbAbsoluteOrder} from '../src/screen-metadata.ts';
import {parsePlaybackFailureMemory,readPlaybackFailureMemory,clearPlaybackFailureMemory} from '../src/playback-failure-memory.ts';

test('anime order offsets stay explicit and bounded',()=>{
 assert.equal(tmdbAbsoluteOrder('official',-12),'tmdb-absolute:official:-12');
 assert.equal(tmdbAbsoluteOrder('tmdb-group:dvd',5),'tmdb-absolute:tmdb-group:dvd:5');
 assert.equal(tmdbAbsoluteOrder('official',0),'official');
 assert.throws(()=>tmdbAbsoluteOrder('official',1000001));
 assert.throws(()=>tmdbAbsoluteOrder('https://outside',1));
});
test('remembered playback problems read the rebuilt server, keep useful rows and forget one',async()=>{
 const row={id:'rf_1',deviceName:'Living room',fileName:'Film.mkv',player:'native',route:'converted',code:'decode_error',detail:'decoder stopped',failedAt:'2026-11-12T10:00:00Z',expiresAt:'2026-11-19T10:00:00Z'};
 const parsed=parsePlaybackFailureMemory({items:[row,{...row,id:'../escape'}]});
 assert.equal(parsed.items.length,1);
 assert.deepEqual([parsed.items[0].sourceId,parsed.items[0].device,parsed.items[0].engine,parsed.items[0].reportedAtMs],['rf_1','Living room','native',Date.parse(row.failedAt)],'each failure names the device it failed on (07-playback rule 75)');
 const calls:string[]=[];
 const api={request:async<T>(path:string,method?:string):Promise<T>=>{calls.push(`${method} ${path}`);return {items:[row]} as T;}};
 assert.equal((await readPlaybackFailureMemory(api)).items[0].route,'converted');
 await clearPlaybackFailureMemory(api,'rf_1');
 await assert.rejects(()=>clearPlaybackFailureMemory(api,'../file'));
 assert.deepEqual(calls,['GET /v1/admin/playback/failures','DELETE /v1/admin/playback/failures/rf_1']);
});

test('account cleanup cannot sign out the accepted viewer family',()=>{
 const account={accessToken:'account',sessionFamilyId:'family'};
 assert.equal(shouldRetireAccountSession(account,account),false);
 assert.equal(shouldRetireAccountSession(account,{accessToken:'renewed',sessionFamilyId:'family'}),false);
 assert.equal(shouldRetireAccountSession(undefined,account),false);
 assert.equal(shouldRetireAccountSession(account,{accessToken:'viewer',sessionFamilyId:'viewer-family'}),true);
});
