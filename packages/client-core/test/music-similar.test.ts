import test from 'node:test';
import assert from 'node:assert/strict';
import {readListeningSimilar,type LibraryContentApi} from '../src/library-content.ts';

const scope={serverId:'server',authority:'local' as const,accountId:'account',profileId:'profile',controllerId:'controller',controllerEpoch:'epoch',commandLaneId:'lane'};
const song={id:'song',kind:'song',title:'Actual song',navigation:{view:'item',entityId:'song'}};
const payload=(bySound:unknown=true,entries:unknown[]=[song])=>({scope:{serverId:'server',authority:'local',accountId:'account',profileId:'profile'},data:{itemId:'seed',libraryId:'music',revision:{catalog:1,viewer:2},bySound,entries}});

test('sound similarity preserves the actual server basis and genuine song projection',async()=>{
 let path='';const api={request:async(value:string)=>{path=value;return payload();}} as unknown as LibraryContentApi;
 const result=await readListeningSimilar(api,scope,'seed',{by:'sound',limit:12});
 assert.equal(new URL(path,'https://server.test').searchParams.get('by'),'sound');
 assert.equal(result.bySound,true);assert.equal(result.entries[0].id,'song');assert.equal(result.entries[0].playback,undefined);
});

test('a requested sound match truthfully retains the server genre fallback',async()=>{
 const result=await readListeningSimilar({request:async()=>payload(false)} as unknown as LibraryContentApi,scope,'seed',{by:'sound'});
 assert.equal(result.bySound,false);
});

test('sound basis is never guessed from request intent or malformed/missing facts',async()=>{
 for(const basis of [undefined,null,'true',1]){const malformed=payload();malformed.data.bySound=basis;await assert.rejects(readListeningSimilar({request:async()=>malformed} as unknown as LibraryContentApi,scope,'seed',{by:'sound'}));}
 await assert.rejects(readListeningSimilar({request:async()=>payload(true,[{...song,kind:'book'}])} as unknown as LibraryContentApi,scope,'seed',{by:'sound'}));
 await assert.rejects(readListeningSimilar({request:async()=>payload()} as unknown as LibraryContentApi,scope,'seed',{by:'genre' as 'sound'}));
});

test('similarity keeps the current viewer boundary',async()=>{
 const data=payload();data.scope.profileId='other';
 await assert.rejects(readListeningSimilar({request:async()=>data} as unknown as LibraryContentApi,scope,'seed',{by:'sound'}));
});
