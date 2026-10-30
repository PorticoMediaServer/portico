/** A bounded reader for server-sent events over `fetch`.
 *
 * The browser's EventSource cannot send an Authorization header, and every Portico stream
 * needs one. This reads the same wire format from a fetch response: `event:`, `data:` (joined
 * across lines), `id:` and `retry:`, frames separated by a blank line, comment lines ignored.
 * One frame is capped so a misbehaving server cannot grow memory without bound. The cap counts
 * the characters kept for the frame being read: its event name, its data lines so far, the last
 * id (which outlives its frame) and the unfinished line. A field sent again replaces what was
 * kept. It is a count of content, not of bytes on the heap, and not of how much arrived in one
 * read: many small complete frames in one chunk are fine. */
export type ServerEvent=Readonly<{event:string;data:string;id:string}>;
const MAX_FRAME=262144;

export async function readEventStream(response:Response,onEvent:(event:ServerEvent)=>void,signal?:AbortSignal):Promise<void>{
 if(!response.ok)throw Object.assign(new Error('The event stream was refused.'),{status:response.status});
 if(!response.body)throw new Error('This client cannot read event streams.');
 // A platform without streaming fetch (React Native) adapts its own transport and may hand over
 // text directly, so the decoder is only made when bytes actually arrive.
 const reader=(response.body as unknown as {getReader():{read():Promise<{value?:Uint8Array|string;done:boolean}>;cancel():Promise<void>;releaseLock():void}}).getReader();let decoder:TextDecoder|undefined;
 let buffer='',event='',data:string[]=[],id='',held=0,afterCR=false;
 const oversized=()=>new Error('The event stream sent an oversized frame.');
 const kept=()=>held+event.length+id.length;
 const abort=()=>{void reader.cancel().catch(()=>{});};
 signal?.addEventListener('abort',abort,{once:true});
 const dispatch=()=>{if(data.length||event)onEvent(Object.freeze({event:event||'message',data:data.join('\n'),id}));event='';data=[];held=0;};
 try{
  for(;;){
   if(signal?.aborted)return;
   const {value,done}=await reader.read();
   if(done){return;}
   let text=typeof value==='string'?value:(decoder??=new TextDecoder()).decode(value,{stream:true});
   // The last read ended on a CR, which was taken as a line ending. If this one begins with the
   // LF of the same CRLF, that LF is not a second, empty line.
   if(afterCR&&text){if(text.startsWith('\n'))text=text.slice(1);afterCR=false;}
   buffer+=text;
   let index:number;
   while((index=buffer.search(/\r\n|\n|\r/))>=0){
    const line=buffer.slice(0,index),crlf=buffer.startsWith('\r\n',index);
    afterCR=!crlf&&buffer[index]==='\r'&&index===buffer.length-1;
    buffer=buffer.slice(index+(crlf?2:1));
    if(line.length>MAX_FRAME)throw oversized();
    if(line===''){dispatch();continue;}
    if(line.startsWith(':'))continue;
    const colon=line.indexOf(':'),field=colon<0?line:line.slice(0,colon);
    let value2=colon<0?'':line.slice(colon+1);if(value2.startsWith(' '))value2=value2.slice(1);
    if(field==='event')event=value2;else if(field==='data'){data.push(value2);held+=value2.length+1;}else if(field==='id'&&!value2.includes('\0'))id=value2;
    if(kept()>MAX_FRAME)throw oversized();
   }
   if(kept()+buffer.length>MAX_FRAME)throw oversized();
  }
 }finally{signal?.removeEventListener('abort',abort);void reader.cancel().catch(()=>{});try{reader.releaseLock();}catch{}}
}
