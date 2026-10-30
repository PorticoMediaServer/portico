import {throwSignalAborted as throwIfAborted} from '../abort.ts';
import {abortScope,abortReason} from '../abort.ts';
import type {ReaderSource} from '../ports.ts';
import type {PDFDocumentProxy,PDFPageProxy,PDFWorker} from 'pdfjs-dist/types/src/display/api.d.ts';
import {pdfRanges} from './ranges.ts';
import {pdfSequential,type PdfSequentialOpen} from './sequential.ts';

export class PdfInputError extends Error {
  readonly code:'pdf_invalid'|'pdf_limit'|'page_invalid'|'worker_required'|'closed';
  constructor(code:PdfInputError['code']){super(code);this.name='PdfInputError';this.code=code;}
}
export type PdfOptions=Readonly<{
  /** Bundle the matching pdfjs-dist worker; no CDN or PDF-provided URLs. */
  workerUrl?:string|URL;
  createWorker?:(url:string|URL)=>Worker;
  cMapUrl?:string;standardFontDataUrl?:string;wasmUrl?:string;
  maxBytes?:number;maxPages?:number;
}>;
export type PdfEngine=Readonly<{
  document:PDFDocumentProxy;
  library:typeof import('pdfjs-dist/legacy/build/pdf.mjs');
  signal:AbortSignal;
  page(index:number,signal:AbortSignal):Promise<PDFPageProxy>;
  run<T>(promise:Promise<T>,signal:AbortSignal):Promise<T>;
  dispose():Promise<void>;
}>;
const rangeBytes=64*1024;
function abortRace<T>(promise:Promise<T>,signal:AbortSignal):Promise<T>{
  throwIfAborted(signal);return new Promise((resolve,reject)=>{
    const abort=()=>reject(abortReason(signal));signal.addEventListener('abort',abort,{once:true});
    promise.then(resolve,reject).finally(()=>signal.removeEventListener('abort',abort));
  });
}
export async function openPdf(source:ReaderSource,signal:AbortSignal,options:PdfOptions={}):Promise<PdfEngine>{
  if(source.sequential){
    if(source.read||typeof source.sequential.open!=='function')throw new PdfInputError('pdf_invalid');
    return openPdfEngine(source,signal,options,s=>source.sequential!.open(s));
  }
  return openPdfEngine(source,signal,options);
}
/** Distinct progressive mode for an authorised, revision-fenced from-start200
 * stream. No range closure or whole-body data parameter is manufactured. */
