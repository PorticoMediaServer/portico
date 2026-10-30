import test from 'node:test';
import assert from 'node:assert/strict';
import {pdfSequential} from '../src/pdf/sequential.ts';
const signal=()=>new AbortController().signal;
function fixture(){const bytes=new Uint8Array(200000);bytes.set(new TextEncoder().encode('%PDF-1.7\n'));return bytes;}
test('sequential PDF reads one real from-start stream with bounded chunks, no range adapter',async()=>{
  const bytes=fixture();let offset=0,opened=0,cancelled=false;const source={id:'from-start',revision:'v1',size:bytes.length};
  const channel=await pdfSequential(source,async()=>{opened++;return new ReadableStream({pull(controller){if(offset===bytes.length){controller.close();return;}const end=Math.min(offset+32000,bytes.length);controller.enqueue(bytes.slice(offset,end));offset=end;},cancel(){cancelled=true;}});},signal());
  const received:Uint8Array[]=[channel.initial];let done=false;
  await channel.start({chunk(bytes){received.push(bytes);},done(){done=true;},error(error){throw error;}});
  assert.equal(opened,1);assert.equal(done,true);assert.ok(received.every(b=>b.length<=65536));assert.deepEqual(Buffer.concat(received),Buffer.from(bytes));assert.equal(typeof (source as {read?:unknown}).read,'undefined');assert.equal(cancelled,false);
});
test('sequential disposal cancels a pending read and publishes no later chunks',async()=>{
  let reads=0,cancelled=false,published=0;const abort=new AbortController(),bytes=fixture();
  const channel=await pdfSequential({id:'stream',revision:'v1',size:bytes.length},async()=>new ReadableStream({pull(c){if(reads++===0)c.enqueue(bytes.slice(0,1024));},cancel(){cancelled=true;}}),abort.signal);
  const pumping=channel.start({chunk(){published++;},done(){assert.fail('cancelled stream must not complete');},error(){assert.fail('caller abort must not become a source error');}});abort.abort();await pumping;assert.equal(cancelled,true);assert.equal(published,0);
});
test('sequential EOF length mismatch, revision change and oversize output fail closed',async()=>{
  for(const kind of ['truncated','changed','oversize']){
    const bytes=fixture(),source={id:'stream',revision:'v1',size:bytes.length};let at=0,error:unknown;
    const channel=await pdfSequential(source,async()=>new ReadableStream({pull(c){if(!at++){c.enqueue(bytes.slice(0,1024));return;}if(kind==='changed')source.revision='v2';if(kind==='oversize')c.enqueue(new Uint8Array(16*1024*1024+1));else c.close();}},{highWaterMark:0}),signal());
    await channel.start({chunk(){},done(){assert.fail('invalid stream must not complete');},error(e){error=e;}});assert.equal((error as {code:string}).code,kind==='oversize'?'pdf_limit':'pdf_invalid');
  }
});
test('byte streams use bounded BYOB buffers; abort during open cancels the returned stream',async()=>{
  const bytes=fixture();let at=0,maxBuffer=0,done=false;
  const channel=await pdfSequential({id:'byte-stream',revision:'v1',size:bytes.length},async()=>new ReadableStream<Uint8Array>({type:'bytes',pull(c){
    const request=c.byobRequest!;maxBuffer=Math.max(maxBuffer,request.view!.byteLength);
    const view=request.view!,count=Math.min(view.byteLength,bytes.length-at);new Uint8Array(view.buffer,view.byteOffset,count).set(bytes.subarray(at,at+count));at+=count;
    if(count)request.respond(count);else{c.close();request.respond(0);}
  }}),signal());
  await channel.start({chunk(bytes){assert.ok(bytes.length<=65536);},done(){done=true;},error(error){throw error;}});
  assert.equal(done,true);assert.equal(at,bytes.length);assert.equal(maxBuffer,65536);
  const abort=new AbortController();let cancelled=false;
  await assert.rejects(pdfSequential({id:'late-stream',revision:'v1',size:bytes.length},async()=>{abort.abort();return new ReadableStream({cancel(){cancelled=true;}});},abort.signal),{name:'AbortError'});
  assert.equal(cancelled,true);
});

for(const change of ['before-start','during-buffer'] as const)test(`sequential buffered bytes stop at a source revision change ${change}`,async()=>{
  const bytes=fixture(),source={id:'buffered-stream',revision:'v1',size:bytes.length};
  let emitted=0,cancelled=0,error:unknown,sent=false;
  const channel=await pdfSequential(source,async()=>new ReadableStream({
    pull(controller){if(sent){controller.close();return;}sent=true;controller.enqueue(bytes);},
    cancel(){cancelled++;}
  },{highWaterMark:0}),signal());
  if(change==='before-start')source.revision='v2';
  await channel.start({chunk(){emitted++;if(change==='during-buffer')source.revision='v2';},done(){assert.fail('retired source must not complete');},error(value){error=value;}});
  assert.equal(emitted,change==='before-start'?0:1);
  assert.equal((error as {code:string}).code,'pdf_invalid');
  assert.equal(cancelled,1);await channel.dispose();assert.equal(cancelled,1);
});
