import {serverPath} from './query/server-api.ts';
import {itemChapters} from './book-chapters.ts';
import {unreadableServerResponse} from './server-messages.ts';
/** The playing title's chapters for the player's Chapters list, from the title's item read
 * (`GET /v1/items/{id}`) for the version the session plays. Seeking is the playback adapter's. */
import type {ContentScope,LibraryContentApi} from './library-content.ts';
export type PlayerChaptersTarget=Readonly<{libraryId:string;itemId:string;sessionId:string;sessionGeneration:number;sourceId?:string}>;
export type PlayerChapter=Readonly<{id:string;index:number;title:string;startSeconds:number;endSeconds:number;thumbnailUrl?:string;action:Readonly<{id:'seek';positionSeconds:number}>}>;
export type PlayerChaptersData=Readonly<{scope:Readonly<{serverId:string;libraryId:string;itemId:string;viewerFence:string;sessionId:string;generation:number;sourceId:string}>;revision:string;status:'available'|'none'|'unavailable';reason:null|'chapter_facts_unavailable'|'invalid_chapter_timeline'|'session_source_unverified'|'session_inactive'|'prepared_chapter_mapping_unavailable';duration:number;chapters:readonly PlayerChapter[];totalCount:number;nextCursor:string}>;
export type PlayerChaptersSnapshot=Readonly<{generation:number;scope:ContentScope;target:PlayerChaptersTarget|null;phase:'idle'|'loading'|'ready'|'error'|'refresh-required';data:PlayerChaptersData|null;pagination:Readonly<{cursor:string|null;canPrevious:boolean}>;error:Readonly<{code:string;message:string;retryable:boolean}>|null}>;
const text=(v:unknown,max=1024):v is string=>typeof v==='string'&&v.length<=max&&!/[\x00-\x1f\x7f]/.test(v);
const id=(v:unknown):v is string=>text(v,256)&&/^[A-Za-z0-9_-]+$/.test(v);
const integer=(v:unknown):v is number=>Number.isSafeInteger(v)&&Number(v)>=0;
class Failure extends Error{code:string;constructor(code:string,message:string){super(message);this.code=code;}}
function bound(s:ContentScope):ContentScope{if(!id(s.serverId)||!text(s.viewerId,4096)||!s.viewerId)throw new Error('Chapters require a bound viewer and server.');return Object.freeze({...s});}
function target(t:PlayerChaptersTarget):PlayerChaptersTarget{if(!t||typeof t!=='object'||!id(t.libraryId)||!id(t.itemId)||!id(t.sessionId)||!integer(t.sessionGeneration)||t.sessionGeneration<1||t.sourceId!==undefined&&!id(t.sourceId))throw new Error('Invalid chapter session target.');return Object.freeze({...t});}
/** The whole list in one page: a book's chapters are part of its item read. */
function project(item:unknown,t:PlayerChaptersTarget,scope:ContentScope):PlayerChaptersData{
 const read=itemChapters(item,t.sourceId);if(!read)throw new Failure('invalid_chapters',unreadableServerResponse);
 const sourceId=read.versionId,duration=read.durationMs/1000;
 const chapters=read.spans.map((span,n)=>Object.freeze({id:`${sourceId}:${n+1}`,index:n+1,title:span.title,startSeconds:span.startMs/1000,endSeconds:span.endMs/1000,action:Object.freeze({id:'seek' as const,positionSeconds:span.startMs/1000})}));
 return Object.freeze({scope:Object.freeze({serverId:scope.serverId,libraryId:t.libraryId,itemId:t.itemId,viewerFence:'profile',sessionId:t.sessionId,generation:t.sessionGeneration,sourceId}),
  revision:sourceId,status:chapters.length?'available':'none',reason:null,duration,chapters:Object.freeze(chapters),totalCount:chapters.length,nextCursor:''});
}
export class PlayerChaptersService{
 private api:LibraryContentApi;private scope:ContentScope;private timeoutMs:number;private generation=0;private disposed=false;private controller?:AbortController;private pending?:Promise<void>;private key='';private current?:PlayerChaptersTarget;private state:PlayerChaptersSnapshot;private listeners=new Set<()=>void>();
 /** `pageSize` is accepted for older callers; the list is always one page. */
 constructor(options:{api:LibraryContentApi;scope:ContentScope;timeoutMs?:number;pageSize?:number}){this.api=options.api;this.scope=bound(options.scope);this.timeoutMs=options.timeoutMs??15000;if(typeof this.timeoutMs!=='number'||!(this.timeoutMs>=1&&this.timeoutMs<=60000))throw new Error('Invalid chapter read limits.');this.state=this.empty();}
 getSnapshot=()=>this.state;subscribe=(f:()=>void)=>{this.listeners.add(f);return()=>{this.listeners.delete(f);};};
 private empty():PlayerChaptersSnapshot{return Object.freeze({generation:this.generation,scope:this.scope,target:null,phase:'idle',data:null,pagination:Object.freeze({cursor:null,canPrevious:false}),error:null});}
 private publish(p:Partial<PlayerChaptersSnapshot>){this.state=Object.freeze({...this.state,...p,generation:this.generation});for(const f of this.listeners)f();}
 private active(){if(this.disposed)throw new Error('Chapter reader disposed.');}
 private stop(){this.generation++;this.controller?.abort();this.pending=undefined;this.key='';}
 select(t:PlayerChaptersTarget){this.active();return this.load(target(t));}
 next(){this.active();return Promise.resolve();}
 previous(){this.active();return Promise.resolve();}
 refresh(){this.active();return this.state.target?this.select(this.state.target):Promise.resolve();}
 retry(){this.active();return this.current?this.load(this.current):Promise.resolve();}
 cancel(){this.active();this.stop();this.current=undefined;this.publish(this.empty());}
 setScope(s:ContentScope,api:LibraryContentApi){this.active();const checked=bound(s);this.stop();this.scope=checked;this.api=api;this.current=undefined;this.publish(this.empty());}
 dispose(){this.stop();this.disposed=true;this.current=undefined;this.listeners.clear();}
 private load(t:PlayerChaptersTarget):Promise<void>{const key=JSON.stringify(t);if(this.pending&&key===this.key)return this.pending;this.stop();this.key=key;this.current=t;const c=new AbortController(),g=this.generation;this.controller=c;this.publish({target:t,phase:'loading',data:null,error:null,pagination:Object.freeze({cursor:null,canPrevious:false})});const p=this.read(t,this.api,this.scope,c,g);this.pending=p;return p;}
 private async read(t:PlayerChaptersTarget,api:LibraryContentApi,scope:ContentScope,c:AbortController,g:number){let timer:ReturnType<typeof setTimeout>|undefined;let abort=()=>{};try{
  const raw=await Promise.race([api.request<unknown>(serverPath('GET /v1/items/{id}',{id:t.itemId}),'GET',undefined,c.signal),new Promise<never>((_,reject)=>{abort=()=>reject(new Failure('cancelled','Chapter read canceled.'));c.signal.addEventListener('abort',abort,{once:true});timer=setTimeout(()=>{reject(new Failure('timeout','Chapter read timed out.'));c.abort();},this.timeoutMs);})]);
  if(this.disposed||g!==this.generation)return;
  this.publish({target:t,phase:'ready',data:project(raw,t,scope),pagination:Object.freeze({cursor:null,canPrevious:false})});
 }catch(e){if(this.disposed||g!==this.generation)return;const err=e as {code?:unknown;status?:unknown};const code=err?.status===401?'unauthorized':err?.status===403?'forbidden':typeof err?.code==='string'?err.code:'request_failed';
  this.publish({phase:'error',data:null,error:Object.freeze({code,message:['unauthorized','forbidden','permission_changed'].includes(code)?'This viewer cannot read these chapters.':code==='invalid_chapters'?unreadableServerResponse:'Chapters could not be loaded. Try again.',retryable:['timeout','request_failed'].includes(code)})});
 }finally{if(timer)clearTimeout(timer);c.signal.removeEventListener('abort',abort);if(g===this.generation)this.pending=undefined;}}
}
