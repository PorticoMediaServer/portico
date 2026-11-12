import test from 'node:test';
import assert from 'node:assert/strict';
import {readListeningSimilar,type LibraryContentApi} from '../src/library-content.ts';
const scope={serverId:'server',authority:'local' as const,accountId:'account',profileId:'profile'};
const album=(id:string)=>({id,kind:'album',title:id,libraryId:'music'});
test('listening related uses the rebuilt read, preserves server order and stops at the requested limit',async()=>{
 const calls:string[]=[];
 const api={request:async(path:string)=>{calls.push(path);return path.endsWith('/related')?
  {groups:[{relation:'guest_artist',items:[album('other')]},{relation:'more_like_this',items:[album('seed'),album('b'),album('a')]}]}:album('seed');}} as unknown as LibraryContentApi;
 const page=await readListeningSimilar(api,scope,'seed',{by:'sound',limit:1});
 assert.deepEqual(calls,['/v1/items/seed','/v1/items/seed/related']);
 assert.deepEqual(page.entries.map(value=>value.id),['b']);assert.equal(page.libraryId,'music');
 assert.equal(page.bySound,false);assert.equal(page.nextCursor,undefined);
});
test('a valid empty related answer is empty; a failed read is not hidden as empty',async()=>{
 const api={request:async(path:string)=>path.endsWith('/related')?{groups:[]}:album('seed')} as unknown as LibraryContentApi;
 assert.deepEqual((await readListeningSimilar(api,scope,'seed')).entries,[]);
 const denied=Object.assign(new Error('denied'),{status:403});
 await assert.rejects(readListeningSimilar({request:async()=>{throw denied;}} as unknown as LibraryContentApi,scope,'seed'),error=>error===denied);
});
