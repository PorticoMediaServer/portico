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
test('a kind the library holds none of leaves its line, and a library with nothing keeps its zeros',()=>{
 const anime=parseLibrarySummary({...summary,counts:[{kind:'movie',count:0,atLeast:false},{kind:'show',count:8,atLeast:false},{kind:'episode',count:32,atLeast:false}]});
 assert.deepEqual(anime.counts.map(row=>row.kind),['show','episode']);assert.ok(Object.isFrozen(anime.counts));
 const empty=parseLibrarySummary({...summary,counts:[{kind:'movie',count:0,atLeast:false}],empty:true});
 assert.deepEqual(empty.counts,[{kind:'movie',count:0,atLeast:false}]);
});
test('a library list without summaries, as the rebuilt server sends it, still lists every library with nothing known about it',async()=>{
 const api={async request<T>(){return {items:[{id:'lib_a',name:'Films',kind:'movies'},{id:'lib_b',name:'Music',kind:'music',summary:{...summary,coverUrls:['https://provider.example/image']}},{id:'lib_c',name:'Shows',kind:'shows',summary}]} as T;}};
 const rows=await readLibrarySummaries(api);
 assert.deepEqual(rows.map(row=>row.name),['Films','Music','Shows']);
 for(const row of rows.slice(0,2))assert.deepEqual(row.summary,{counts:[],coverUrls:[],latestArtwork:'',firstScan:false,empty:false,emptyKnown:false});
 assert.equal(rows[2].summary.counts[0].count,24);
 // The server's plural kinds read in the app's own words, so an audiobooks library draws square.
 assert.deepEqual(rows.map(row=>row.kind),['movie','music','tv']);
 // An unreadable list or library row still refuses the read.
 await assert.rejects(readLibrarySummaries({async request<T>(){return {items:[{id:'lib_a',kind:'movies'}]} as T;}}));
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

test('a library card leaves out kinds it holds none of and words audiobooks as books', async () => {
 const {visibleLibraryCounts} = await import('../src/library-summary.ts');
 // phone-app 6, Anime: "8 shows · 32 episodes", not "0 movies · 8 shows · 32 episodes".
 assert.deepEqual(visibleLibraryCounts([{kind:'movie',count:0,atLeast:false},{kind:'show',count:8,atLeast:false},{kind:'episode',count:32,atLeast:false}]).map(part=>[part.pivot,part.count]),[['shows',8],['episodes',32]]);
 assert.deepEqual(visibleLibraryCounts([{kind:'audiobook',count:8,atLeast:false}]).map(part=>part.pivot),['books']);
 // A capped count is kept even at zero; an unknown kind has no word and is left out.
 assert.equal(visibleLibraryCounts([{kind:'movie',count:0,atLeast:true},{kind:'hologram',count:4,atLeast:false}]).length,1);
});
