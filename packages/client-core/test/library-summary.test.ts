import test from 'node:test';
import assert from 'node:assert/strict';
import {parseLibrarySummary,readLibrarySummaries} from '../src/library-summary.ts';
import {browseServerFilesystem, cleanServerPath, typedServerPath} from '../src/library-management.ts';
const summary={counts:[{kind:'movie',count:24,atLeast:false}],coverUrls:['/art/a'],latestArtwork:'/art/wash',firstScan:false,empty:false,emptyKnown:true};
test('library summaries are one scoped read and retain exact/lower-bound counts',async()=>{
 let calls=0;const api={async request<T>(path:string){calls++;assert.equal(path,'/v1/libraries');return {items:[{id:'library',name:'Films',kind:'movie',summary}]} as T;}};
 const rows=await readLibrarySummaries(api);assert.equal(calls,1);assert.equal(rows[0].summary.counts[0].count,24);assert.ok(Object.isFrozen(rows[0].summary.counts));assert.equal(parseLibrarySummary({...summary,counts:[{kind:'episode',count:20000,atLeast:true}]}).counts[0].atLeast,true);
 for(const bad of [{...summary,coverUrls:['https://provider.example/image']},{...summary,counts:[{kind:'movie',count:-1,atLeast:false}]},{...summary,counts:[{kind:'movie',count:1,atLeast:false},{kind:'movie',count:2,atLeast:false}]}])assert.throws(()=>parseLibrarySummary(bad));
});
test('the folder explorer asks for hidden and writable folders only when wanted, and shows which are a library\'s or cannot be one',async()=>{
 const paths:string[]=[];
 const api={async request<T>(path:string){paths.push(path);return {path:'/media',parent:'/',folders:[{name:'Films',path:'/media/Films',library:'Films'},{name:'portico',path:'/media/portico',cannotBeChosen:'This folder holds Portico\'s own data.'},{name:'Music',path:'/media/Music'}],nextCursor:'200'} as T;}};
 const page=await browseServerFilesystem(api,{path:'/media'});
 await browseServerFilesystem(api,{path:'/media',showHidden:true,checkWritable:true});
 assert.equal(paths[0],'/v1/admin/filesystem?path=%2Fmedia');
 assert.match(paths[1],/hidden=true/);assert.match(paths[1],/writable=true/);
 assert.deepEqual(page.entries.map(entry=>[entry.name,entry.library,entry.cannotBeChosen]),[['Films','Films',undefined],['portico',undefined,'This folder holds Portico\'s own data.'],['Music',undefined,undefined]]);
 assert.equal(page.nextCursor,'200');assert.equal(page.parent,'/');
});
test('a folder path loses its trailing slash, so /srv/media/ is the folder /srv/media, and only a typed one loses its padding',()=>{
 for(const [path,clean] of [['/srv/media/','/srv/media'],['/srv/media//','/srv/media'],['/','/'],['C:\\Media\\','C:\\Media'],['C:\\','C:\\'],['/srv/Films /','/srv/Films ']])assert.equal(cleanServerPath(path),clean);
 assert.equal(cleanServerPath(typedServerPath(' /srv/media// ')),'/srv/media');
});
