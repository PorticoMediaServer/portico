import {unreadableServerResponse} from './server-messages.ts';
/** Server-authoritative queue projection. No client ordering or activation. */
export type QueueScope = Readonly<{
 serverId: string; authority: 'local' | 'hosted'; accountId: string; profileId: string;
 controllerId: string; controllerEpoch: string; commandLaneId: string;
}>;
export type QueueSourceContext = Readonly<{ kind: 'item' | 'playlist' | 'album' | 'artist' | 'book' | 'collection'; id: string; revision: string | null; entryId: string | null }>;
export type QueueItemInput=Readonly<{itemId:string;editionId:string|null;partId:string|null;sourceContext:QueueSourceContext}>;
export type QueueEntry = Readonly<{ id: string; removed: boolean } & (
 { hidden: true; /** A v1 placeholder still being snapshotted (a large queue building): quiet, not an error. */ pending?: true } | { hidden: false; itemId: string; editionId: string | null; partId: string | null; sourceContext: QueueSourceContext }
)>;
export type QueueEditReceipt = Readonly<{ operationId:string; sequence:string; disposition:'accepted'|'conflict'; acceptedRevision:string|null; replayed:boolean; bodyCompacted:boolean }>;
export type QueueSnapshot = Readonly<{
 id: string; scope: QueueScope; revision: string; highestSeenSequence:string; replayWindow:Readonly<{results:256;bodies:32}>; editReceipt?:QueueEditReceipt; repeat: 'off' | 'one' | 'all'; shuffled: boolean;
 currentEntryId: string | null; currentPlaybackId: string | null; entries: readonly QueueEntry[];
}>;
export type QueueMutation = Readonly<{ operationId: string; sequence:string; expectedRevision: string } & (
 { action:'append'|'insert-next';entries:readonly QueueItemInput[] } | { action:'fence';cancelledSequence:string } | { action:'add'|'play-next';entry:QueueItemInput } | { action: 'remove'; entryId: string } | { action: 'reorder'; entryIds: readonly string[] } |
 { action: 'repeat'; repeat: QueueSnapshot['repeat'] } | { action: 'shuffle'; shuffled: boolean }
)>;
export type QueuePlaybackActions = Readonly<{
 playEntry(queueId: string, entryId: string, expectedQueueRevision: string): Promise<void>;
 next(queueId: string, expectedQueueRevision: string): Promise<void>;
}>;
const keys = ['serverId','authority','accountId','profileId','controllerId','controllerEpoch','commandLaneId'] as const;
const id = (v: unknown): v is string => typeof v === 'string' && /^[A-Za-z0-9_-]{1,128}$/.test(v);
const counter = (v: unknown): v is string => typeof v === 'string' && /^[1-9][0-9]{0,18}$/.test(v) && BigInt(v) <= 9223372036854775807n;
export const queueSequence = (v:unknown):v is string => v === '0' || counter(v);
const record = (v: unknown): v is Record<string, unknown> => v !== null && typeof v === 'object' && !Array.isArray(v);
const optionalID = (v: unknown): v is string | null => v === null || id(v);
function bad(): never { throw new Error(unreadableServerResponse); }
function only(v: Record<string, unknown>, names: readonly string[]): void { if (Object.keys(v).some(k => !names.includes(k))) bad(); }
function freeze<T>(v: T): T { if (v && typeof v === 'object') { for (const child of Object.values(v)) freeze(child); Object.freeze(v); } return v; }
export function queueScope(v: QueueScope): QueueScope {
 if (!record(v) || !keys.every(k => k === 'authority' ? v[k] === 'local' || v[k] === 'hosted' : id(v[k]))) bad();
 return freeze(Object.fromEntries(keys.map(k => [k, v[k]])) as QueueScope);
}
export function parseQueueSnapshot(v: unknown, bound: QueueScope, queueId?: string): QueueSnapshot {
 const scope = queueScope(bound);
 if (!record(v) || !id(v.id) || queueId !== undefined && v.id !== queueId || !record(v.scope) || !keys.every(k => v.scope && (v.scope as Record<string,unknown>)[k] === scope[k])) bad();
 only(v, ['id','scope','revision','highestSeenSequence','replayWindow','editReceipt','repeat','shuffled','currentEntryId','currentPlaybackId','entries']); only(v.scope,keys);
 if (!counter(v.revision) || !['off','one','all'].includes(v.repeat as string) || typeof v.shuffled !== 'boolean' || !optionalID(v.currentEntryId) || !optionalID(v.currentPlaybackId) || !Array.isArray(v.entries) || v.entries.length > 1000) bad();
 if (!queueSequence(v.highestSeenSequence) || !record(v.replayWindow) || v.replayWindow.results!==256 || v.replayWindow.bodies!==32) bad();
 only(v.replayWindow,['results','bodies']);
 let editReceipt:QueueEditReceipt|undefined;
 if(v.editReceipt!==undefined){
  const r=v.editReceipt;if(!record(r))bad();only(r,['operationId','sequence','disposition','acceptedRevision','replayed','bodyCompacted']);
  if(!id(r.operationId)||!counter(r.sequence)||BigInt(r.sequence)>BigInt(v.highestSeenSequence)||!['accepted','conflict'].includes(r.disposition as string)||typeof r.replayed!=='boolean'||typeof r.bodyCompacted!=='boolean'||r.bodyCompacted&&!r.replayed)bad();
  if(r.disposition==='accepted' ? !counter(r.acceptedRevision)||BigInt(r.acceptedRevision)>BigInt(v.revision) : r.acceptedRevision!==null)bad();
  editReceipt={operationId:r.operationId,sequence:r.sequence,disposition:r.disposition as QueueEditReceipt['disposition'],acceptedRevision:r.acceptedRevision as string|null,replayed:r.replayed,bodyCompacted:r.bodyCompacted};
 }
 const entries: QueueEntry[] = v.entries.map((e: unknown) => {
  if (!record(e) || !id(e.id) || typeof e.removed !== 'boolean' || typeof e.hidden !== 'boolean') bad();
  if (e.hidden) { only(e,['id','hidden','removed']); return { id:e.id, hidden:true, removed:e.removed }; }
  only(e,['id','hidden','removed','itemId','editionId','partId','sourceContext']);
  if (!id(e.itemId) || !optionalID(e.editionId) || !optionalID(e.partId) || !record(e.sourceContext)) bad();
  const source = e.sourceContext; only(source,['kind','id','revision','entryId']);
  if (!['item','playlist','album','artist','book','collection'].includes(source.kind as string) || !id(source.id) || source.revision !== null && !counter(source.revision) || !optionalID(source.entryId)) bad();
  return { id:e.id, hidden:false, removed:e.removed, itemId:e.itemId, editionId:e.editionId, partId:e.partId, sourceContext: { kind:source.kind as QueueSourceContext['kind'], id:source.id, revision:source.revision as string|null, entryId:source.entryId } };
 });
 if (new Set(entries.map(e => e.id)).size !== entries.length || entries.some(e => e.removed && e.id !== v.currentEntryId)) bad();
 if (v.currentEntryId !== null && !entries.some(e => e.id === v.currentEntryId) || (v.currentEntryId === null) !== (v.currentPlaybackId === null)) bad();
 return freeze({ id:v.id, scope, revision:v.revision, highestSeenSequence:v.highestSeenSequence, replayWindow:{results:256,bodies:32}, ...(editReceipt?{editReceipt}:{}), repeat:v.repeat as QueueSnapshot['repeat'], shuffled:v.shuffled, currentEntryId:v.currentEntryId, currentPlaybackId:v.currentPlaybackId, entries });
}

