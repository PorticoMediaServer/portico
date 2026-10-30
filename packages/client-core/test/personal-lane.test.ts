import test from 'node:test';
import assert from 'node:assert/strict';
import {validateHomeSeeAll} from '../src/home.ts';
import {setContainerState,undoContainerState} from '../src/container-state.ts';

test('Home destinations retain the exact library browse or list',()=>{
 assert.deepEqual(validateHomeSeeAll({type:'library',libraryId:'library',pivot:'shows',sort:[{field:'added',direction:'desc'}]}),{type:'library',libraryId:'library',pivot:'shows',sort:[{field:'added',direction:'desc'}]});
 assert.deepEqual(validateHomeSeeAll({type:'row',rowId:'continue'}),{type:'row',rowId:'continue'});
 assert.deepEqual(validateHomeSeeAll({type:'saved_view',resourceId:'view'}),{type:'saved_view',resourceId:'view'});
 assert.throws(()=>validateHomeSeeAll({type:'library',libraryId:'library',pivot:'movies',sort:[{field:'added',direction:'other'}]}));
 assert.throws(()=>validateHomeSeeAll({type:'row',rowId:''}));
});

test('Container Undo keeps the server receipt and posts its revision',async()=>{
 const calls:unknown[][]=[];
 const api={async request<T>(...args:unknown[]):Promise<T>{calls.push(args);return {revision:calls.length,watched:true,watermark:'2026-10-13T00:00:00Z',watchlisted:false,favorite:false,...(calls.length===1?{undoToken:'token',undoUntil:'2026-10-13T00:00:30Z'}:{})} as T;}};
 const state=await setContainerState(api,'show','show',{expectedRevision:0,watched:true});
 assert.equal(state.undoToken,'token');
 assert.equal(state.undoUntil,'2026-10-13T00:00:30Z');
 const restored=await undoContainerState(api,'show','show',{expectedRevision:state.revision,undoToken:state.undoToken!});
 assert.equal(restored.revision,2);
 assert.deepEqual(calls[1].slice(0,3),['/v1/containers/show/show/personal-state/undo','POST',{expectedRevision:1,undoToken:'token'}]);
});
