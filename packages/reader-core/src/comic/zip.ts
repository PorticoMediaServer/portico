import {throwSignalAborted as throwIfAborted} from '../abort.ts';
import {abortReason} from '../abort.ts';
import type {ReaderSource} from '../ports.ts';

/** Local/offline CBZ only. Online readers use authorised page variants. */
export type ZipEntry = Readonly<{name:string; method:number; flags:number; crc:number; compressed:number; size:number; offset:number; end:number}>;
export type ArchiveLimits = Readonly<{entries:number; directoryBytes:number; pageBytes:number; totalBytes:number; ratio:number; rangeBytes:number}>;
export const archiveLimits: ArchiveLimits = Object.freeze({entries:10000,directoryBytes:4*1024*1024,pageBytes:32*1024*1024,totalBytes:1024*1024*1024,ratio:1000,rangeBytes:256*1024});
export class ComicInputError extends Error {
  readonly code:'archive_invalid'|'archive_limit'|'archive_unsupported'|'image_invalid'|'image_limit'|'page_invalid'|'closed';
  constructor(code:ComicInputError['code']){super(code);this.name='ComicInputError';this.code=code;}
}
export function checkAbort(signal:AbortSignal):void {throwIfAborted(signal);}
const fail = (code:ComicInputError['code']='archive_invalid'):never => {throw new ComicInputError(code);};
const u16 = (b:Uint8Array,n:number) => new DataView(b.buffer,b.byteOffset,b.byteLength).getUint16(n,true);
const u32 = (b:Uint8Array,n:number) => new DataView(b.buffer,b.byteOffset,b.byteLength).getUint32(n,true);
const decoder = new TextDecoder('utf-8',{fatal:true});
function nameOf(b:Uint8Array,flags:number):string {
  // ASCII is shared by legacy ZIP encodings. Never guess non-UTF8 page names.
  if(!(flags&0x800)&&b.some(v=>v>127))return fail('archive_unsupported');
  let name:string;try{name=decoder.decode(b);}catch{return fail();}
  if(!name||name.length>2048||/[\x00-\x1f\x7f\\]/.test(name)||name.startsWith('/')||/^[a-z]:/i.test(name)||name.split('/').some(p=>p==='..'||p==='.'))return fail();
  return name;
}
export async function readRange(source:ReaderSource,start:number,endExclusive:number,signal:AbortSignal,chunkBytes=archiveLimits.rangeBytes):Promise<Uint8Array> {
  checkAbort(signal);
  if(typeof source.read!=='function'||!Number.isSafeInteger(source.size)||source.size<1||!Number.isSafeInteger(start)||!Number.isSafeInteger(endExclusive)||start<0||endExclusive<start||endExclusive>source.size||!Number.isSafeInteger(chunkBytes)||chunkBytes<1||chunkBytes>1024*1024)return fail();
  const id=source.id,revision=source.revision,size=source.size;
  const result=new Uint8Array(endExclusive-start);
  for(let at=start;at<endExclusive;at+=chunkBytes){
    checkAbort(signal);const end=Math.min(at+chunkBytes,endExclusive);
    const bytes=await source.read({start:at,endExclusive:end},signal);checkAbort(signal);
    if(source.id!==id||source.revision!==revision||source.size!==size||!(bytes instanceof Uint8Array)||bytes.byteLength!==end-at)return fail();
    result.set(bytes,at-start);
  }
  return result;
}
export async function readDirectory(source:ReaderSource,signal:AbortSignal,limits:ArchiveLimits=archiveLimits):Promise<readonly ZipEntry[]> {
  if(source.size<22)return fail();
  const tailStart=Math.max(0,source.size-65557),tail=await readRange(source,tailStart,source.size,signal,limits.rangeBytes);
  let end=-1;
  for(let i=tail.length-22;i>=0;i--){if(u32(tail,i)===0x06054b50&&i+22+u16(tail,i+20)===tail.length){end=i;break;}}
  if(end<0)return fail();
  const count=u16(tail,end+10),size=u32(tail,end+12),offset=u32(tail,end+16);
  if(u16(tail,end+4)!==0||u16(tail,end+6)!==0||u16(tail,end+8)!==count||count===65535||size===0xffffffff||offset===0xffffffff)return fail('archive_unsupported');
  if(!count||count>limits.entries||size>limits.directoryBytes)return fail('archive_limit');
  if(offset+size!==tailStart+end)return fail();
  const dir=await readRange(source,offset,offset+size,signal,limits.rangeBytes),entries:ZipEntry[]=[],names=new Set<string>();let at=0,total=0;
  for(let n=0;n<count;n++){
    if(at+46>dir.length||u32(dir,at)!==0x02014b50)return fail();
    const flags=u16(dir,at+8),method=u16(dir,at+10),compressed=u32(dir,at+20),bytes=u32(dir,at+24),nl=u16(dir,at+28),el=u16(dir,at+30),cl=u16(dir,at+32),local=u32(dir,at+42);
    if(at+46+nl+el+cl>dir.length)return fail();
    if(flags&~0x080e||flags&1||method!==0&&method!==8||u16(dir,at+34)!==0||compressed===0xffffffff||bytes===0xffffffff||local===0xffffffff)return fail('archive_unsupported');
    // Reject UNIX symlinks; no entry is ever extracted to a filesystem.
    if((u32(dir,at+38)>>>16&0xf000)===0xa000)return fail();
    const name=nameOf(dir.subarray(at+46,at+46+nl),flags),key=name.normalize('NFC').toLowerCase();
    if(names.has(key)||local+30>offset||compressed>limits.pageBytes||bytes>limits.pageBytes||bytes>Math.max(1,compressed)*limits.ratio)return fail('archive_limit');
    names.add(key);total+=bytes;if(total>limits.totalBytes)return fail('archive_limit');
    let extra=at+46+nl;
    while(extra<at+46+nl+el){if(extra+4>at+46+nl+el)return fail();const tag=u16(dir,extra),len=u16(dir,extra+2);extra+=4;if(extra+len>at+46+nl+el)return fail();if(tag===1)return fail('archive_unsupported');extra+=len;}
    entries.push({name,flags,method,compressed,size:bytes,offset:local,end:offset,crc:u32(dir,at+16)});
    at+=46+nl+el+cl;
  }
  if(at!==dir.length)return fail();
  // Local headers/data may not overlap one another or the central directory.
  const sorted=[...entries].sort((a,b)=>a.offset-b.offset);
  const ends=new Map<number,number>();
  for(let i=0;i<sorted.length;i++){
    const end=sorted[i+1]?.offset??offset;if(sorted[i].offset+30+sorted[i].compressed>end)return fail();
    ends.set(sorted[i].offset,end);
  }
  return Object.freeze(entries.map(e=>Object.freeze({...e,end:ends.get(e.offset)!})));
}
const crcTable=Uint32Array.from({length:256},(_,n)=>{let crc=n;for(let i=0;i<8;i++)crc=crc>>>1^(-(crc&1)&0xedb88320);return crc>>>0;});
export function crc32(bytes:Uint8Array):number {let crc=0xffffffff;for(const b of bytes)crc=crc>>>8^crcTable[(crc^b)&255];return (crc^0xffffffff)>>>0;}

