import test from 'node:test';
import assert from 'node:assert/strict';
import {parseLibrarySummary,readLibrarySummaries} from '../src/library-summary.ts';
import {browseServerFilesystem} from '../src/library-management.ts';
const summary={counts:[{kind:'movie',count:24,atLeast:false}],coverUrls:['/art/a'],latestArtwork:'/art/wash',firstScan:false,empty:false,emptyKnown:true};
test('library summaries are one scoped read and retain exact/lower-bound counts',async()=>{
 let calls=0;const api={async request<T>(path:string){calls++;assert.equal(path,'/v1/libraries');return {items:[{id:'library',name:'Films',kind:'movie',summary}]} as T;}};
 const rows=await readLibrarySummaries(api);assert.equal(calls,1);assert.equal(rows[0].summary.counts[0].count,24);assert.ok(Object.isFrozen(rows[0].summary.counts));assert.equal(parseLibrarySummary({...summary,counts:[{kind:'episode',count:20000,atLeast:true}]}).counts[0].atLeast,true);
 for(const bad of [{...summary,coverUrls:['https://provider.example/image']},{...summary,counts:[{kind:'movie',count:-1,atLeast:false}]},{...summary,counts:[{kind:'movie',count:1,atLeast:false},{kind:'movie',count:2,atLeast:false}]}])assert.throws(()=>parseLibrarySummary(bad));
});
test('folder explorer defaults to 200 folders and requests only explicit write checks',async()=>{
 const paths:string[]=[];const api={async request<T>(path:string){paths.push(path);return {result:{path:'/media',parent:'/',separator:'/',platform:'linux',writable:false,writableChecked:false,truncated:false,nextCursor:'',roots:[],entries:[],suggestions:[]}} as T;}};
 await browseServerFilesystem(api);await browseServerFilesystem(api,{path:'/media',showHidden:true,prefix:'Film',checkWritable:true});assert.match(paths[0],/limit=200/);assert.doesNotMatch(paths[0],/includeFiles|checkWritable/);assert.match(paths[1],/showHidden=1/);assert.match(paths[1],/checkWritable=1/);await assert.rejects(()=>browseServerFilesystem(api,{limit:201}));await assert.rejects(()=>browseServerFilesystem(api,{prefix:'../'}));
});
