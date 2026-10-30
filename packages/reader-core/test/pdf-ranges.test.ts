import test from 'node:test';
import assert from 'node:assert/strict';
import {pdfRanges} from '../src/pdf/ranges.ts';
const signal=()=>new AbortController().signal;
test('PDF ranges preserve full request boundaries while reading64KiB chunks with at most2 providers',async()=>{
  const bytes=Uint8Array.from({length:800000},(_,i)=>i%251),received:{start:number;bytes:Uint8Array}[]=[],reads:{start:number;endExclusive:number}[]=[];let active=0,peak=0;
  let resolve!:()=>void,reject!:(e:unknown)=>void;const done=new Promise<void>((r,j)=>{resolve=r;reject=j;});
  const pump=pdfRanges({id:'pdf',revision:'v1',size:bytes.length,async read(range,sig){sig.throwIfAborted();active++;peak=Math.max(peak,active);reads.push(range);await new Promise(r=>setTimeout(r,1));active--;return bytes.slice(range.start,range.endExclusive);}},{signal:signal(),onRange(start,data){received.push({start,bytes:data});if(received.length===4)resolve();},onError:reject});
  try{for(const start of [0,200000,400000,600000])pump.request(start,start+200000);await done;assert.equal(peak,2);assert.ok(reads.every(r=>r.endExclusive-r.start<=65536));for(const r of received)assert.deepEqual(r.bytes,bytes.subarray(r.start,r.start+200000));}finally{pump.dispose();}
});
test('PDF provider cancellation stops active and queued ranges, with no late publication',async()=>{
  let active=0,cancelled=0,published=0,start!:()=>void;const ready=new Promise<void>(r=>{start=r;});
  const pump=pdfRanges({id:'pdf',revision:'v1',size:1000000,async read(_range,sig){active++;if(active===2)start();return new Promise<Uint8Array>((_r,reject)=>sig.addEventListener('abort',()=>{cancelled++;reject(sig.reason);},{once:true}));}},{signal:signal(),onRange(){published++;},onError(){assert.fail('disposal is not a provider error');}});
  for(const n of [0,1,2,3])pump.request(n*65536,(n+1)*65536);await ready;pump.dispose();await new Promise(r=>setTimeout(r,0));assert.equal(active,2);assert.equal(cancelled,2);assert.equal(published,0);
});
test('PDF range size, revision and exact byte-count checks fail once and discard the queue',async()=>{
  for(const mode of ['bounds','limit','revision','short']){
    let resolve!:(e:unknown)=>void;const done=new Promise<unknown>(r=>{resolve=r;});let errors=0,publications=0;
    const source={id:'pdf',revision:'v1',size:32*1024*1024,async read(range:{start:number;endExclusive:number}){if(mode==='revision')source.revision='v2';return new Uint8Array(mode==='short'?1:range.endExclusive-range.start);}};
    const pump=pdfRanges(source,{signal:signal(),onRange(){publications++;},onError(e){errors++;resolve(e);}});
    pump.request(mode==='bounds'?-1:0,mode==='limit'?17*1024*1024:65536);const error=await done;assert.equal((error as {code:string}).code,mode==='limit'?'pdf_limit':'pdf_invalid');pump.request(0,65536);pump.dispose();assert.equal(errors,1);assert.equal(publications,0);
  }
});
