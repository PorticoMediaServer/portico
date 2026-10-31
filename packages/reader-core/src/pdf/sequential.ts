import {throwSignalAborted as throwIfAborted} from '../abort.ts';
import {releaseQuietly} from '@portico/client-core';
import type {ReaderSource} from '../ports.ts';
import {abortScope,abortReason} from '../abort.ts';

export type PdfSequentialOpen=(signal:AbortSignal)=>Promise<ReadableStream<Uint8Array>>;
/** One genuine from-start stream. Keeps only a header and one bounded chunk;
 * never implements ReadAt, seeks, reopens, creates a file, or caches a body. */
export async function pdfSequential(source:ReaderSource,open:PdfSequentialOpen,signal:AbortSignal){
  throwIfAborted(signal);const id=source.id,revision=source.revision,size=source.size;
  const fail=(code:string)=>Object.assign(new Error(code),{code});
  if(!id||!revision||!Number.isSafeInteger(size)||size<8||size>512*1024*1024)throw fail('pdf_limit');
  const lifetime=new AbortController(),scope=abortScope([signal,lifetime.signal]),combined=scope.signal;
  let stream:ReadableStream<Uint8Array>;
  try{stream=await open(combined);if(combined.aborted){await stream.cancel(abortReason(combined)).catch(()=>{});throwIfAborted(combined);}}
  catch(error){releaseQuietly('pdf sequential scope dispose',()=>scope.dispose());throw error;}
  // Fetch byte streams support BYOB: control each application allocation even
  // when the browser coalesces network packets while the PDF worker starts.
  let byob:ReadableStreamBYOBReader|undefined,plain:ReadableStreamDefaultReader<Uint8Array>|undefined;
  try{try{byob=stream.getReader({mode:'byob'});}catch{plain=stream.getReader();}}catch(error){releaseQuietly('pdf sequential scope dispose',()=>scope.dispose());throw error;}
  const reader=byob??plain!;let closed=false,started=false,total=0,remainder:Uint8Array|undefined;
  const unchanged=()=>{if(source.id!==id||source.revision!==revision||source.size!==size)throw fail('pdf_invalid');};
  const close=async(reason?:unknown)=>{if(closed)return;closed=true;combined.removeEventListener('abort',abort);lifetime.abort(reason);scope.dispose();try{await reader.cancel(reason);}catch{}finally{reader.releaseLock();}};
  const abort=()=>{void close(abortReason(combined));};combined.addEventListener('abort',abort,{once:true});
  async function read():Promise<Uint8Array|undefined>{
    throwIfAborted(combined);unchanged();const next=byob?await byob.read(new Uint8Array(65536)):await plain!.read();throwIfAborted(combined);unchanged();
    if(next.done){if(total!==size)throw fail('pdf_invalid');return;}
    // Custom non-byte streams cannot offer BYOB; retain at most one capped
    // source chunk, then deliver64KiB copies to PDF.js. No whole-body concat.
    if(!(next.value instanceof Uint8Array)||!next.value.length||next.value.length>16*1024*1024)throw fail('pdf_limit');
    total+=next.value.length;if(total>size)throw fail('pdf_invalid');return next.value;
  }
  try{
    const header=new Uint8Array(Math.min(1024,size));let at=0;
    while(at<header.length){const bytes=await read();if(!bytes)throw fail('pdf_invalid');const n=Math.min(bytes.length,header.length-at);header.set(bytes.subarray(0,n),at);at+=n;if(n<bytes.length)remainder=bytes.subarray(n);}
    if(!new TextDecoder('latin1').decode(header).includes('%PDF-'))throw fail('pdf_invalid');
    return Object.freeze({initial:header,
      async start(sink:{chunk(bytes:Uint8Array):void;done():void;error(error:unknown):void}){
        if(closed||started)throw fail('pdf_invalid');started=true;
        try{
          if(remainder){for(let at=0;at<remainder.length;at+=65536){throwIfAborted(combined);unchanged();sink.chunk(new Uint8Array(remainder.subarray(at,at+65536)));}remainder=undefined;}
          for(;;){const bytes=await read();if(!bytes)break;for(let at=0;at<bytes.length;at+=65536){throwIfAborted(combined);unchanged();sink.chunk(new Uint8Array(bytes.subarray(at,at+65536)));}}
          sink.done();await close();
        }catch(error){const expectedClose=closed;await close(error);if(!expectedClose&&!signal.aborted)sink.error(error);}
      },dispose:close
    });
  }catch(error){await close(error);throw error;}
}
