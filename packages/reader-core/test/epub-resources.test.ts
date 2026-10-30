import {test} from 'node:test';
import assert from 'node:assert/strict';
import {resourceAssets} from '../src/epub/resources.ts';
const signal=()=>new AbortController().signal;
function fixture() {
  const entry={path:'chapter.xhtml',mime:'application/xhtml+xml',size:1};let calls=0;
  const resources={entries:[entry],async read(){calls++;return new Uint8Array([1]);}};
  return {source:{id:'publication',revision:'v1',size:100,resources},entry,get calls(){return calls;}};
}
test('EPUB resource index freezes observed lengths and ignores later caller metadata mutation',async()=>{
  const f=fixture(),assets=resourceAssets(f.source);f.entry.size=2;
  assert.equal(assets.entries[0].size,1);assert.ok(Object.isFrozen(assets.entries)&&Object.isFrozen(assets.entries[0]));
  assert.deepEqual(await assets.read('chapter.xhtml',signal()),new Uint8Array([1]));
  f.source.resources.read=async()=>new Uint8Array([1,2]);await assert.rejects(assets.read('chapter.xhtml',signal()),{code:'invalid_book'});await assets.dispose();
});
test('EPUB resource reads refuse changed identity before I/O and after a pending response',async()=>{
  const f=fixture(),assets=resourceAssets(f.source);f.source.revision='v2';
  await assert.rejects(assets.read('chapter.xhtml',signal()),{code:'invalid_source'});assert.equal(f.calls,0);await assets.dispose();
  const g=fixture();let resolve!:(bytes:Uint8Array)=>void,started!:()=>void;
  const entered=new Promise<void>(done=>started=done);g.source.resources.read=async()=>{started();return new Promise<Uint8Array>(done=>resolve=done);};
  const pendingAssets=resourceAssets(g.source),reading=pendingAssets.read('chapter.xhtml',signal());await entered;g.source.revision='v2';resolve(new Uint8Array([1]));
  await assert.rejects(reading,{code:'invalid_source'});await pendingAssets.dispose();
});
test('disposing EPUB resources aborts pending provider work and cannot deliver late bytes',async()=>{
  const f=fixture();let providerSignal!:AbortSignal,resolve!:(bytes:Uint8Array)=>void;
  f.source.resources.read=async(_path?:string,active?:AbortSignal)=>{providerSignal=active!;return new Promise<Uint8Array>(done=>resolve=done);};
  const assets=resourceAssets(f.source),reading=assets.read('chapter.xhtml',signal());await assets.dispose();assert.ok(providerSignal.aborted);
  resolve(new Uint8Array([1]));await assert.rejects(reading,{code:'aborted'});await assets.dispose();
});
