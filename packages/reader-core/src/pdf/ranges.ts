import {throwSignalAborted as throwIfAborted} from '../abort.ts';
import type {ReaderSource} from '../ports.ts';
import {abortScope} from '../abort.ts';

/** PDF.js requests may cover several chunks. Return one exact requested range. */
export function pdfRanges(source:ReaderSource,options:{signal:AbortSignal;onRange(start:number,bytes:Uint8Array):void;onError(error:unknown):void}){
  const id=source.id,revision=source.revision,size=source.size,life=new AbortController(),scope=abortScope([options.signal,life.signal]),signal=scope.signal;
  const queue:{start:number;end:number}[]=[];let active=0,closed=false,total=0,count=0;
  const error=(code:string)=>Object.assign(new Error(code),{code});
  const unchanged=()=>{if(source.id!==id||source.revision!==revision||source.size!==size||typeof source.read!=='function')throw error('pdf_invalid');};
  const fail=(reason:unknown)=>{if(closed||signal.aborted)return;closed=true;queue.length=0;life.abort(reason);scope.dispose();options.onError(reason);};
  const pump=()=>{
    while(!closed&&!signal.aborted&&active<2&&queue.length){
      const range=queue.shift()!;active++;
      void (async()=>{
        const result=new Uint8Array(range.end-range.start);
        for(let start=range.start;start<range.end;start+=65536){
          throwIfAborted(signal);unchanged();const end=Math.min(start+65536,range.end);
          const bytes=await source.read!({start,endExclusive:end},signal);throwIfAborted(signal);unchanged();
          if(!(bytes instanceof Uint8Array)||bytes.byteLength!==end-start)throw error('pdf_invalid');result.set(bytes,start-range.start);
        }
        throwIfAborted(signal);if(!closed)options.onRange(range.start,result);
      })().catch(fail).finally(()=>{active--;pump();});
    }
  };
  return Object.freeze({
    request(start:number,end:number){
      if(closed||signal.aborted)return;
      try{
        unchanged();if(!Number.isSafeInteger(start)||!Number.isSafeInteger(end)||start<0||end<=start||end>size)throw error('pdf_invalid');
        total+=end-start;count++;
        if(end-start>16*1024*1024||queue.length>=128||count>32768||total>Math.max(size*4,16*1024*1024))throw error('pdf_limit');
        queue.push({start,end});pump();
      }catch(reason){fail(reason);}
    },
    dispose(){if(closed)return;closed=true;queue.length=0;life.abort();scope.dispose();}
  });
}