export async function openPdfFromStart(source:ReaderSource,open:PdfSequentialOpen,signal:AbortSignal,options:PdfOptions={}):Promise<PdfEngine>{
  if(source.read)throw new PdfInputError('pdf_invalid');return openPdfEngine(source,signal,options,open);
}
async function openPdfEngine(source:ReaderSource,signal:AbortSignal,options:PdfOptions,sequentialOpen?:PdfSequentialOpen):Promise<PdfEngine>{
  throwIfAborted(signal);
  if(!sequentialOpen&&typeof source.read!=='function'||!source.id||!source.revision||!Number.isSafeInteger(source.size)||source.size<8||source.size>Math.min(options.maxBytes??512*1024*1024,512*1024*1024))throw new PdfInputError('pdf_limit');
  const id=source.id,revision=source.revision,size=source.size;
  const unchanged=()=>{if(source.id!==id||source.revision!==revision||source.size!==size)throw new PdfInputError('pdf_invalid');};
  const life=new AbortController(),scope=abortScope([signal,life.signal]),combined=scope.signal;
  let sequential:Awaited<ReturnType<typeof pdfSequential>>|undefined;
  try{sequential=sequentialOpen?await pdfSequential(source,sequentialOpen,combined):undefined;}catch(error){scope.dispose();throw error;}
  const firstEnd=Math.min(source.size,rangeBytes);let initial:Uint8Array;
  try{initial=sequential?.initial??await source.read!({start:0,endExclusive:firstEnd},combined);throwIfAborted(combined);unchanged();}catch(error){scope.dispose();await sequential?.dispose();throw error;}
  if(!(initial instanceof Uint8Array)||!sequential&&initial.byteLength!==firstEnd||!new TextDecoder('latin1').decode(initial.subarray(0,1024)).includes('%PDF-')){scope.dispose();await sequential?.dispose();throw new PdfInputError('pdf_invalid');}
  let library:typeof import('pdfjs-dist/legacy/build/pdf.mjs');
  try{library=await import('pdfjs-dist/legacy/build/pdf.mjs');throwIfAborted(combined);}catch(error){scope.dispose();await sequential?.dispose();throw error;}
  let nativeWorker:Worker|undefined,worker:PDFWorker|undefined;
  try{if(options.workerUrl){nativeWorker=(options.createWorker??((url)=>new Worker(url,{type:'module'})))(options.workerUrl);worker=library.PDFWorker.create({port:nativeWorker});}
    else if(typeof window!=='undefined')throw new PdfInputError('worker_required');
  }catch(error){worker?.destroy();nativeWorker?.terminate();scope.dispose();await sequential?.dispose();throw error;}
  const transport=new library.PDFDataRangeTransport(source.size,new Uint8Array(initial),!sequential);
  let resolveReady!:()=>void;const ready=new Promise<void>(resolve=>{resolveReady=resolve;}),transportReady=transport.transportReady.bind(transport);
  transport.transportReady=listener=>{transportReady(listener);resolveReady();};
  let failed:unknown,closed=false;
  let rejectFatal!:(error:unknown)=>void;const fatal=new Promise<never>((_resolve,reject)=>{rejectFatal=reject;});void fatal.catch(()=>{});
  let loading:ReturnType<typeof library.getDocument>|undefined;
  const fail=(error:unknown)=>{if(failed||closed)return;failed=error;rejectFatal(error);life.abort(error);void loading?.destroy().catch(()=>{});};
  const ranges=sequential?undefined:pdfRanges(source,{signal:combined,onRange:(start,bytes)=>transport.onDataRange(start,bytes),onError:fail});
  transport.requestDataRange=(start,end)=>{if(ranges)ranges.request(start,end);else fail(new PdfInputError('pdf_invalid'));};
  transport.abort=()=>{ranges?.dispose();void sequential?.dispose();};
  const abort=()=>{rejectFatal(abortReason(combined));void dispose().catch(()=>{});};combined.addEventListener('abort',abort,{once:true});
  let disposed:Promise<void>|undefined;
  const dispose=():Promise<void>=>{
    if(disposed)return disposed;closed=true;ranges?.dispose();combined.removeEventListener('abort',abort);life.abort();scope.dispose();
    disposed=(async()=>{try{await sequential?.dispose();await loading?.destroy();}finally{worker?.destroy();nativeWorker?.terminate();}})();return disposed;
  };
  try{
    loading=library.getDocument({range:transport,rangeChunkSize:rangeBytes,disableRange:!!sequential,disableStream:!sequential,disableAutoFetch:!sequential,worker,
      stopAtErrors:true,enableXfa:false,maxImageSize:40_000_000,canvasMaxAreaInBytes:64*1024*1024,
      useWorkerFetch:false,...options.cMapUrl?{cMapUrl:options.cMapUrl,cMapPacked:true}:{},...options.standardFontDataUrl?{standardFontDataUrl:options.standardFontDataUrl}:{},...options.wasmUrl?{wasmUrl:options.wasmUrl}:{}});
    // Password challenges fail closed rather than leaving an unresolved loading task.
    loading.onPassword=()=>fail(new PdfInputError('pdf_invalid'));
    if(sequential)void abortRace(ready,combined).then(()=>sequential.start({chunk(bytes){transport.onDataProgressiveRead(bytes);},done(){transport.onDataProgressiveDone();},error:fail})).catch(fail);
    const document=await abortRace(Promise.race([loading.promise,fatal]),combined);
    if(!Number.isSafeInteger(document.numPages)||document.numPages<1||document.numPages>(options.maxPages??10000))throw new PdfInputError('pdf_limit');
    return Object.freeze({document,library,signal:life.signal,
      async run<T>(promise:Promise<T>,taskSignal:AbortSignal){if(closed)throw new PdfInputError('closed');unchanged();const task=abortScope([taskSignal,life.signal]);try{const result=await abortRace(Promise.race([promise,fatal]),task.signal);unchanged();return result;}finally{task.dispose();}},
      async page(index:number,taskSignal:AbortSignal){if(closed)throw new PdfInputError('closed');unchanged();if(!Number.isInteger(index)||index<0||index>=document.numPages)throw new PdfInputError('page_invalid');const task=abortScope([taskSignal,life.signal]);try{const result=await abortRace(Promise.race([document.getPage(index+1),fatal]),task.signal);unchanged();return result;}finally{task.dispose();}},dispose});
  }catch(error){await dispose();throw error;}
}

/** Stream text with a byte budget; scans never gain scripts, links, forms or HTML. */
export async function pdfText(engine:PdfEngine,page:PDFPageProxy,signal:AbortSignal):Promise<string>{
  const scope=abortScope([engine.signal,signal]),combined=scope.signal;
  try{throwIfAborted(combined);}catch(error){scope.dispose();throw error;}
  const reader=page.streamTextContent().getReader();let text='',bytes=0;
  const abort=()=>{void reader.cancel(abortReason(combined)).catch(()=>{});};combined.addEventListener('abort',abort,{once:true});
  try{
    for(;;){const next=await engine.run(reader.read(),combined);throwIfAborted(combined);if(next.done)break;
      for(const item of next.value.items){if('str'in item){bytes+=item.str.length*2;if(bytes>2*1024*1024)throw new PdfInputError('pdf_limit');text+=item.str+(item.hasEOL?'\n':' ');}}
    }return text.trim();
  }finally{combined.removeEventListener('abort',abort);scope.dispose();await reader.cancel().catch(()=>{});reader.releaseLock();}
}

export function pdfTextStream(page:PDFPageProxy,signal:AbortSignal):ReadableStream {
  const reader=page.streamTextContent({includeMarkedContent:true}).getReader();let bytes=0,items=0,ended=false;
  const stop=async(reason?:unknown)=>{if(ended)return;ended=true;signal.removeEventListener('abort',abort);await reader.cancel(reason).catch(()=>{});reader.releaseLock();};
  const abort=()=>{void stop(abortReason(signal));};signal.addEventListener('abort',abort,{once:true});
  return new ReadableStream({
    async pull(controller){
      try{throwIfAborted(signal);const next=await reader.read();throwIfAborted(signal);if(next.done){await stop();controller.close();return;}
        items+=next.value.items.length;for(const item of next.value.items)if('str'in item)bytes+=item.str.length*2;
        if(bytes>2*1024*1024||items>50000)throw new PdfInputError('pdf_limit');controller.enqueue(next.value);
      }catch(error){await stop(error);controller.error(error);}
    },cancel(reason){return stop(reason);}
  },{highWaterMark:1});
}
