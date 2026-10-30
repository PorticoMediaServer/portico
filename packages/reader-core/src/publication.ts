import type {ReadingSession,ReadingLocation,ReadingProgress} from '@portico/client-core';
import type {ReaderSource,ReaderFormat,ReaderMetadata,ReaderLocation,ReaderRange,ReaderContentsTarget} from './ports.ts';
import type {ComicPageProvider} from './comic/archive.ts';
import {rasterInfo} from './comic/image.ts';
import {abortScope,abortReason} from './abort.ts';
import {ReaderError,throwIfAborted} from './errors.ts';
import {validateLocation} from './controller.ts';

/** Derived from the canonical captured facade. Native byte bridges implement
 * these same public callbacks; no HTTP client, token or URL enters an engine. */
export type ReaderSessionInput=Pick<ReadingSession,'manifest'|'signal'|'headPDF'|'readRange'|'readAsset'|'page'> &
  Partial<Pick<ReadingSession,'openSequential'|'search'>>;
export type ReadingPublication=Readonly<{
  format:ReaderFormat;source:ReaderSource;metadata:ReaderMetadata;signal:AbortSignal;
  comicPages?:(source:ReaderSource,signal:AbortSignal)=>Promise<ComicPageProvider>;
  resumeLocation(progress:ReadingProgress):ReaderLocation|null;
  contentsTarget(index:number):ReaderContentsTarget;
  /** Local retirement only. The caller still owns captured session.close(). */
  dispose():void;
}>;

export function readerLocationFromReading(location:ReadingLocation):ReaderLocation {
  let result:ReaderLocation;
  if(location.format==='epub') {
    if(typeof location.cfi!=='string'||location.pageIndex!==undefined)throw new ReaderError('invalid_location');
    result={format:'epub',cfi:location.cfi,progression:location.progression,...location.section===undefined?{}:{section:location.section}};
  } else if(location.format==='pdf'||location.format==='cbz'||location.format==='cbr') {
    if(location.pageIndex===undefined||location.cfi!==undefined||location.section!==undefined)throw new ReaderError('invalid_location');
    result={format:location.format,page:location.pageIndex,progression:location.progression};
  } else throw new ReaderError('invalid_location');
  validateLocation(result,result.format);return Object.freeze(result);
}
export function readingLocationFromReader(location:ReaderLocation):ReadingLocation {
  validateLocation(location,location.format);
  // Observed chrome labels/chapters are not wire location fields.
  return Object.freeze(location.format==='epub'?{format:'epub',cfi:location.cfi,progression:location.progression,
    ...location.section===undefined?{}:{section:location.section}}:{format:location.format,pageIndex:location.page,progression:location.progression});
}