/** Validate and copy journal/wire intent before durable admission. */
export function parseQueueMutation(v:unknown):QueueMutation {
 if(!record(v)||!id(v.operationId)||!counter(v.sequence)||!counter(v.expectedRevision))bad();
 const common={operationId:v.operationId,sequence:v.sequence,expectedRevision:v.expectedRevision};
 const base=['operationId','sequence','expectedRevision','action'];
 switch(v.action){
  case 'fence':only(v,[...base,'cancelledSequence']);if(!counter(v.cancelledSequence)||BigInt(v.cancelledSequence)>=BigInt(v.sequence))bad();return freeze({...common,action:'fence',cancelledSequence:v.cancelledSequence});
  case 'append': case 'insert-next': only(v,[...base,'entries']);if(!Array.isArray(v.entries)||!v.entries.length||v.entries.length>1000)bad();return freeze({...common,action:v.action,entries:v.entries.map(parseQueueItem)});
  case 'add': case 'play-next': only(v,[...base,'entry']);return freeze({...common,action:v.action,entry:parseQueueItem(v.entry)});
  case 'remove': only(v,[...base,'entryId']);if(!id(v.entryId))bad();return freeze({...common,action:'remove',entryId:v.entryId});
  case 'reorder': only(v,[...base,'entryIds']);if(!Array.isArray(v.entryIds)||v.entryIds.length>1000||!v.entryIds.every(id)||new Set(v.entryIds).size!==v.entryIds.length)bad();return freeze({...common,action:'reorder',entryIds:[...v.entryIds]});
  case 'repeat': only(v,[...base,'repeat']);if(!['off','one','all'].includes(v.repeat as string))bad();return freeze({...common,action:'repeat',repeat:v.repeat as QueueSnapshot['repeat']});
  case 'shuffle': only(v,[...base,'shuffled']);if(typeof v.shuffled!=='boolean')bad();return freeze({...common,action:'shuffle',shuffled:v.shuffled});
  default:bad();
 }
}

