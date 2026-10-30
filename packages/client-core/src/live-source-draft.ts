/** In-memory, single-viewer draft. Root clears this on logout/server/viewer changes. */
export type LiveSourceInput={id:string;requestId:string;expectedRevision:number;name:string;playlist:string;guide:string;tunerCount:number};
export const channelRequestID=()=>Array.from(crypto.getRandomValues(new Uint8Array(24)),v=>v.toString(16).padStart(2,'0')).join('');
export const newLiveSourceInput=():LiveSourceInput=>({id:channelRequestID(),requestId:channelRequestID(),expectedRevision:0,name:'',playlist:'',guide:'',tunerCount:0});
export class LiveSourceDraft{
 private value:LiveSourceInput=newLiveSourceInput();private listeners=new Set<()=>void>();private reads={playlist:0,guide:0};private disposed=false;private reading={playlist:false,guide:false};
 get isReading(){return this.reading.playlist||this.reading.guide;}
 getSnapshot=()=>this.value;subscribe=(fn:()=>void)=>{this.listeners.add(fn);return()=>this.listeners.delete(fn)};
 private publish(value:LiveSourceInput){if(this.disposed)return;this.value=Object.freeze(value);for(const fn of this.listeners)fn();}
 update(patch:Partial<LiveSourceInput>){this.publish({...this.value,...patch,requestId:channelRequestID()});}
 replace(value:LiveSourceInput){this.reads.playlist++;this.reads.guide++;this.reading={playlist:false,guide:false};this.publish(value);}
 dispose(){this.disposed=true;this.reads.playlist++;this.reads.guide++;this.value=newLiveSourceInput();for(const fn of this.listeners)fn();this.listeners.clear();}
 async file(field:'playlist'|'guide',read:()=>Promise<string>):Promise<boolean>{const ticket=++this.reads[field];this.reading[field]=true;this.publish({...this.value});try{const value=await read();if(this.disposed||ticket!==this.reads[field])return false;const other=field==='playlist'?this.value.guide:this.value.playlist;if(new TextEncoder().encode(value).length+new TextEncoder().encode(other).length>16*1024*1024)throw new Error('The playlist and guide together must be no larger than 16 MB.');this.update({[field]:value});return true}finally{if(!this.disposed&&ticket===this.reads[field]){this.reading[field]=false;this.publish({...this.value})}}}
}
let current:{scope:string;draft:LiveSourceDraft}|null=null;
export function liveSourceDraft(scope:string):LiveSourceDraft{if(!current||current.scope!==scope){current?.draft.dispose();current={scope,draft:new LiveSourceDraft()}}return current.draft;}
export function clearLiveSourceDraft(scope?:string){if(scope!==undefined&&current?.scope!==scope)return;current?.draft.dispose();current=null;}
export type LiveSourceAuthority=Readonly<{scope:string;generation:object}>;
let authority:LiveSourceAuthority|null=null;
/** Each authenticated WorkspaceApi lifetime owns one generation. Input edits do not change it. */
export function beginLiveSourceAuthority(scope:string):LiveSourceAuthority{clearLiveSourceDraft();authority=Object.freeze({scope,generation:{}});return authority;}
export function denyLiveSourceAuthority(expected:LiveSourceAuthority){if(authority===expected)clearLiveSourceDraft(expected.scope);}
export function endLiveSourceAuthority(expected:LiveSourceAuthority){if(authority!==expected)return;clearLiveSourceDraft(expected.scope);authority=null;}
