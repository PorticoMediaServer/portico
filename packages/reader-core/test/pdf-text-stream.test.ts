import test from 'node:test';
import assert from 'node:assert/strict';
import {pdfTextStream} from '../src/pdf/engine.ts';
import type {PDFPageProxy} from 'pdfjs-dist/types/src/display/api.d.ts';

test('PDF text-layer input enforces text and DOM-item budgets and cancels the upstream stream',async()=>{
  for(const items of [[{str:'x'.repeat(1024*1024+1)}],Array.from({length:50001},()=>({str:''}))]){
    let cancelled=false;const source=new ReadableStream({start(c){c.enqueue({items});},cancel(){cancelled=true;}});
    const page={streamTextContent:()=>source} as unknown as PDFPageProxy;
    const reader=pdfTextStream(page,new AbortController().signal).getReader();await assert.rejects(reader.read(),{code:'pdf_limit'});assert.equal(cancelled,true);reader.releaseLock();
  }
});
test('PDF text-layer cancellation releases a pending provider read',async()=>{
  let cancelled=false;const source=new ReadableStream({cancel(){cancelled=true;}}),abort=new AbortController();
  const page={streamTextContent:()=>source} as unknown as PDFPageProxy,reader=pdfTextStream(page,abort.signal).getReader();const pending=reader.read();abort.abort();await assert.rejects(pending,{name:'AbortError'});assert.equal(cancelled,true);reader.releaseLock();
});
test('PDF text layers request and retain marked-content identities for the structure tree',async()=>{
  let options:unknown;
  const items=[{type:'beginMarkedContentProps',id:'p0R_mc0',tag:'H1'},{str:'A heading'},{type:'endMarkedContent'}];
  const page={streamTextContent:(value:unknown)=>{options=value;return new ReadableStream({start(controller){controller.enqueue({items});controller.close();}});}} as unknown as PDFPageProxy;
  const reader=pdfTextStream(page,new AbortController().signal).getReader();
  assert.deepEqual(options,{includeMarkedContent:true});
  assert.deepEqual((await reader.read()).value.items,items);
  assert.equal((await reader.read()).done,true);reader.releaseLock();
});
