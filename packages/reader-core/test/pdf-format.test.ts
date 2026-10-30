import test from 'node:test';
import assert from 'node:assert/strict';
import {fileURLToPath} from 'node:url';
import {openPdf,pdfText} from '../src/pdf/engine.ts';
import {createPdfAdapter} from '../src/pdf/adapter.ts';

function source(bytes:Uint8Array){return {id:'constitution',revision:'test-1',size:bytes.length,async read(range:{start:number;endExclusive:number},signal:AbortSignal){signal.throwIfAborted();return new Uint8Array(bytes.subarray(range.start,range.endExclusive));}};}
const signal=()=>new AbortController().signal;
// Deterministic, lawful PDF fixture containing public-domain US Constitution text.
// This is a real PDF parser fixture, not visual acceptance evidence.
function pdf(withContents=false){
  const content='BT /F1 18 Tf 50 700 Td (We the People of the United States) Tj ET';
  const objects=['<< /Type /Catalog /Pages 2 0 R >>','<< /Type /Pages /Kids [3 0 R 6 0 R] /Count 2 >>','<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>',`<< /Length ${content.length} >>\nstream\n${content}\nendstream`,'<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>','<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>'];
  if(withContents){objects[0]='<< /Type /Catalog /Pages 2 0 R /Outlines 7 0 R /Lang (en-US) /ViewerPreferences << /Direction /R2L >> >>';objects.push('<< /Type /Outlines /First 8 0 R /Last 8 0 R /Count 1 >>','<< /Title (We the People) /Parent 7 0 R /Dest [6 0 R /Fit] >>');}
  let text='%PDF-1.7\n',offsets=[0];for(let i=0;i<objects.length;i++){offsets.push(Buffer.byteLength(text));text+=`${i+1} 0 obj\n${objects[i]}\nendobj\n`;}
  const xref=Buffer.byteLength(text);text+=`xref\n0 ${objects.length+1}\n0000000000 65535 f \n`;for(const offset of offsets.slice(1))text+=`${String(offset).padStart(10,'0')} 00000 n \n`;text+=`trailer\n<< /Size ${objects.length+1} /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`;return new Uint8Array(Buffer.from(text));
}
test('PDF bounds, missing binary source, malformed header, short read and cancellation reject',async()=>{
  await assert.rejects(openPdf(source(new Uint8Array(4)),signal()),{code:'pdf_limit'});
  await assert.rejects(openPdf({...source(pdf()),read:undefined},signal()),{code:'pdf_limit'});
  await assert.rejects(openPdf(source(new Uint8Array(128)),signal()),{code:'pdf_invalid'});
  await assert.rejects(openPdf({...source(pdf()),read:async()=>new Uint8Array(8)},signal()),{code:'pdf_invalid'});
  const aborted=new AbortController();aborted.abort();await assert.rejects(openPdf(source(pdf()),aborted.signal),{name:'AbortError'});
  const input=source(pdf());input.read=async r=>{input.revision='changed';return pdf().subarray(r.start,r.endExclusive);};await assert.rejects(openPdf(input,signal()),{code:'pdf_invalid'});
});
test('pdf.js parses actual PDF pages and text; page bounds, revision change and disposal reject',async()=>{
  const input=source(pdf()),engine=await openPdf(input,signal(),{standardFontDataUrl:fileURLToPath(new URL('standard_fonts/',import.meta.resolve('pdfjs-dist/package.json')))});
  try{
    assert.equal(engine.document.numPages,2);const page=await engine.page(1,signal());assert.equal(page.getViewport({scale:1}).height,792);
    assert.equal(await pdfText(engine,page,signal()),'We the People of the United States');
    await assert.rejects(engine.page(-1,signal()),{code:'page_invalid'});await assert.rejects(engine.page(2,signal()),{code:'page_invalid'});
    input.revision='changed';await assert.rejects(engine.page(0,signal()),{code:'pdf_invalid'});
  }finally{await engine.dispose();}
  await assert.rejects(engine.page(0,signal()),{code:'closed'});
});
test('pdf.js parses a genuine from-start stream without a range method or reopen',async()=>{
  const bytes=pdf();let opens=0,at=0;
  const input={id:'constitution',revision:'sequential-1',size:bytes.length,sequential:{async open(signal:AbortSignal){
    opens++;signal.throwIfAborted();return new ReadableStream<Uint8Array>({pull(controller){
      signal.throwIfAborted();if(at===bytes.length){controller.close();return;}
      const end=Math.min(at+128,bytes.length);controller.enqueue(bytes.slice(at,end));at=end;
    }},{highWaterMark:0});
  }}};
  const engine=await openPdf(input,signal(),{standardFontDataUrl:fileURLToPath(new URL('standard_fonts/',import.meta.resolve('pdfjs-dist/package.json')))});
  try{assert.equal(engine.document.numPages,2);assert.equal(await pdfText(engine,await engine.page(1,signal()),signal()),'We the People of the United States');assert.equal(opens,1);assert.equal(at,bytes.length);}
  finally{await engine.dispose();}
  await assert.rejects(openPdf({...input,read:source(bytes).read},signal()),{code:'pdf_invalid'});
});
test('PDF adapter exposes only actual embedded contents, language and reading order',async()=>{
  const document=await createPdfAdapter({standardFontDataUrl:fileURLToPath(new URL('standard_fonts/',import.meta.resolve('pdfjs-dist/package.json')))}).open({source:source(pdf(true)),signal:signal()});
  try{
    assert.deepEqual(document.metadata,{language:'en-US',direction:'rtl',pageCount:2});
    assert.deepEqual(document.toc,[{id:'pdf-outline-1',label:'We the People',location:{format:'pdf',page:1,progression:1}}]);
    const matches=[];for await(const match of document.search('People',{signal:signal(),limit:1}))matches.push(match);
    assert.equal(matches.length,1);assert.equal(matches[0].location.format,'pdf');assert.match(matches[0].excerpt,/We the People/);
  }finally{await document.dispose();}
});
