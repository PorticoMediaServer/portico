import {playbackIdentity as copyPlaybackIdentity} from './identity.ts';
import {parseQueueMutation, parseQueueSnapshot, queueScope, type QueueScope, type QueueSnapshot, type QueueEntry} from './queues.ts';
import {parseQueuePostPlay, type QueuePostPlay} from './post-play.ts';
import {invalidState, record, seconds, wireId} from './validation.ts';
export type QueueDisplay = Readonly<{entryId:string;itemId:string;title:string;kind:string;libraryId?:string;posterUrl?:string;stillUrl?:string;backdropUrl?:string;artist?:string;album?:string;durationSeconds?:number;showId?:string;seasonId?:string}>;
export type PlayerQueueView = Readonly<{queue:QueueSnapshot;items:readonly QueueDisplay[];next:Readonly<{entryId:string|null;available:boolean;reason:'ready'|'end'|'unavailable'}>;postPlay:QueuePostPlay}>;
export type QueueAction = Readonly<{queueId:string;expectedRevision:string} & ({kind:'next'}|{kind:'previous'}|{kind:'entry';entryId:string}|{kind:'edit';intent:import('./queues.ts').QueueIntent})>;
/** A bounded projection of the existing queue workspace, never a replacement queue engine.
 * Order, availability, shuffle and repeat stay server-owned. Duplicate items keep entry ids. */
export class PlayerQueue {
  private scope:QueueScope; private view:PlayerQueueView|null=null;
  constructor(scope:QueueScope) { this.scope=queueScope(scope); }
  getSnapshot=()=>this.view;
  adopt(raw:unknown):boolean {
    if(!record(raw)||!record(raw.next)||!Array.isArray(raw.items)||raw.items.length>200)invalidState();
    const queue=parseQueueSnapshot(raw.queue,this.scope,this.view?.queue.id);
    if(!['ready','end','unavailable'].includes(String(raw.next.reason))||typeof raw.next.available!=='boolean'||raw.next.entryId!==null&&!wireId(raw.next.entryId))invalidState();
    const next=Object.freeze({entryId:raw.next.entryId as string|null,available:raw.next.available,reason:raw.next.reason as 'ready'|'end'|'unavailable'});
    if(next.available && (!next.entryId||next.reason!=='ready') || next.reason==='end' && next.entryId!==null)invalidState();
    const ids=new Set<string>();
    const items=Object.freeze(raw.items.map((v:unknown)=>{
      if(!record(v)||!wireId(v.entryId)||!wireId(v.itemId)||typeof v.title!=='string'||v.title.length>1024||typeof v.kind!=='string'||v.kind.length>64||ids.has(v.entryId)||v.durationSeconds!==undefined&&!seconds(v.durationSeconds)||v.showId!==undefined&&!wireId(v.showId)||v.seasonId!==undefined&&!wireId(v.seasonId))invalidState();
      const displayKeys=['libraryId','posterUrl','stillUrl','backdropUrl','artist','album'] as const;
      if(displayKeys.some(k=>v[k]!==undefined&&(typeof v[k]!=='string'||(v[k] as string).length>4096)))invalidState();
      const extra=Object.fromEntries(displayKeys.filter(k=>v[k]!==undefined).map(k=>[k,v[k]]));
      const entry=queue.entries.find(e=>e.id===v.entryId);
      if(!entry||entry.hidden||entry.itemId!==v.itemId)invalidState();
      ids.add(v.entryId);
      return Object.freeze({entryId:v.entryId,itemId:v.itemId,title:v.title,kind:v.kind,...extra,...(v.durationSeconds!==undefined?{durationSeconds:v.durationSeconds}:{}),...(v.showId?{showId:v.showId}:{}),...(v.seasonId?{seasonId:v.seasonId}:{})}) as QueueDisplay;
    }));
    const postPlay=parseQueuePostPlay(raw.postPlay,next);
    if(this.view && (BigInt(queue.revision)<BigInt(this.view.queue.revision)||BigInt(queue.highestSeenSequence)<BigInt(this.view.queue.highestSeenSequence)))return false;
    this.view=Object.freeze({queue,items,next,postPlay});return true;
  }
  /** Replace only at a new playback boundary, after the caller resets its session choices. */
  reset(scope:QueueScope=this.scope):void { this.scope=queueScope(scope);this.view=null; }
  private action<T extends {kind:string}>(action:T):Readonly<T&{queueId:string;expectedRevision:string}>|null { return this.view?Object.freeze({...action,queueId:this.view.queue.id,expectedRevision:this.view.queue.revision}):null; }
  next():QueueAction|null { return this.view?.next.available?this.action({kind:'next' as const}):null; }
  previous():QueueAction|null {
    if(!this.view)return null;
    const queue=this.view.queue,at=queue.entries.findIndex(e=>e.id===queue.currentEntryId);
    if(at<0)return null;
    return (queue.entries.slice(0,at).some(visible)||queue.repeat==='all'&&queue.entries.some(visible))?this.action({kind:'previous' as const}):null;
  }
  select(entryId:string):QueueAction|null { return this.view?.queue.entries.some(e=>e.id===entryId&&visible(e))?this.action({kind:'entry' as const,entryId}):null; }
  edit(intent:import('./queues.ts').QueueIntent):QueueAction|null {
    if(!this.view)return null;
    const {operationId,sequence,expectedRevision,...copied}=parseQueueMutation({...intent,operationId:'validation',sequence:'1',expectedRevision:this.view.queue.revision});
    return this.action({kind:'edit' as const,intent:Object.freeze(copied)});
  }
  upcoming():readonly QueueDisplay[] { if(!this.view)return Object.freeze([]);const at=this.view.queue.entries.findIndex(e=>e.id===this.view!.queue.currentEntryId);const ids=new Set(this.view.queue.entries.slice(at+1).filter(visible).map(e=>e.id));return Object.freeze(this.view.queue.entries.filter(e=>ids.has(e.id)).flatMap(e=>{const display=this.view!.items.find(item=>item.entryId===e.id);return display?[display]:[];})); }
}
const visible=(entry:QueueEntry):boolean=>!entry.hidden&&!entry.removed;

/** Existing show-selector/anchor semantics from the web's pure container-play.ts. The complete
 * show is queued by the existing queue adapter, never a loaded season page. All drawer choices
 * retain the caller's playback identity, including a different season (answered Q12). */
export function episodePlayback(identity:import('./identity.ts').PlaybackIdentity,showId:string,itemId:string):Readonly<{
  playback:import('./identity.ts').PlaybackIdentity;
  selector:Readonly<{container:Readonly<{kind:'show';id:string}>}>;
  options:Readonly<{anchor:Readonly<{itemId:string}>}>;
}> {
  if(!wireId(showId)||!wireId(itemId))invalidState();
  return Object.freeze({playback:copyPlaybackIdentity(identity),selector:Object.freeze({container:Object.freeze({kind:'show',id:showId})}),options:Object.freeze({anchor:Object.freeze({itemId})})});
}