/** Local/offline only. Reuse the already pinned ZIP codec when the host has
 * no native raw-DEFLATE stream. Reads remain bounded ranges of the original
 * archive; only the selected member is inflated, into a capped destination. */
async function bundledInflate(source:ReaderSource,entry:ZipEntry,signal:AbortSignal,limits:ArchiveLimits):Promise<Uint8Array> {
  const {Reader,ZipReader,ERR_INVALID_UNCOMPRESSED_SIZE}=await import('@zip.js/zip.js');checkAbort(signal);
  const id=source.id,revision=source.revision,size=source.size;
  class BoundedReader extends Reader<ReaderSource> {
    constructor(){super(source);this.size=size;}
    readUint8Array(start:number,length:number) {
      if(source.id!==id||source.revision!==revision||source.size!==size)return fail();
      if(!Number.isSafeInteger(length)||length<0||length>Math.max(limits.directoryBytes,limits.pageBytes))return fail('archive_limit');
      return readRange(source,start,start+length,signal,limits.rangeBytes);
    }
  }
  const zip=new ZipReader(new BoundedReader(),{useWebWorkers:false,useCompressionStream:false,checkCrc32:true,strictness:'strict'});
  try {
    let count=0;
    for await(const selected of zip.getEntriesGenerator()) {
      checkAbort(signal);if(++count>limits.entries)return fail('archive_limit');
      if(selected.filename!==entry.name)continue;
      if(selected.directory||selected.encrypted||selected.offset!==entry.offset||selected.compressionMethod!==entry.method
        ||selected.compressedSize!==entry.compressed||selected.uncompressedSize!==entry.size||selected.signature!==entry.crc)return fail();
      const result=new Uint8Array(entry.size);let length=0;
      const sink=new WritableStream<Uint8Array>({write(chunk) {
        checkAbort(signal);if(length+chunk.length>result.length)return fail('archive_limit');result.set(chunk,length);length+=chunk.length;
      }});
      await selected.getData(sink,{signal,checkCrc32:true,useWebWorkers:false,useCompressionStream:false});checkAbort(signal);
      if(length!==entry.size||crc32(result)!==entry.crc)return fail();return result;
    }
    return fail();
  }catch(error){if(signal.aborted)throw abortReason(signal);if(error instanceof ComicInputError)throw error;
    if(error instanceof Error&&error.message===ERR_INVALID_UNCOMPRESSED_SIZE)return fail('archive_limit');return fail();
  }finally{await zip.close();}
}
export async function readEntry(source:ReaderSource,entry:ZipEntry,signal:AbortSignal,limits:ArchiveLimits=archiveLimits):Promise<Uint8Array> {
  if(!Number.isSafeInteger(entry.size)||entry.size<0||entry.size>limits.pageBytes||entry.compressed>limits.pageBytes)return fail('archive_limit');
  const header=await readRange(source,entry.offset,entry.offset+30,signal,limits.rangeBytes);
  if(u32(header,0)!==0x04034b50||u16(header,6)!==entry.flags||u16(header,8)!==entry.method)return fail();
  const nameLength=u16(header,26),extraLength=u16(header,28),start=entry.offset+30+nameLength+extraLength;
  if(start+entry.compressed>entry.end)return fail();
  const name=await readRange(source,entry.offset+30,entry.offset+30+nameLength,signal,limits.rangeBytes);
  if(nameOf(name,entry.flags)!==entry.name)return fail();
  if(!(entry.flags&8)&&(u32(header,14)!==entry.crc||u32(header,18)!==entry.compressed||u32(header,22)!==entry.size))return fail();
  let inflater:DecompressionStream|undefined;
  if(entry.method===8){try{inflater=new DecompressionStream('deflate-raw');}catch{return bundledInflate(source,entry,signal,limits);}}
  const bytes=await readRange(source,start,start+entry.compressed,signal,limits.rangeBytes);
  let result:Uint8Array;
  if(entry.method===0){if(entry.size!==entry.compressed)return fail();result=bytes;}
  else{
    // Stream into a capped destination; never allocate from an inflater's output claim.
    const stream=new Blob([bytes as Uint8Array<ArrayBuffer>]).stream().pipeThrough(inflater!);
    const reader=stream.getReader();result=new Uint8Array(entry.size);let length=0;
    const abort=()=>{void reader.cancel(abortReason(signal)).catch(()=>{});};signal.addEventListener('abort',abort,{once:true});
    try{for(;;){checkAbort(signal);const next=await reader.read();checkAbort(signal);if(next.done)break;if(length+next.value.length>entry.size)return fail('archive_limit');result.set(next.value,length);length+=next.value.length;}if(length!==entry.size)return fail();}
    catch(error){if(signal.aborted)throw abortReason(signal);if(error instanceof ComicInputError)throw error;return fail();}
    finally{signal.removeEventListener('abort',abort);await reader.cancel().catch(()=>{});reader.releaseLock();}
  }
  checkAbort(signal);if(crc32(result)!==entry.crc)return fail();return result;
}
/** Locale-independent ordering, without lossy Number conversion for long digit runs. */
export function naturalCompare(a:string,b:string):number {
  const aa=a.toLowerCase().match(/\d+|\D+/g)??[],bb=b.toLowerCase().match(/\d+|\D+/g)??[];
  for(let i=0;i<Math.min(aa.length,bb.length);i++){
    const x=aa[i],y=bb[i];if(x===y)continue;
    if(/^\d/.test(x)&&/^\d/.test(y)){const xx=x.replace(/^0+/, '')||'0',yy=y.replace(/^0+/, '')||'0';if(xx.length!==yy.length)return xx.length-yy.length;if(xx!==yy)return xx<yy?-1:1;}
    else return x<y?-1:1;
  }
  return aa.length-bb.length||(a<b?-1:a>b?1:0);
}