export function parseQueueItem(v:unknown):QueueItemInput {
 if(!record(v))bad();only(v,['itemId','editionId','partId','sourceContext']);
 if(!id(v.itemId)||!optionalID(v.editionId)||!optionalID(v.partId)||!record(v.sourceContext))bad();
 const source=v.sourceContext;only(source,['kind','id','revision','entryId']);
 if(!['item','playlist','album','artist','book','collection'].includes(source.kind as string)||!id(source.id)||source.revision!==null&&!counter(source.revision)||!optionalID(source.entryId))bad();
 return freeze({itemId:v.itemId,editionId:v.editionId,partId:v.partId,sourceContext:{kind:source.kind as QueueSourceContext['kind'],id:source.id,revision:source.revision as string|null,entryId:source.entryId}});
}
export type QueueCreation=Readonly<{queueId:string}&({kind:'items';entries:readonly QueueItemInput[]}|{kind:'playlist';playlistId:string;expectedRevision:string})>;
export type QueueCreationIntent=QueueCreation extends infer C?C extends QueueCreation?Omit<C,'queueId'>:never:never;
export function parseQueueCreation(v:unknown):QueueCreation {
 if(!record(v)||!id(v.queueId))bad();
 if(v.kind==='items'){only(v,['queueId','kind','entries']);if(!Array.isArray(v.entries)||v.entries.length>1000)bad();return freeze({queueId:v.queueId,kind:'items',entries:v.entries.map(parseQueueItem)});}
 if(v.kind==='playlist'){only(v,['queueId','kind','playlistId','expectedRevision']);if(!id(v.playlistId)||!counter(v.expectedRevision))bad();return freeze({queueId:v.queueId,kind:'playlist',playlistId:v.playlistId,expectedRevision:v.expectedRevision});}
 bad();
}

export type QueuePlaylistCopy=Readonly<{operationId:string;expectedRevision:string;name:string;summary:string}>;
export type QueuePlaylistCopyReceipt=Readonly<{queueId:string;queueRevision:string;playlistId:string;playlistRevision:string;deleted:boolean}>;
export function parseQueuePlaylistCopy(v:unknown):QueuePlaylistCopy {
 if(!record(v))bad();only(v,['operationId','expectedRevision','name','summary']);
 if(!id(v.operationId)||!counter(v.expectedRevision)||typeof v.name!=='string'||typeof v.summary!=='string'||v.name.trim()!==v.name||!v.name||[...v.name].length>200||[...v.summary].length>4000||/[\0\r\n]/.test(v.name)||/\0/.test(v.summary)||new TextDecoder().decode(new TextEncoder().encode(v.name))!==v.name||new TextDecoder().decode(new TextEncoder().encode(v.summary))!==v.summary)bad();
 return freeze({operationId:v.operationId,expectedRevision:v.expectedRevision,name:v.name,summary:v.summary});
}
export function parseQueuePlaylistReceipt(v:unknown,queueId:string,expectedRevision:string):QueuePlaylistCopyReceipt {
 if(!record(v))bad();only(v,['queueId','queueRevision','playlistId','playlistRevision','deleted']);
 if(v.queueId!==queueId||v.queueRevision!==expectedRevision||!counter(v.queueRevision)||!id(v.playlistId)||!counter(v.playlistRevision)||typeof v.deleted!=='boolean')bad();
 return freeze({queueId,queueRevision:v.queueRevision,playlistId:v.playlistId,playlistRevision:v.playlistRevision,deleted:v.deleted});
}
/** An Up Next edit as a caller asks for it (the mutation without its fences). */
export type QueueIntent = QueueMutation extends infer M ? M extends QueueMutation ? Omit<M,'operationId'|'sequence'|'expectedRevision'> : never : never;
/** A queue workspace's state: the snapshot and what its last edit did. */
export type QueueState = Readonly<{queue: QueueSnapshot | null; phase: 'idle'|'loading'|'ready'|'sending'|'retry-required'|'conflict'|'fenced'|'expired'|'error'|'denied'; error: string|null}>;
