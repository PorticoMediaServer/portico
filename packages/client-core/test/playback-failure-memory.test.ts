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
test('owner failure memory keeps useful rows and clears one source',async()=>{
 const row={sourceId:'file',engine:'native',route:'video_conversion',code:'decode_error',detail:'decoder stopped',reportedAtMs:1,expiresAtMs:2};
 const parsed=parsePlaybackFailureMemory({items:[row,{...row,sourceId:'../escape'}],more:false});
 assert.equal(parsed.items.length,1);
 const calls:string[]=[];
 const api={request:async<T>(path:string,method?:string):Promise<T>=>{calls.push(`${method} ${path}`);return {items:[row],more:false} as T;}};
 assert.equal((await readPlaybackFailureMemory(api)).items[0].route,'video_conversion');
 await clearPlaybackFailureMemory(api,'file');
 await assert.rejects(()=>clearPlaybackFailureMemory(api,'../file'));
 assert.deepEqual(calls,['GET /v1/admin/playback/route-failures','DELETE /v1/admin/playback/route-failures/file']);
});

test('account cleanup cannot sign out the accepted viewer family',()=>{
 const account={accessToken:'account',sessionFamilyId:'family'};
 assert.equal(shouldRetireAccountSession(account,account),false);
 assert.equal(shouldRetireAccountSession(account,{accessToken:'renewed',sessionFamilyId:'family'}),false);
 assert.equal(shouldRetireAccountSession(undefined,account),false);
 assert.equal(shouldRetireAccountSession(account,{accessToken:'viewer',sessionFamilyId:'viewer-family'}),true);
});
