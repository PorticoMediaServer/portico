import test from 'node:test';
import assert from 'node:assert/strict';
import {refreshAllMetadata} from '../src/server-admin/metadata-refresh.ts';

test('Refresh all lists every owner-library page before queuing one refresh job per library',async()=>{
 const calls:{path:string;method?:string;body?:unknown}[]=[];
 const api={request:async<T,>(path:string,method?:string,body?:unknown)=>{calls.push({path,method,body});return (method==='GET'?path.includes('cursor=next')?{items:[{id:'movies'},{id:'music'}]}:{items:[{id:'movies'}],nextCursor:'next'}:{id:'job',kind:'refresh_library',state:'queued'}) as T;}};
 await refreshAllMetadata(api);
 assert.deepEqual(calls,[
  {path:'/v1/admin/libraries',method:'GET',body:undefined},
  {path:'/v1/admin/libraries?cursor=next',method:'GET',body:undefined},
  {path:'/v1/jobs',method:'POST',body:{kind:'refresh_library',target:{library:'movies'}}},
  {path:'/v1/jobs',method:'POST',body:{kind:'refresh_library',target:{library:'music'}}},
 ]);
});
test('an unreadable or repeated library page starts no refresh work',async()=>{
 for(const answer of [{items:[{id:''}]},{items:[],nextCursor:'loop'}]){
  const methods:string[]=[];
  const api={request:async<T,>(_path:string,method='GET')=>{methods.push(method);return answer as T;}};
  await assert.rejects(refreshAllMetadata(api));assert.ok(methods.every(method=>method==='GET'));
 }
});
test('a failed job stops the batch and is reported instead of claiming all libraries started',async()=>{
 const submitted:string[]=[],failure={status:503};
 const api={request:async<T,>(_path:string,method?:string,body?:any)=>{if(method==='GET')return {items:[{id:'one'},{id:'two'},{id:'three'}]} as T;submitted.push(body.target.library);if(submitted.length===2)throw failure;return {} as T;}};
 await assert.rejects(refreshAllMetadata(api),error=>error===failure);assert.deepEqual(submitted,['one','two']);
});
test('retiring the view after a request stops subsequent library jobs',async()=>{
 const abort=new AbortController(),submitted:string[]=[];
 const api={request:async<T,>(_path:string,method?:string,body?:any)=>{if(method==='GET')return {items:[{id:'one'},{id:'two'}]} as T;submitted.push(body.target.library);abort.abort();return {} as T;}};
 await assert.rejects(refreshAllMetadata(api,abort.signal),{name:'AbortError'});assert.deepEqual(submitted,['one']);
});
