import {test} from 'node:test';
import assert from 'node:assert/strict';
import {createReadingPublication,readerLocationFromReading,readingLocationFromReader} from '../src/index.ts';
import type {ReaderSessionInput} from '../src/index.ts';
import type {ReadingManifest,ReadingProgress} from '@portico/client-core';
const png=new Uint8Array(Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAAC0lEQVR42mNgAAIAAAUAAarVyFEAAAAASUVORK5CYII=','base64'));
const active=()=>new AbortController().signal;
function fixture(format:'epub'|'pdf'|'cbz'|'cbr') {
  const lifetime=new AbortController(),calls:string[]=[],revision='a'.repeat(64);
  let manifest:ReadingManifest=Object.freeze({sessionId:'session-one',itemId:'item',sourceId:'asset',revision,format,size:100000,metadata:Object.freeze({title:'Catalogue title'}),
    ...format==='epub'?{resources:Object.freeze([{path:'chapter.xhtml',mime:'application/xhtml+xml',size:1}]),spine:Object.freeze(['chapter.xhtml'])}:{},
    ...(format==='cbz'||format==='cbr')?{pageNames:Object.freeze(['page.png'])}:{}});
  const session:ReaderSessionInput={get manifest(){return manifest;},signal:lifetime.signal,async headPDF(){calls.push('head');},async readRange(range){calls.push('range');return new Uint8Array(range.endExclusive-range.start);},
    async openSequential(){calls.push('sequential');return new ReadableStream<Uint8Array>();},async readAsset(){calls.push('asset');return new Uint8Array([1]);},async page(index){calls.push('page');return {index,name:'page.png',bytes:png,mime:'image/png'};}};
  return {session,lifetime,calls,replaceManifest(change:Partial<ReadingManifest>){manifest=Object.freeze({...manifest,...change});}};
}

test('PDF publication HEADs once and exposes only captured bounded ranges with owned bytes',async()=>{
  const f=fixture('pdf'),publication=await createReadingPublication(f.session);
  assert.deepEqual(f.calls,['head']);assert.equal(publication.source.id,'asset');assert.equal(publication.source.revision,f.session.manifest.revision);
  assert.equal(publication.source.resources,undefined);assert.equal(publication.source.sequential,undefined);assert.equal(publication.comicPages,undefined);
  assert.deepEqual(publication.metadata,{title:'Catalogue title'});assert.ok(Object.isFrozen(publication.source));
  assert.equal((await publication.source.read!({start:0,endExclusive:65536},active())).length,65536);
  await assert.rejects(publication.source.read!({start:0,endExclusive:65537},active()),{code:'limit_exceeded'});assert.deepEqual(f.calls,['head','range']);
  publication.dispose();await assert.rejects(publication.source.read!({start:0,endExclusive:1},active()),{code:'aborted'});assert.equal(f.lifetime.signal.aborted,false);
});

test('EPUB publication supplies original enumerated assets only and preserves omitted author',async()=>{
  const f=fixture('epub'),publication=await createReadingPublication(f.session);
  assert.equal(f.calls.length,0);assert.equal(publication.source.read,undefined);assert.equal(publication.metadata.author,undefined);
  assert.ok(Object.isFrozen(publication.source.resources!.entries[0]));
  await assert.rejects(publication.source.resources!.read('../missing',active()),{code:'invalid_source'});assert.equal(f.calls.length,0);
  assert.deepEqual(await publication.source.resources!.read('chapter.xhtml',active()),new Uint8Array([1]));assert.deepEqual(f.calls,['asset']);publication.dispose();
});

test('contents selection captures the actual manifest index and source revision without reading bytes',async()=>{
  const f=fixture('epub');
  f.replaceManifest({contents:[{title:'Chapter',depth:0,section:'chapter.xhtml',fragment:'part-one'}]});
  const publication=await createReadingPublication(f.session);
  assert.deepEqual(publication.contentsTarget(0),{sourceId:'asset',sourceRevision:f.session.manifest.revision,index:0});
  assert.deepEqual(publication.source.contents,[{title:'Chapter',depth:0,section:'chapter.xhtml',fragment:'part-one'}]);
  assert.ok(Object.isFrozen(publication.source.contents));assert.ok(Object.isFrozen(publication.source.contents![0]));
  assert.throws(()=>publication.contentsTarget(-1),{code:'invalid_location'});
  assert.throws(()=>publication.contentsTarget(1),{code:'invalid_location'});assert.deepEqual(f.calls,[]);
  f.replaceManifest({revision:'b'.repeat(64)});
  assert.throws(()=>publication.contentsTarget(0),{code:'invalid_source'});publication.dispose();
});

test('comic publication inspects actual returned variants, never supplies an archive reader, and bounds before I/O',async()=>{
  const f=fixture('cbz'),publication=await createReadingPublication(f.session);
  assert.equal(publication.source.read,undefined);assert.equal(publication.source.resources,undefined);assert.equal(publication.metadata.pageCount,1);
  const provider=await publication.comicPages!(publication.source,active());
  await assert.rejects(provider.page(-1,{width:300,signal:active()}),{code:'invalid_location'});await assert.rejects(provider.page(0,{width:31,signal:active()}),{code:'invalid_location'});assert.equal(f.calls.length,0);
  const page=await provider.page(0,{width:300,signal:active()});assert.deepEqual(page.image,{mime:'image/png',width:1,height:1});assert.notEqual(page.bytes,png);
  await provider.dispose();await assert.rejects(provider.page(0,{width:300,signal:active()}),{code:'aborted'});
  await assert.rejects(publication.comicPages!({...publication.source},active()),{code:'invalid_source'});publication.dispose();
});

test('comic MIME drift and late responses after authority retirement cannot become engine bytes',async()=>{
  const f=fixture('cbz');f.session.page=async index=>({index,name:'page.png',bytes:png,mime:'image/jpeg'});
  const publication=await createReadingPublication(f.session),provider=await publication.comicPages!(publication.source,active());
  await assert.rejects(provider.page(0,{width:300,signal:active()}),{code:'invalid_book'});await provider.dispose();publication.dispose();
  const g=fixture('epub');let finish!:(bytes:Uint8Array)=>void;g.session.readAsset=async()=>new Promise(done=>finish=done);
  const p=await createReadingPublication(g.session),reading=p.source.resources!.read('chapter.xhtml',active());g.lifetime.abort();finish(new Uint8Array([1]));await assert.rejects(reading,{code:'aborted'});p.dispose();
});

test('wire/core locations preserve zero-based facts and omit renderer-only labels',()=>{
  for(const location of [{format:'pdf' as const,page:0,progression:0},{format:'cbz' as const,page:2,progression:0.5},
    {format:'epub' as const,cfi:'epubcfi(/6/2!/4/2)',section:'chapter.xhtml',progression:0.2}]) {
    const wire=readingLocationFromReader({...location,chapter:'Observed chapter',pageLabel:'iii',pagesLeftInSection:2});
    assert.equal('chapter'in wire,false);assert.deepEqual(readerLocationFromReading(wire),location);
  }
  assert.throws(()=>readerLocationFromReading({format:'pdf',pageIndex:0,cfi:'epubcfi(/6)',progression:0}),{code:'invalid_location'});
  assert.throws(()=>readerLocationFromReading({format:'epub',cfi:'not-a-cfi',progression:0}),{code:'invalid_location'});
});

test('resume mapping preserves null and rejects stale/wrong-source progress without creating a saved location',async()=>{
  const f=fixture('pdf'),publication=await createReadingPublication(f.session);
  const progress:ReadingProgress={progress:{sourceId:'asset',sourceRevision:f.session.manifest.revision,revision:1,observedAt:1,stale:false,location:{format:'pdf',pageIndex:0,progression:0}}};
  assert.equal(publication.resumeLocation({progress:null}),null);assert.deepEqual(publication.resumeLocation(progress),{format:'pdf',page:0,progression:0});
  assert.equal(publication.resumeLocation({progress:{...progress.progress!,sourceRevision:'b'.repeat(64),stale:true}}),null);
  assert.throws(()=>publication.resumeLocation({progress:{...progress.progress!,sourceId:'other'}}),{code:'invalid_source'});assert.deepEqual(f.calls,['head']);publication.dispose();
});

test('sequential PDF publication exposes a from-start pull stream, never a range reader or eager body',async()=>{
  const f=fixture('pdf');f.replaceManifest({sequential:true,size:6});
  let pulls=0;const bytes=new Uint8Array([1,2,3]),input=new ReadableStream<Uint8Array>({pull(c){pulls++;if(pulls<=2)c.enqueue(bytes);else c.close();}},{highWaterMark:0});
  f.session.openSequential=async()=>{f.calls.push('sequential');return input;};
  const publication=await createReadingPublication(f.session);assert.equal(publication.source.read,undefined);assert.ok(Object.isFrozen(publication.source.sequential));
  const stream=await publication.source.sequential!.open(active());assert.deepEqual(f.calls,['head','sequential']);assert.equal(pulls,0);
  const reader=stream.getReader();const first=(await reader.read()).value!;assert.deepEqual(first,bytes);assert.notEqual(first,bytes);assert.equal(pulls,1);
  first.fill(99);assert.deepEqual(bytes,new Uint8Array([1,2,3]));assert.deepEqual((await reader.read()).value,bytes);assert.equal((await reader.read()).done,true);
  reader.releaseLock();assert.equal(input.locked,false);assert.equal(f.lifetime.signal.aborted,false);publication.dispose();
});

for(const retirement of ['session','publication','operation','consumer'] as const)test(`sequential publication retains cancellation after headers: ${retirement}`,async()=>{
  const f=fixture('pdf');f.replaceManifest({sequential:true});let cancelled=0;
  const input=new ReadableStream<Uint8Array>({cancel(){cancelled++;}},{highWaterMark:0});f.session.openSequential=async()=>input;
  const publication=await createReadingPublication(f.session),operation=new AbortController(),stream=await publication.source.sequential!.open(operation.signal),reader=stream.getReader();
  const pending=reader.read();
  if(retirement==='consumer'){await reader.cancel();assert.equal((await pending).done,true);}
  else {
    const rejected=assert.rejects(pending);
    if(retirement==='session')f.lifetime.abort();else if(retirement==='operation')operation.abort();else publication.dispose();
    await rejected;
  }
  await Promise.resolve();assert.equal(cancelled,1);assert.equal(input.locked,false);
  if(retirement!=='session')assert.equal(f.lifetime.signal.aborted,false);
  reader.releaseLock();publication.dispose();
});

test('sequential publication rejects late-open bytes and identity changes before/after reads',async()=>{
  const f=fixture('pdf');f.replaceManifest({sequential:true});let deliver!:(stream:ReadableStream<Uint8Array>)=>void,cancelled=0;
  f.session.openSequential=async()=>new Promise(done=>deliver=done);
  const publication=await createReadingPublication(f.session),opening=publication.source.sequential!.open(active());
  publication.dispose();deliver(new ReadableStream({cancel(){cancelled++;}}));await assert.rejects(opening,{code:'aborted'});assert.equal(cancelled,1);
  for(const when of ['before','during'] as const){
    const g=fixture('pdf');g.replaceManifest({sequential:true});let enqueue!:(bytes:Uint8Array)=>void,cancelled=0;
    const input=new ReadableStream<Uint8Array>({start(c){enqueue=bytes=>c.enqueue(bytes);},cancel(){cancelled++;}},{highWaterMark:0});g.session.openSequential=async()=>input;
    const p=await createReadingPublication(g.session),reader=(await p.source.sequential!.open(active())).getReader();
    if(when==='before')g.replaceManifest({revision:'b'.repeat(64)});
    const pending=reader.read(),rejected=assert.rejects(pending,{code:'invalid_source'});
    if(when==='during'){await Promise.resolve();g.replaceManifest({revision:'b'.repeat(64)});enqueue(new Uint8Array([1]));}
    await rejected;assert.equal(cancelled,1);reader.releaseLock();p.dispose();
  }
});

test('sequential publication checks exact EOF and bounded byte chunks without buffering',async()=>{
  for(const kind of ['short','long','oversized'] as const){
    const f=fixture('pdf'),size=kind==='oversized'?2*1024*1024:2;f.replaceManifest({sequential:true,size});let cancelled=0;
    const input=new ReadableStream<Uint8Array>({pull(c){if(kind==='short')c.close();else c.enqueue(new Uint8Array(kind==='long'?3:1024*1024+1));},cancel(){cancelled++;}},{highWaterMark:0});f.session.openSequential=async()=>input;
    const p=await createReadingPublication(f.session),reader=(await p.source.sequential!.open(active())).getReader();
    await assert.rejects(reader.read(),{code:kind==='oversized'?'limit_exceeded':'invalid_source'});assert.equal(input.locked,false);assert.equal(cancelled,kind==='short'?0:1);reader.releaseLock();p.dispose();
  }
  const missing=fixture('pdf');missing.replaceManifest({sequential:true});delete missing.session.openSequential;await assert.rejects(createReadingPublication(missing.session),{code:'invalid_source'});assert.deepEqual(missing.calls,[]);
  for(const format of ['epub','cbz'] as const){const f=fixture(format);f.replaceManifest({sequential:true});await assert.rejects(createReadingPublication(f.session),{code:'invalid_source'});assert.deepEqual(f.calls,[]);}
});

test('a comic session without enumerated page names is refused, never read as a crash',async()=>{
  const f=fixture('cbz');
  f.replaceManifest({pageNames:undefined});
  await assert.rejects(createReadingPublication(f.session),(error:unknown)=>(error as {code?:string}).code==='invalid_source');
  assert.deepEqual(f.calls,[]);
});

// CBR is opaque archive identity plus original server-rendered page bytes, UNRUN.
test('CBR publication preserves format, bounded locations and exact raster-page ownership', async()=>{
  const f=fixture('cbr'),publication=await createReadingPublication(f.session);
  try {
    assert.equal(publication.format,'cbr');assert.equal(publication.metadata.pageCount,1);
    assert.equal(publication.source.read,undefined);assert.equal(publication.source.resources,undefined);
    const provider=await publication.comicPages!(publication.source,active());
    assert.equal((await provider.page(0,{width:320,signal:active()})).name,'page.png');
    assert.deepEqual(f.calls,['page']);
    assert.deepEqual(readerLocationFromReading({format:'cbr',pageIndex:0,progression:0}),{format:'cbr',page:0,progression:0});
    assert.deepEqual(readingLocationFromReader({format:'cbr',page:0,progression:0}),{format:'cbr',pageIndex:0,progression:0});
    await assert.rejects(publication.comicPages!({...publication.source},active()),{code:'invalid_source'});
    await assert.rejects(provider.page(1,{width:320,signal:active()}),{code:'invalid_location'});
    f.replaceManifest({revision:'b'.repeat(64)});
    await assert.rejects(provider.page(0,{width:320,signal:active()}),{code:'invalid_source'});
    assert.deepEqual(f.calls,['page']);provider.dispose();
  }finally{publication.dispose();}
});
