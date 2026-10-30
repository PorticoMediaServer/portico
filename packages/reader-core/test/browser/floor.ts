import {createPdfAdapter,createComicAdapter,sourceFromBlob} from '@portico/reader-core';
import type {ReaderDocument,ReaderSource,ReaderView} from '@portico/reader-core';
import {parsePreferenceSnapshot,readingPreferences,type ReadingPreferences} from '@portico/client-core';
import bootstrap from '../../../../server/testdata/contracts/get-bootstrap.json';
import workerUrl from 'pdfjs-dist/legacy/build/pdf.worker.mjs?url';
import {sha256} from '@noble/hashes/sha2.js';

type Outcome=Readonly<{operation:string;status:'pass'|'fail';facts?:unknown;error?:unknown}>;
type Fixture=Readonly<{bytes:number;sha256:string;mime:string}>;
type Manifest=Readonly<{pdf:Fixture;comic:Fixture;worker:Fixture;pdfjsVersion:string}>;
const host=window as unknown as {readerFloorErrors:unknown[];readerFloorFinish(result:unknown):void;readerCleanup:unknown};
const surface=document.querySelector<HTMLElement>('#surface')!;
const preferences=readingPreferences(parsePreferenceSnapshot(bootstrap.response.preferences));
const digest=(bytes:Uint8Array)=>Array.from(sha256(bytes),b=>b.toString(16).padStart(2,'0')).join('');
function assert(value:unknown,message:string):asserts value {if(!value)throw Error(message);}
function failure(error:unknown):unknown {
  return error instanceof Error?{name:error.name,message:error.message,stack:error.stack,cause:error.cause===undefined?undefined:failure(error.cause)}:{message:String(error)};
}
async function asset(path:string,expected:Fixture,signal?:AbortSignal):Promise<Blob> {
  const response=await fetch(new URL(path,document.baseURI),{credentials:'omit',redirect:'error',signal});
  assert(response.ok,'trusted fixture asset response');
  const bytes=new Uint8Array(await response.arrayBuffer());
  assert(bytes.length===expected.bytes&&digest(bytes)===expected.sha256,'exact trusted fixture asset');
  return new Blob([bytes],{type:expected.mime});
}
async function mounted(publication:ReaderDocument<ReadingPreferences>,signal:AbortSignal) {
  const view=await publication.mount({container:surface,preferences,viewport:{width:800,height:600},signal,onLocation(){},onSelection(){}});
  const canvas=surface.querySelector<HTMLCanvasElement>('canvas');assert(canvas&&canvas.width>0&&canvas.height>0,'actual rendered canvas');
  const context=canvas.getContext('2d');assert(context,'canvas pixel read');
  const pixels=context.getImageData(0,0,canvas.width,canvas.height).data;
  let ink=false;for(let i=0;i<pixels.length;i+=4)if(pixels[i+3]&&Math.min(pixels[i],pixels[i+1],pixels[i+2])<240){ink=true;break;}
  assert(ink,'actual nonblank publication raster');return view;
}

