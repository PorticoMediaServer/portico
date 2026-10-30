import type {ReaderSource} from '../ports.ts';
import {archiveLimits,checkAbort,ComicInputError,naturalCompare,readDirectory,readEntry,type ArchiveLimits,type ZipEntry} from './zip.ts';
import {comicInfo,rasterInfo,type RasterInfo} from './image.ts';
import {abortScope} from '../abort.ts';

export type ComicPage=Readonly<{index:number;name:string;bytes:Uint8Array;image:RasterInfo}>;
export type ComicPageProvider=Readonly<{
  id:string;revision:string;pageCount:number;metadata:Readonly<{title?:string;author?:string;direction?:'ltr'|'rtl'|'vertical'}>;
  /** Authorised, sized raster only. width is a maximum requested render width. */
  page(index:number,options:{width:number;signal:AbortSignal}):Promise<ComicPage>;
  dispose():void|Promise<void>;
}>;
export async function openComicArchive(source:ReaderSource,signal:AbortSignal,limits:ArchiveLimits=archiveLimits):Promise<ComicPageProvider> {
  const id=source.id,revision=source.revision,size=source.size;
  const unchanged=()=>source.id===id&&source.revision===revision&&source.size===size;
  const entries=await readDirectory(source,signal,limits);
  if(!unchanged())throw new ComicInputError('archive_invalid');
  const pages=entries.filter(e=>/\.(?:jpe?g|png|webp|gif)$/i.test(e.name)&&!e.name.split('/').some(p=>p.startsWith('.')||p==='__MACOSX')).sort((a,b)=>naturalCompare(a.name,b.name));
  if(!pages.length)throw new ComicInputError('image_invalid');
  const info:ZipEntry|undefined=entries.find(e=>/(?:^|\/)ComicInfo\.xml$/i.test(e.name));
  if(info&&info.size>128*1024)throw new ComicInputError('archive_limit');
  const metadata=info?comicInfo(await readEntry(source,info,signal,limits)):{};
  if(!unchanged())throw new ComicInputError('archive_invalid');
  let closed=false;const life=new AbortController();
  return Object.freeze({id,revision,pageCount:pages.length,metadata,
    async page(index:number,options:{width:number;signal:AbortSignal}):Promise<ComicPage>{
      if(closed)throw new ComicInputError('closed');
      if(source.id!==id||source.revision!==revision||source.size!==size)throw new ComicInputError('archive_invalid');
      if(!Number.isInteger(index)||index<0||index>=pages.length||!Number.isFinite(options.width)||options.width<1||options.width>32768)throw new ComicInputError('page_invalid');
      const task=abortScope([options.signal,life.signal]),taskSignal=task.signal;
      try {checkAbort(taskSignal);
      const entry=pages[index],bytes=await readEntry(source,entry,taskSignal,limits);checkAbort(taskSignal);
      if(source.id!==id||source.revision!==revision||source.size!==size)throw new ComicInputError('archive_invalid');
      return Object.freeze({index,name:entry.name,bytes,image:rasterInfo(bytes)});
      }finally{task.dispose();}
    },dispose(){if(!closed){closed=true;life.abort();}}
  });
}
