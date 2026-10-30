/** Selected-server JSON transport. Native fetch buffers responses; refuse a declared
 * length above the bound and verify the actual UTF-8 size before parsing.
 * Browser fetch is incrementally bounded. Error bodies never become UI text. */
export function consoleUTF8Size(value:string):number {
 let bytes=0;
 for(let i=0;i<value.length;i++){
  const c=value.charCodeAt(i);
  if(c<0x80)bytes++;
  else if(c<0x800)bytes+=2;
  else if(c>=0xd800&&c<=0xdbff&&i+1<value.length&&value.charCodeAt(i+1)>=0xdc00&&value.charCodeAt(i+1)<=0xdfff){bytes+=4;i++;}
  else bytes+=3;
 }
 return bytes;
}
export class ConsoleTransportError extends Error {
 status:number;code:string;currentRevision?:number;fields?:string[];
 constructor(status:number,code:string,currentRevision?:number,fields?:string[]){
  const messages:Record<string,string>={
   unauthorized:'This selected-server session no longer has the required access.',
   invalid_console_request:'Check the indicated fields and their limits.',
   console_conflict:'The document changed. Read its current revision before reapplying your changes.',
   console_capacity:'The server is busy or a local quota was reached. Retry this action later.',
   console_expired:'This private record or operation receipt expired. Read the current state first.',
   not_found:'This record is no longer available in the selected scope.',
   console_unavailable:'The server could not confirm the result. Retry the same action to reconcile it.',
  };
  const aliases:Record<string,string>={invalid_request:'invalid_console_request',revision_mismatch:'console_conflict',rate_limited:'console_capacity',internal:'console_unavailable'};
  super(messages[aliases[code]??code]??messages.console_unavailable);this.status=status;this.code=code;this.currentRevision=currentRevision;this.fields=fields; if(currentRevision!==undefined)this.message+=' Current revision: '+currentRevision+'.';if(fields?.length)this.message+=' Fields: '+fields.join(', ')+'.';
 }
}
function bad():never{throw new ConsoleTransportError(502,'console_unavailable');}
export async function readConsoleJson<T>(response:Response,signal:AbortSignal):Promise<T>{
 const max=response.ok?1048576:8192;const length=response.headers.get('Content-Length');
 const cancel=()=>{try{void response.body?.cancel().catch(()=>{});}catch{}};
 if(response.headers.get('Content-Type')?.split(';')[0].trim().toLowerCase()!=='application/json'||length!==null&&(!/^\d+$/.test(length)||Number(length)>max)){cancel();bad();}
 let raw:string;
 if(response.body&&typeof response.body.getReader==='function'){
  const reader=response.body.getReader();let reject!:(e:Error)=>void;const aborted=new Promise<never>((_,r)=>{reject=r;});
  const abort=()=>{void reader.cancel().catch(()=>{});reject(new ConsoleTransportError(0,'console_unavailable'));};signal.addEventListener('abort',abort,{once:true});
  try{
   if(signal.aborted)abort();const chunks:Uint8Array[]=[];let count=0;
   for(;;){const next=await Promise.race([reader.read(),aborted]);if(next.done)break;count+=next.value.byteLength;if(count>max)bad();chunks.push(next.value);}
   const bytes=new Uint8Array(count);let offset=0;for(const chunk of chunks){bytes.set(chunk,offset);offset+=chunk.length;}
   try{raw=new TextDecoder('utf-8',{fatal:true}).decode(bytes);}catch{bad();}
  }finally{signal.removeEventListener('abort',abort);void reader.cancel().catch(()=>{});try{reader.releaseLock();}catch{}}
 }else{
  // React Native's platform transport supplies text(), but not a ReadableStream. It has already
  // buffered the whole body, so a missing Content-Length (a compressing proxy sends chunked gzip)
  // protects nothing; a declared length above the bound was refused above, and the decoded size is
  // checked here.
  raw=await response.text();if(consoleUTF8Size(raw)>max)bad();
 }
 if(signal.aborted)throw new ConsoleTransportError(0,'console_unavailable');
 let value:unknown;try{value=JSON.parse(raw);}catch{bad();}
 if(!response.ok){
  const e=(value&&typeof value==='object'&&!Array.isArray(value)?(value as {error?:unknown}).error:undefined);
  const data=e&&typeof e==='object'&&!Array.isArray(e)?e as Record<string,unknown>:{};
  const code=response.status===401||response.status===403?'unauthorized':typeof data.code==='string'?data.code:'console_unavailable';
  const current=data.current&&typeof data.current==='object'&&!Array.isArray(data.current)?data.current as Record<string,unknown>:{};
  const revisionValue=data.currentRevision??current.revision;
  const revision=typeof revisionValue==='number'&&Number.isSafeInteger(revisionValue)&&revisionValue>=0?revisionValue:undefined;
  const fieldPaths=Array.isArray(data.fields)?data.fields.map(f=>typeof f==='string'?f:f&&typeof f==='object'&&!Array.isArray(f)?(f as Record<string,unknown>).path:undefined):undefined;
  const fields=fieldPaths&&fieldPaths.length<=20&&fieldPaths.every(f=>typeof f==='string'&&/^[A-Za-z][A-Za-z0-9.\[\]-]{0,100}$/.test(f))?fieldPaths as string[]:undefined;
  throw new ConsoleTransportError(response.status,code,revision,fields);
 }
 return value as T;
}