export async function createReadingPublication(session:ReaderSessionInput,callerSignal?:AbortSignal):Promise<ReadingPublication> {
  const manifest=session.manifest,{sourceId:id,revision,size,sessionId,sequential}=manifest,life=new AbortController();
  const contents=Object.freeze((manifest.contents??[]).map(entry=>Object.freeze({...entry})));
  const scope=abortScope([session.signal,life.signal,...callerSignal?[callerSignal]:[]]),signal=scope.signal;
  const current=()=>{
    throwIfAborted(signal);
    if(session.manifest!==manifest||manifest.sourceId!==id||manifest.revision!==revision||manifest.size!==size||manifest.sessionId!==sessionId||manifest.sequential!==sequential)
      throw new ReaderError('invalid_source');
  };
  async function run<T>(taskSignal:AbortSignal,work:(signal:AbortSignal)=>Promise<T>):Promise<T> {
    current();const task=abortScope([signal,taskSignal]);
    try{throwIfAborted(task.signal);const value=await work(task.signal);throwIfAborted(task.signal);current();return value;}
    finally{task.dispose();}
  }
  // The operation scope must outlive the headers: retain it until EOF, consumer
  // cancellation or authority retirement. Pull one bounded chunk, never a body.
  async function openSequential(taskSignal:AbortSignal):Promise<ReadableStream<Uint8Array>> {
    current();const task=abortScope([signal,taskSignal]);let stream:Awaited<ReturnType<NonNullable<ReaderSessionInput['openSequential']>>>;
    try {
      throwIfAborted(task.signal);if(typeof session.openSequential!=='function')throw new ReaderError('invalid_source');
      stream=await session.openSequential(task.signal);
      try{throwIfAborted(task.signal);current();}catch(error){await stream.cancel(error).catch(()=>{});throw error;}
    }catch(error){task.dispose();throw error;}
    let reader:ReturnType<typeof stream.getReader>;
    try{reader=stream.getReader();}catch(error){task.dispose();throw error;}
    let controller:ReadableStreamDefaultController<Uint8Array>,ended=false,total=0;
    const unlock=()=>{try{reader.releaseLock();}catch{/* pending read releases after cancellation */}};
    const finish=(error?:unknown,cancel=false)=>{
      if(ended)return;ended=true;task.signal.removeEventListener('abort',abort);task.dispose();
      if(!cancel){if(error!==undefined)controller.error(error);else controller.close();}
      if(error!==undefined||cancel)return reader.cancel(error).catch(()=>{}).finally(unlock);
      unlock();
    };
    const abort=()=>finish(abortReason(task.signal));
    return new ReadableStream<Uint8Array>({
      start(value){controller=value;task.signal.addEventListener('abort',abort,{once:true});if(task.signal.aborted)abort();},
      async pull(){
        if(ended)return;
        try {
          current();const next=await reader.read();if(ended)return;throwIfAborted(task.signal);current();
          if(next.done){if(total!==size)throw new ReaderError('invalid_source');finish();return;}
          if(!(next.value instanceof Uint8Array)||next.value.length===0||total+next.value.length>size)throw new ReaderError('invalid_source');
          if(next.value.length>1024*1024)throw new ReaderError('limit_exceeded');
          total+=next.value.length;controller.enqueue(next.value.slice());
        }catch(error){finish(error);}finally{if(ended)unlock();}
      },
      cancel(reason){return finish(reason,true);}
    },{highWaterMark:0});
  }
  const dispose=()=>{life.abort();scope.dispose();};
  try {
    current();const format=manifest.format,comic=format==='cbz'||format==='cbr';
    if(format!=='epub'&&format!=='pdf'&&!comic)throw new ReaderError('unsupported_format');
    // A comic's pages are the server's enumerated names. Without them there is
    // nothing to draw, and a forced read would throw a TypeError instead of the
    // reader's own refusal on a perfectly valid session answer.
    if(comic&&(!Array.isArray(manifest.pageNames)||manifest.pageNames.length===0))throw new ReaderError('invalid_source');
    const pageNames=Object.freeze([...(manifest.pageNames??[])]);
    if(sequential===true&&(format!=='pdf'||typeof session.openSequential!=='function'))throw new ReaderError('invalid_source');
    if(!id||!revision||!Number.isSafeInteger(size)||size<1)throw new ReaderError('invalid_source');
    const direction=manifest.metadata.direction;
    if(direction!==undefined&&direction!=='ltr'&&direction!=='rtl'&&direction!=='vertical')throw new ReaderError('invalid_source');
    const {direction:_fileDirection,...identity}=manifest.metadata;
    const metadata:ReaderMetadata=Object.freeze({...identity,...direction===undefined?{}:{direction},
      ...comic?{pageCount:manifest.pageNames!.length}:{}});
    let source:ReaderSource;
    if(format==='pdf') {
      await session.headPDF(signal);current();
      source=sequential===true?Object.freeze({id,revision,size,sequential:Object.freeze({open:openSequential})}):Object.freeze({id,revision,size,async read(range:ReaderRange,taskSignal:AbortSignal) {
        const length=range.endExclusive-range.start;
        if(!Number.isSafeInteger(range.start)||!Number.isSafeInteger(range.endExclusive)||range.start<0||length<0||range.endExclusive>size)
          throw new ReaderError('invalid_source');
        if(length>65536)throw new ReaderError('limit_exceeded');
        return run(taskSignal,async active=>{
          if(length===0)return new Uint8Array();const bytes=await session.readRange(range,active);
          if(!(bytes instanceof Uint8Array)||bytes.length!==length)throw new ReaderError('invalid_source');return bytes.slice();
        });
      }});
    } else if(format==='epub') {
      const entries=Object.freeze((manifest.resources??[]).map(entry=>Object.freeze({...entry})));
      const spine=Object.freeze([...(manifest.spine??[])]);
      // The optional indexed port exists only for a native, non-converted EPUB
      // with a captured spine and the retained session facade that owns search.
      const indexed=manifest.conversion===undefined&&spine.length>0&&typeof session.search==='function';
      const indexedSearch=indexed?async(query:string,options:Parameters<NonNullable<ReaderSource['indexedSearch']>>[1])=>{
        const page=await run(options.signal,active=>session.search!(query,{limit:options.limit,...(options.cursor===undefined?{}:{cursor:options.cursor}),signal:active}));
        if(!Number.isSafeInteger(page.indexRevision)||page.indexRevision<0)throw new ReaderError('invalid_book');
        return Object.freeze({sourceId:page.sourceId,sourceRevision:page.sourceRevision,indexRevision:page.indexRevision,
          coverage:Object.freeze({status:page.coverage.status,...(page.coverage.reason===undefined?{}:{reason:page.coverage.reason})}),
          hits:Object.freeze(page.hits.map(hit=>Object.freeze({sectionPath:hit.sectionPath,sectionIndex:hit.sectionIndex,utf16Offset:hit.utf16Offset,excerpt:hit.excerpt,matchStart:hit.matchStart,matchLength:hit.matchLength}))),
          ...(page.next===undefined?{}:{next:page.next})});
      }:undefined;
      source=Object.freeze({id,revision,size,contents,
        ...(indexed?{spine,indexedSearch}:{}),
        resources:Object.freeze({entries,async read(path:string,taskSignal:AbortSignal) {
        const entry=entries.find(entry=>entry.path===path);if(!entry)throw new ReaderError('invalid_source');
        return run(taskSignal,async active=>{const bytes=await session.readAsset(path,active);
          if(!(bytes instanceof Uint8Array)||bytes.length!==entry.size)throw new ReaderError('invalid_source');return bytes.slice();});
      }})});
    } else source=Object.freeze({id,revision,size});
    const publication:ReadingPublication={format,source,metadata,signal,dispose,
      contentsTarget(index) {
        current();
        if(format!=='epub'||!Number.isSafeInteger(index)||index<0||index>=contents.length)throw new ReaderError('invalid_location');
        return Object.freeze({sourceId:id,sourceRevision:revision,index});
      },
      resumeLocation(response) {
        current();const progress=response.progress;if(progress===null)return null;
        if(progress.sourceId!==id)throw new ReaderError('invalid_source');
        if(progress.stale||progress.sourceRevision!==revision)return null;
        const location=readerLocationFromReading(progress.location);validateLocation(location,format);
        if((location.format==='cbz'||location.format==='cbr')&&location.page>=pageNames.length)throw new ReaderError('invalid_location');return location;
      },
      ...comic?{async comicPages(candidate:ReaderSource,parent:AbortSignal):Promise<ComicPageProvider> {
        current();throwIfAborted(parent);if(candidate!==source)throw new ReaderError('invalid_source');
        const providerLife=new AbortController(),providerScope=abortScope([signal,parent,providerLife.signal]);
        const provider:ComicPageProvider={id,revision,pageCount:pageNames.length,metadata,
          async page(index,options) {
            throwIfAborted(providerScope.signal);
            if(!Number.isInteger(index)||index<0||index>=provider.pageCount||!Number.isInteger(options.width)||options.width<32||options.width>2048)
              throw new ReaderError('invalid_location');
            const task=abortScope([providerScope.signal,options.signal]);
            try{return await run(task.signal,async active=>{
              const page=await session.page(index,{width:options.width,signal:active});
              if(page.index!==index||page.name!==pageNames[index]||!(page.bytes instanceof Uint8Array)||page.bytes.length>16*1024*1024)
                throw new ReaderError('invalid_book');
              const image=rasterInfo(page.bytes);
              if(image.mime!==page.mime||image.width>options.width)throw new ReaderError('invalid_book');
              return Object.freeze({index,name:page.name,bytes:page.bytes.slice(),image});
            });}finally{task.dispose();}
          },dispose(){providerLife.abort();providerScope.dispose();}};
        return Object.freeze(provider);
      }}:{}
    };
    return Object.freeze(publication);
  }catch(error){dispose();throw error;}
}