async function run() {
  const response=await fetch(new URL('./fixtures/manifest.json',document.baseURI),{credentials:'omit',redirect:'error'});
  assert(response.ok,'trusted fixture manifest');const manifest:Manifest=await response.json();
  assert(manifest.pdfjsVersion==='6.4.299'&&manifest.worker.sha256==='bb422f60804477de4a0b09091d8347a916a387755ed1cbad24ccaec1d09c78bf','pinned same-version raw worker');
  let workers=0,terminated=0;
  const results:Outcome[]=[];
  async function operation(name:string,work:(signal:AbortSignal)=>Promise<unknown>) {
    const lifetime=new AbortController(),timer=setTimeout(()=>lifetime.abort(),20000);
    try{results.push({operation:name,status:'pass',facts:await work(lifetime.signal)});return true;}
    catch(error){results.push({operation:name,status:'fail',error:failure(error)});return false;}
    finally{clearTimeout(timer);lifetime.abort();surface.replaceChildren();}
  }
  async function pdf(url:string,signal:AbortSignal) {
    const blob=await asset('./fixtures/publication.pdf',manifest.pdf,signal),source=sourceFromBlob(blob,'lawful-gpo-pdf',manifest.pdf.sha256);
    let publication:ReaderDocument<ReadingPreferences>|undefined,view:ReaderView<ReadingPreferences>|undefined;
    try {
      publication=await createPdfAdapter<ReadingPreferences>({workerUrl:url,createWorker(path){
        const worker=new Worker(path,{type:'module'});workers++;
        worker.addEventListener('error',event=>host.readerFloorErrors.push({worker:'pdf6.4.299',message:event.message,file:event.filename,line:event.lineno}));
        const stop=worker.terminate.bind(worker);worker.terminate=()=>{terminated++;stop();};return worker;
      }}).open({source,signal});
      assert(publication.metadata.pageCount===85,'actual GPO85 parser count');
      view=await mounted(publication,signal);const location=view.getLocation();
      return {pageCount:publication.metadata.pageCount,location,canvasCount:surface.querySelectorAll('canvas').length,workerUrl:new URL(url,document.baseURI).protocol};
    }finally{await view?.dispose();await publication?.dispose();}
  }
  const direct=await operation('PDF direct bundled module worker: real GPO85 open and page raster',signal=>pdf(workerUrl,signal));
  if(!direct)await operation('PDF same-version verified Blob worker diagnostic',async signal=>{
    // Diagnostic alternative only after a measured direct-worker failure. The
    // same pinned app asset is hashed; no book URL, CDN or authority is added.
    const blob=await asset(workerUrl,manifest.worker,signal),url=URL.createObjectURL(new Blob([blob],{type:'text/javascript'}));
    try{return await pdf(url,signal);}finally{URL.revokeObjectURL(url);}
  });
  await operation('Offline DEFLATE: real Nemo CBZ member extraction and raster',async signal=>{
    const blob=await asset('./fixtures/publication.cbz',manifest.comic,signal),source:ReaderSource=sourceFromBlob(blob,'lawful-offline-nemo',manifest.comic.sha256);
    let publication:ReaderDocument<ReadingPreferences>|undefined,view:ReaderView<ReadingPreferences>|undefined;
    try {
      publication=await createComicAdapter<ReadingPreferences>({localArchive:true}).open({source,signal});
      const page=await publication.page!(0,{width:2048,signal});assert(page.bytes.length>100000&&page.width>500&&page.height>500,'actual full-page Nemo member');
      view=await mounted(publication,signal);return {pageCount:publication.metadata.pageCount,mime:page.mime,width:page.width,height:page.height,canvasCount:surface.querySelectorAll('canvas').length};
    }finally{await view?.dispose();await publication?.dispose();}
  });
  host.readerCleanup={surfaceChildren:surface.children.length,overrides:0,workers,terminated};
  return {status:results.every(result=>result.status==='pass')?'pass':'fail',runtime:{userAgent:navigator.userAgent,protocol:location.protocol,origin:location.origin,secureContext:window.isSecureContext,
    decompressionStream:typeof DecompressionStream,offscreenCanvas:typeof OffscreenCanvas,createImageBitmap:typeof createImageBitmap,abortAny:typeof AbortSignal.any,abortThrow:typeof AbortSignal.prototype.throwIfAborted},
    pdfjsVersion:manifest.pdfjsVersion,workerBytes:manifest.worker.bytes,results,workers,terminated,errors:host.readerFloorErrors,
    qualification:'Exact reader engines and lawful bundled fixture assets. Runtime is recorded; deployment target is not a supported-floor runtime proof. No auth, source/session route or full-screen verdict.'};
}
void run().then(result=>host.readerFloorFinish(result),error=>host.readerFloorFinish({status:'fail',stage:'fixture-or-entry',error:failure(error),errors:host.readerFloorErrors}));
