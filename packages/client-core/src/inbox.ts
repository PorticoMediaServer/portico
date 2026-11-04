import {routeBody} from "../../contracts/src/routes.ts";
import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {routePath} from "../../contracts/src/route-paths.ts";
import {randomId} from './random-id.ts';
import {installationIdentity} from './installation.ts';

const feedbackClientWord=/^[A-Za-z0-9][A-Za-z0-9 ._+-]{0,39}$/;
/** The viewer's notification inbox and the feedback dialog, as framework-free services shared
 * by the web and Apple clients.
 *
 * Both follow the forgiving-client rule: a failed request never takes what is already on
 * screen away. A refresh that fails keeps the loaded messages and records the error beside
 * them; only a first load with nothing to show is an error state. State changes are optimistic
 * and are put back if the server refuses them. */
import {
 mergeNotifications,parseFeedbackCapabilities,parseFeedbackSubmission,parseNotificationBatchResult,parseNotificationInbox,parseNotificationSummary,
 type FeedbackCapabilities,type FeedbackSubmissionResult,type Notification,type NotificationAudience,type NotificationCounts,type NotificationDelta,type NotificationInbox,
} from './notifications.ts';
import {serviceProblem,serviceText} from './presentation/service-text.ts';
import type {MessageId} from '../../i18n/src/index.ts';
import {serverNow} from './server-clock.ts';
import {dependsOn} from './freshness-hub.ts';
import {queryKeys} from './query/keys.ts';
import type {ViewerQueries} from './query/viewer-queries.ts';
import {until} from './query/until.ts';
import type {ViewerQueryOptions} from './query/react.ts';

export type InboxApi={request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>};
export type InboxView='unread'|'all'|'archived';
export type InboxAction='read'|'unread'|'archive'|'unarchive';
export type InboxServiceOptions={unreadTimeoutMs?:number;setTimer?:(fn:()=>void,ms:number)=>unknown;clearTimer?:(timer:unknown)=>void;
 /** The viewer's cache: first pages and unread counts are read through it, so every inbox of this viewer shares them and a view opened again shows at once. */
 queries?:ViewerQueries;
 /** What the cached documents are read with: a requester that lives as long as `queries` (the shell's checked one), never a screen's,
  * since a read one inbox starts is shared with the others. Without one, the cache's own requester. */
 cacheApi?:InboxApi;
 /** A cached read was answered by another server: this inbox's screen retires (the native screens' rule). */
 onScopeChanged?:(error:Error)=>void};
export type InboxSnapshot=Readonly<{
 phase:'idle'|'loading'|'ready'|'error';
 audience:NotificationAudience;
 /** Audiences this viewer may read; an owner also has 'account-admin'. */
 audiences:readonly NotificationAudience[];
 view:InboxView;
 items:readonly Notification[];
 counts:NotificationCounts;
 /** Unread across every audience the viewer has, for the badge. */
 unread:number;
 /** The same, per audience: desktop-search draws it beside the other audience ("Server 1"). */
 unreadBy:Readonly<Partial<Record<NotificationAudience,number>>>;
 revision:number;
 more:boolean;
 busy:boolean;
 error?:string;
}>;

/** The first page's size, and the most the server returns in one page. */
const PAGE=50,MAX_PAGE=100;
/** What every cached notification document moves with: the server's hint, or a change made on this device. */
const notificationsMoved=dependsOn.event('notification.changed');
const inboxPath=(audience:NotificationAudience,view:InboxView,limit:number)=>routePath("get_notifications_inbox") + "?audience=" + (encodeURIComponent(audience)) + "&state=" + (view) + "&limit=" + limit;
const noCounts:NotificationCounts=Object.freeze({unread:0,read:0,archived:0,total:0});
const data=(raw:unknown):unknown=>raw&&typeof raw==='object'&&'data' in (raw as Record<string,unknown>)?(raw as Record<string,unknown>).data:raw;
/**
 * A cached notification document is shared by every inbox of the viewer, so it is read with a
 * requester that lives as long as the cache (`api`: the shell's, or the cache's own), never one
 * screen's: closing the screen that started the read must not end it for the others. The checks a
 * screen's requester made travel with the read: the answer is this cache's server's, for the audience asked for.
 */
function cachedNotifications<T>(queries:ViewerQueries,api:InboxApi,path:string,audience:NotificationAudience,parse:(value:unknown)=>T&{audience:string},signal:AbortSignal):Promise<T>{
 return api.request<unknown>(path,'GET',undefined,signal).then(raw=>{
  const envelope=raw&&typeof raw==='object'&&!Array.isArray(raw)?raw as {scope?:unknown;data?:unknown}:undefined;
  const scope=envelope?.scope&&typeof envelope.scope==='object'&&!Array.isArray(envelope.scope)?envelope.scope as {serverId?:unknown;viewerFence?:unknown}:undefined;
  // Every notification document comes in the server's envelope. Its fence's shape is checked, never
  // compared with another read's (CLIENT-DATA-REVIEW §4.4); another server's answer ends the screens waiting on it.
  const fenceText=scope?.viewerFence;
  if(!envelope||!('data' in envelope)||!scope||typeof scope.serverId!=='string'||!isText(fenceText))throw notificationInvalid();
  if(scope.serverId!==queries.scope.serverId)throw notificationScopeChanged();
  if(!/^[a-f0-9]{64}$/.test(fenceText))throw notificationInvalid();
  const value=parse(envelope.data);
  if(value.audience!==audience)throw notificationInvalid();
  return value;
 });
}
const isText=(value:unknown):value is string=>typeof value==='string';
const notificationInvalid=()=>Object.assign(new Error('invalid_notifications_response'),{code:'invalid_response'});
/** Another server answered for this viewer's notifications: every screen waiting on the read retires (`onScopeChanged`). */
const notificationScopeChanged=()=>Object.assign(new Error('notifications_scope_changed'),{code:'access_refused',scopeChanged:true});
const isScopeChanged=(error:unknown)=>!!error&&typeof error==='object'&&(error as {scopeChanged?:unknown}).scopeChanged===true;
/** A notification view's first page in the viewer's cache. */
export function inboxFirstPageQuery(queries:ViewerQueries,audience:NotificationAudience,view:InboxView,api:InboxApi=queries.api):ViewerQueryOptions<NotificationInbox>{
 return {queryKey:queryKeys.notificationsInbox(audience,view),
  queryFn:({signal})=>cachedNotifications(queries,api,inboxPath(audience,view,PAGE),audience,parseNotificationInbox,signal),
  meta:{dependencies:[notificationsMoved]}};
}
/** The viewer's unread count in one audience, in the viewer's cache. */
export function unreadCountQuery(queries:ViewerQueries,audience:NotificationAudience,api:InboxApi=queries.api):ViewerQueryOptions<number>{
 return {queryKey:queryKeys.notificationsUnread(audience),
  queryFn:async({signal})=>(await cachedNotifications(queries,api,routePath("get_notifications_unread_count") + "?audience=" + (encodeURIComponent(audience)),audience,parseNotificationSummary,signal)).counts.unread,
  meta:{dependencies:[notificationsMoved]}};
}
// A transport failure ("Failed to fetch") is not something to show a person; the server's own
// words are.
// X-04: what the viewer reads comes from the catalogue, never from `error.message`.
const message=(e:unknown,fallback:MessageId,context:'notifications'|'feedback'='notifications',operation:'load'|'save'|'action'='load')=>serviceProblem(e,context,fallback,operation);
// A refusal the server stated is definite: it did not act, so nothing is waiting
// on it. A 5xx, a timeout or a transport failure leaves the outcome unknown and
// the write keeps its exact intent.
const definiteRefusal=(e:unknown):boolean=>{
 const status=(e as {status?:unknown}|undefined)?.status;
 return typeof status==='number'&&[400,404,409,410,412,428,429].includes(status);
};
const conflict=(e:unknown):boolean=>(e as {status?:unknown}|undefined)?.status===409;
// One write is one immutable intent: the audience and revision the page on
// screen was read at, the exact operations, and the operation id the server's
// receipt is stored under. Each dispatch of that intent carries its own token as
// well, so a late answer from an older dispatch cannot settle a newer one.
type InboxOperation=Readonly<{action:string;ids?:readonly string[]}>;
type InboxIntent=Readonly<{operationId:string;expectedRevision:number;audience:NotificationAudience;operations:readonly InboxOperation[]}>;
// The one page this service actually read and still shows. Its token is the
// page identity a write is aimed at; a cursor string is not one, because the
// same or an empty cursor can recur across reads.
type InboxPage=Readonly<{token:number;revision:number;audience:NotificationAudience;view:InboxView}>;
type InboxFlight=Readonly<{token:number;intent:InboxIntent;controller:AbortController;pageToken:number}>;
type InboxMore=Readonly<{pageToken:number;generation:number;audience:NotificationAudience;view:InboxView;cursor:string;controller:AbortController}>;

export class InboxService{
 private api:InboxApi;private operationId:()=>string;
 private state:InboxSnapshot=Object.freeze<InboxSnapshot>({phase:'idle',audience:'profile',audiences:['profile'],view:'unread',items:[],counts:noCounts,unread:0,unreadBy:{},revision:0,more:false,busy:false});
 private listeners=new Set<()=>void>();private cursor='';private generation=0;private controller?:AbortController;private disposed=false;
 private unreadFlight?:Promise<number>;private unreadController?:AbortController;private unreadDirty=false;private unreadRoundFresh=false;private options:InboxServiceOptions;
 // The one write this view owns. 'inflight' means its answer is still out and no
 // other change is dispatched; 'unresolved' means the attempt ended without a
 // definite answer and only the same change may resume it.
 private flight?:Readonly<{flight:InboxFlight;phase:'inflight'|'unresolved'}>;
 private writeToken=0;
 // The admitted page: installed only by a successful first-page adoption and
 // retired synchronously by the next read or by dispose. A write needs one.
 private page?:InboxPage;
 private pageToken=0;
 private reading=false;
 private moreFlight?:InboxMore;
 // The newest revision a `notification.changed` hint named that this view has not read yet.
 private pending?:number;
 constructor(api:InboxApi,operationId:()=>string=()=>randomId(),options:InboxServiceOptions={}){this.api=api;this.operationId=operationId;this.options=options;}
 getSnapshot=()=>this.state;
 subscribe=(fn:()=>void)=>{this.listeners.add(fn);return()=>{this.listeners.delete(fn);};};
 private publish(patch:Partial<InboxSnapshot>){if(this.disposed)return;this.state=Object.freeze({...this.state,...patch});this.listeners.forEach(f=>f());}
 dispose(){this.disposed=true;this.generation++;this.controller?.abort();this.unreadController?.abort();this.retireFlight();this.retireMore();this.retirePage();this.listeners.clear();}

 /** Loads the first page of a view. What is on screen stays until the new page arrives. */
 async load(audience:NotificationAudience=this.state.audience,view:InboxView=this.state.view):Promise<void>{return this.read(audience,view,PAGE,false);}
 /**
  * A `notification.changed` hint (OW-5): quietly reread the view on screen, keeping its rows
  * until the new page arrives. A revision this page already holds (the echo of its own write)
  * reads nothing. While a read, a continuation or a write is out, one reread follows it rather
  * than cancelling it. A view that has paged on rereads as many rows as it shows, up to the
  * server's page limit. `revision` is the event's own (a decimal string); without one, or on a
  * stream resync, the view is reread.
  */
 refresh(revision?:string):void{
  if(this.disposed||this.state.phase==='idle')return;
  const named=revision!==undefined&&/^[0-9]{1,15}$/.test(revision)?Number(revision):undefined;
  const wanted=named??Infinity;
  if(wanted<=this.state.revision)return;
  this.pending=Math.max(this.pending??0,wanted);
  this.settle();
 }
 /** Runs the reread a hint asked for once nothing else is out. */
 private settle(){
  if(this.disposed||this.pending===undefined||this.reading||this.moreFlight||this.flight?.phase==='inflight')return;
  const wanted=this.pending;this.pending=undefined;
  if(wanted<=this.state.revision)return;
  void this.read(this.state.audience,this.state.view,Math.min(MAX_PAGE,Math.max(PAGE,this.state.items.length)),true);
 }
 /** The cached first page of a view, when nothing it was built from has moved since it was read. */
 private cached(audience:NotificationAudience,view:InboxView):NotificationInbox|undefined{
  const queries=this.options.queries;
  if(!queries)return undefined;
  const key=queryKeys.notificationsInbox(audience,view),state=queries.client.getQueryState<NotificationInbox>(key);
  return state&&state.data!==undefined&&!state.isInvalidated&&queries.documentFor(key)?.getState().stale!==true?state.data:undefined;
 }
 /** A first page: from the viewer's cache when there is one (current unless `fresh`), else from the server. */
 private scoped<T>(read:Promise<T>):Promise<T>{return read.catch(error=>{if(isScopeChanged(error))this.options.onScopeChanged?.(error as Error);throw error;});}
 private async firstPage(audience:NotificationAudience,view:InboxView,limit:number,signal:AbortSignal,fresh:boolean):Promise<NotificationInbox>{
  const queries=this.options.queries;
  if(queries&&limit===PAGE){const options=inboxFirstPageQuery(queries,audience,view,this.options.cacheApi);return this.scoped(until(queries.client.fetchQuery(fresh?{...options,staleTime:0}:options),signal));}
  return parseNotificationInbox(data(await this.api.request<RouteResponse<"get_notifications_inbox">>(inboxPath(audience,view,limit),'GET',undefined,signal)));
 }
 /** `fresh`: something moved that the cached page may not include (a hint, a resync, a conflict). */
 private async read(audience:NotificationAudience,view:InboxView,limit:number,fresh:boolean):Promise<void>{
  if(this.disposed)return;
  // A new page retires the write and the admitted page identity at once. The
  // rows stay on screen, but nothing is admitted to write against until this
  // read succeeds and installs its own page.
  this.retireFlight();this.retireMore();this.retirePage();this.reading=true;
  const generation=++this.generation;this.controller?.abort();const controller=this.controller=new AbortController();
  const same=audience===this.state.audience&&view===this.state.view;
  // A view the cache holds and nothing has moved since: shown at once, with no request and no loading state.
  const held=fresh||limit!==PAGE?undefined:this.cached(audience,view);
  if(held){this.reading=false;this.adopt(held,audience,view);void this.refreshUnread(false);this.settle();return;}
  this.publish({phase:this.state.items.length&&same?'ready':'loading',audience,view,busy:false,error:undefined,...(same?{}:{items:[],more:false})});
  try{
   if(!this.readCurrent(generation,controller))return;
   const inbox=await this.firstPage(audience,view,limit,controller.signal,fresh);
   if(!this.readCurrent(generation,controller))return;
   this.adopt(inbox,audience,view);
   // A page from the cache goes with the count cached beside it; a fresh page with a fresh count.
   void this.refreshUnread(fresh);
  }catch(e){
   if(generation!==this.generation||controller.signal.aborted)return;
   this.publish({phase:this.state.items.length?'ready':'error',error:message(e,'notifications.error.load')});
  }finally{if(generation===this.generation){this.reading=false;this.settle();}}
 }
 async more():Promise<void>{
  if(this.disposed||this.reading||this.flight||this.moreFlight||!this.page||!this.cursor)return;
  const page=this.page;
  const flight:InboxMore=Object.freeze({pageToken:page.token,generation:this.generation,audience:page.audience,view:page.view,cursor:this.cursor,controller:new AbortController()});
  this.moreFlight=flight;
  this.publish({busy:true});
  try{
   // Publishing can synchronously retire this page. Never reacquire its cursor
   // or dispatch a retained continuation against the replacement view.
   if(!this.moreCurrent(flight))return;
   const {audience,view,cursor,controller}=flight;
   const inbox=parseNotificationInbox(data(await this.api.request<RouteResponse<"get_notifications_inbox">>(routePath("get_notifications_inbox") + "?audience=" + (encodeURIComponent(audience)) + "&state=" + (view) + "&limit=" + PAGE + "&cursor=" + (encodeURIComponent(cursor)),'GET',undefined,controller.signal)));
   if(!this.moreCurrent(flight))return;
   this.moreFlight=undefined;
   this.cursor=inbox.nextCursor;
   this.admit(inbox.revision,audience,view);
   const held=new Set(this.state.items.map(n=>n.id));
   this.publish({items:[...this.state.items,...inbox.items.filter(n=>!held.has(n.id))],counts:inbox.counts,revision:inbox.revision,more:!!inbox.nextCursor,busy:false,error:undefined});
  }catch(e){
   if(!this.moreCurrent(flight))return;
   this.moreFlight=undefined;
   this.publish({busy:false,error:message(e,'notifications.error.more')});
  }finally{
   if(this.moreFlight===flight)this.moreFlight=undefined;
   flight.controller.abort();
   this.settle();
  }
 }
 /** Applies a change pushed by the event stream or long poll. */
 accept(delta:NotificationDelta){
  if(delta.resync){void this.read(this.state.audience,this.state.view,PAGE,true);return;}
  if(delta.revision<=this.state.revision)return;
  // While a first page is being read, its answer already carries this change;
  // merging a delta into retained rows would admit a partial page.
  if(this.reading)return;
  // The revision moved, so any write aimed at the previous page must not publish
  // its late counts over this one.
  if(this.page)this.admit(delta.revision,this.state.audience,this.state.view);
  this.publish({items:this.visible(mergeNotifications(this.state.items,delta)),counts:delta.counts,revision:delta.revision,busy:false});
  this.unreadDirty=true;void this.refreshUnread();
 }
 private visible(items:readonly Notification[]):Notification[]{
  const view=this.state.view;
  return items.filter(n=>view==='archived'?n.archived:view==='unread'?!n.archived&&!n.read:!n.archived);
 }
 /** Optimistic: the row changes at once and is put back if the server refuses. */
 async apply(action:InboxAction,ids:readonly string[]):Promise<void>{
  if(this.disposed||!ids.length)return;
  // The write flight is claimed before the rows change, synchronously, so a
  // listener woken by the optimistic patch cannot dispatch a second write.
  const flight=this.begin([{action,ids:[...ids]}]);
  if(!flight)return;
  const before=this.state,chosen=new Set(flight.intent.operations[0].ids),now=serverNow();
  const next=before.items.map(n=>!chosen.has(n.id)?n:action==='read'?{...n,read:true,readAt:n.readAt??now}:action==='unread'?{...n,read:false,readAt:null}:action==='archive'?{...n,archived:true,archivedAt:now}:{...n,archived:false,archivedAt:null});
  this.publish({items:this.visible(next),error:undefined});
  await this.dispatch(flight,before);
  this.settle();
 }
 async readAll():Promise<void>{
  if(this.disposed)return;
  const flight=this.begin([{action:'read-all'}]);
  if(!flight)return;
  const before=this.state,now=serverNow();
  this.publish({items:this.visible(before.items.map(n=>n.read?n:{...n,read:true,readAt:now})),error:undefined});
  await this.dispatch(flight,before);
  this.settle();
 }
 /**
  * Claims the view's one write flight. Undefined means nothing is sent: no page
  * is admitted, a first page is being read, the service is closed, a dispatch is
  * still out, or an unanswered attempt holds the document and this is a
  * different change. Only the same change may resume that exact attempt.
  */
 private begin(operations:{action:string;ids?:string[]}[]):InboxFlight|undefined{
  if(this.disposed||this.reading||this.moreFlight)return undefined;
  const page=this.page;
  // A write is aimed at a page this service read and still shows. Before the
  // first successful load there is no page, and the server's revision 0 means
  // "no records" and is the one value its CAS treats as unfenced; neither may
  // carry a change the owner never saw.
  if(!page||page.revision<1)return undefined;
  const held=this.flight;
  // A dispatch still owns the next change; its own answer settles the rows it
  // already drew, so repeats send nothing, whatever they would have changed.
  if(held?.phase==='inflight')return undefined;
  let intent:InboxIntent;
  if(held){
   // The unanswered attempt keeps its exact audience, revision, operations and
   // operation id. Only that same change may resume it; a different action is
   // refused rather than replacing what the server may already have applied.
   if(JSON.stringify(held.flight.intent.operations)!==JSON.stringify(operations)){
    this.publish({error:message(new Error('an inbox change is unresolved'),'notifications.error.change','notifications','save')});
    return undefined;
   }
   intent=held.flight.intent;
  }else{
   intent=Object.freeze({operationId:this.operationId(),expectedRevision:page.revision,audience:page.audience,
    operations:Object.freeze(operations.map(operation=>Object.freeze(operation.ids?{action:operation.action,ids:Object.freeze([...operation.ids])}:{action:operation.action})))});
  }
  // The operation-id supplier is caller code and may synchronously load or
  // dispose. It cannot lend a replacement page to this original intent.
  if(this.disposed||this.reading||this.moreFlight||this.page!==page||this.flight!==held)return undefined;
  return this.launch(intent,page);
 }
 private launch(intent:InboxIntent,page:InboxPage):InboxFlight{
  const controller=new AbortController();
  const flight=Object.freeze({token:++this.writeToken,intent,controller,pageToken:page.token});
  this.flight=Object.freeze({flight,phase:'inflight'});
  return flight;
 }
 /** True while this exact dispatch is still the view's one write. An older
  * dispatch's late answer may settle nothing, not the rows nor a successor. */
 private owns(token:number):boolean{return this.flight?.flight.token===token;}
 /** The exact admitted page this dispatch was aimed at is still the page on
  * screen. A cursor is not an identity: the same or an empty cursor recurs. */
 private current(flight:InboxFlight):boolean{return !this.disposed&&this.page?.token===flight.pageToken;}
 private retireFlight(){
  const held=this.flight;
  this.flight=undefined;
  held?.flight.controller.abort();
 }
 private readCurrent(generation:number,controller:AbortController):boolean{return !this.disposed&&this.generation===generation&&this.controller===controller&&!controller.signal.aborted;}
 private moreCurrent(flight:InboxMore):boolean{return !this.disposed&&!this.reading&&this.moreFlight===flight&&this.page?.token===flight.pageToken&&this.generation===flight.generation&&!flight.controller.signal.aborted;}
 private retireMore(){const held=this.moreFlight;this.moreFlight=undefined;held?.controller.abort();}
 private retirePage(){this.page=undefined;}
 private admit(revision:number,audience:NotificationAudience,view:InboxView){
  // A new page identity retires the exact write aimed at the old one: an
  // obsolete intent must never survive to block or be reused against this page.
  this.retireFlight();this.retireMore();
  this.page=Object.freeze({token:++this.pageToken,revision,audience,view});
 }
 private async dispatch(flight:InboxFlight,before:InboxSnapshot):Promise<void>{
  const {token,intent}=flight;
  // The wire body is derived from the immutable intent alone: no later
  // revision, audience or operations can be substituted into this dispatch.
  const body={operationId:intent.operationId,expectedRevision:intent.expectedRevision,audience:intent.audience,
   operations:intent.operations.map(operation=>({action:operation.action,...(operation.ids?{ids:[...operation.ids]}:{})}))};
  // A listener woken by the optimistic rows may synchronously load, reread or
  // dispose this service. Those retire the flight and its signal, so nothing is
  // sent for a page that is no longer admitted.
  if(!this.owns(token)||!this.current(flight)||flight.controller.signal.aborted)return;
  try{
   const result=parseNotificationBatchResult(data(await this.api.request<RouteResponse<"post_notifications_inbox_actions">>(routePath("post_notifications_inbox_actions"),'POST',routeBody("post_notifications_inbox_actions", body),flight.controller.signal)));
   if(!this.owns(token))return;
   this.flight=undefined;
   if(!this.current(flight))return;
   // The confirmed change is now part of this page: a later write fences at the
   // revision the server just returned, never at the one it replaced.
   this.admit(result.revision,intent.audience,this.state.view);
   // Every cached notification document of this viewer (another view, another inbox, the badge) is read again when next asked for.
   this.options.queries?.moved([notificationsMoved]);
   this.publish({counts:result.counts,revision:result.revision});this.unreadDirty=true;void this.refreshUnread();
  }catch(e){
   if(!this.owns(token))return;
   // The page this attempt was aimed at is gone. Release this claim and stop:
   // a stale failure may not mark itself unresolved, take over, or write its
   // error into the page that is on screen now.
   if(!this.current(flight)){this.flight=undefined;return;}
   // A stated refusal is finished. An unanswered attempt stays unresolved with
   // its original intent until the same change explicitly resumes it.
   if(definiteRefusal(e))this.flight=undefined;
   else this.flight=Object.freeze({flight,phase:'unresolved'});
   const notice=message(e,'notifications.error.change','notifications','save');
   // Claim the conflict read before publishing rollback/error. A listener may
   // replace this view from that publish; it must retire this exact read rather
   // than letting the stale catch reacquire and clear the successor.
   if(conflict(e))await this.reread(notice,before,intent.audience,before.view);
   else this.publish({items:before.items,counts:before.counts,error:notice});
  }
 }
 /** One fresh first page after a conflict, with the conflict copy kept visible. */
 private async reread(notice:string,before:InboxSnapshot,audience:NotificationAudience,view:InboxView):Promise<void>{
  this.retireFlight();this.retireMore();this.retirePage();this.reading=true;
  const generation=++this.generation;this.controller?.abort();const controller=this.controller=new AbortController();
  this.publish({items:before.items,counts:before.counts,error:notice,busy:false});
  try{
   if(!this.readCurrent(generation,controller))return;
   const inbox=await this.firstPage(audience,view,PAGE,controller.signal,true);
   if(!this.readCurrent(generation,controller))return;
   this.adopt(inbox,audience,view,notice);
   void this.refreshUnread();
  }catch{/* the rows and the conflict copy stay as they are */}
  finally{if(generation===this.generation){this.reading=false;this.settle();}}
 }
 private adopt(inbox:NotificationInbox,audience:NotificationAudience,view:InboxView,notice?:string){
  this.admit(inbox.revision,audience,view);
  this.cursor=inbox.nextCursor;
  const audiences=(inbox.audiences.filter(a=>a==='profile'||a==='account-admin') as NotificationAudience[]);
  this.publish({phase:'ready',busy:false,items:inbox.items,counts:inbox.counts,revision:inbox.revision,more:!!inbox.nextCursor,audiences:audiences.length?audiences:['profile'],error:notice});
 }
 /** The badge. Failure leaves the last known number in place. `fresh: false` takes the viewer's cached count when nothing moved since it was read. */
 refreshUnread(fresh=true):Promise<number>{
  if(this.disposed)return Promise.resolve(this.state.unread);
  if(this.unreadFlight){
   // A round that may answer from the cache cannot stand in for a fresh one: one more round follows it.
   if(fresh&&!this.unreadRoundFresh)this.unreadDirty=true;
   return this.unreadFlight;
  }
  this.unreadFlight=this.readUnread(fresh).finally(()=>{this.unreadFlight=undefined;});
  return this.unreadFlight;
 }
 private async readUnread(fresh:boolean):Promise<number>{
  // Only the first round may answer from the cache; a round repeated because something moved reads the server.
  let round=fresh;
  do{
   this.unreadDirty=false;this.unreadRoundFresh=round;
   const audiences=[...this.state.audiences],revision=this.state.revision;
   const controller=this.unreadController=new AbortController();
   const setTimer=this.options.setTimer??((fn:()=>void,ms:number)=>setTimeout(fn,ms));
   const clearTimer=this.options.clearTimer??((timer:unknown)=>clearTimeout(timer as ReturnType<typeof setTimeout>));
   // This is a small badge read, not the event long poll or a playback request. Bound only
   // this refresh so an unresponsive transport cannot park every subsequent badge update.
   const timer=setTimer(()=>controller.abort(),this.options.unreadTimeoutMs??10_000);
   let abort:()=>void=()=>{};
   const cancelled=new Promise<never>((_,reject)=>{abort=()=>reject(new Error('unread_refresh_aborted'));controller.signal.addEventListener('abort',abort,{once:true});});
   try{
    let total=0;const by:Partial<Record<NotificationAudience,number>>={};
    for(const audience of audiences){
     const count=await Promise.race([this.unreadCount(audience,controller.signal,round),cancelled]);
     if(controller.signal.aborted||this.disposed)return this.state.unread;
     by[audience]=count;total+=count;
    }
    // An inbox load can discover the owner audience while the profile read is pending.
    // Never publish a partial/obsolete badge; coalesce changes into one fresh round.
    if(audiences.join(',')!==this.state.audiences.join(',')||revision!==this.state.revision)this.unreadDirty=true;
    const unreadBy=Object.freeze(by);
    if(!this.unreadDirty&&(total!==this.state.unread||JSON.stringify(unreadBy)!==JSON.stringify(this.state.unreadBy)))this.publish({unread:total,unreadBy});
   }catch{
    if(controller.signal.aborted||this.disposed)return this.state.unread;
    if(audiences.join(',')!==this.state.audiences.join(',')||revision!==this.state.revision)this.unreadDirty=true;
    if(!this.unreadDirty)return this.state.unread;
   }
   finally{clearTimer(timer);controller.signal.removeEventListener('abort',abort);if(this.unreadController===controller)this.unreadController=undefined;}
   round=true;
  }while(this.unreadDirty&&!this.disposed);
  return this.state.unread;
 }
 private async unreadCount(audience:NotificationAudience,signal:AbortSignal,fresh:boolean):Promise<number>{
  const queries=this.options.queries;
  if(queries){const options=unreadCountQuery(queries,audience,this.options.cacheApi);return this.scoped(until(queries.client.fetchQuery(fresh?{...options,staleTime:0}:options),signal));}
  return parseNotificationSummary(data(await this.api.request<RouteResponse<"get_notifications_unread_count">>(routePath("get_notifications_unread_count") + "?audience=" + (encodeURIComponent(audience)),'GET',undefined,signal))).counts.unread;
 }

}

export type FeedbackDraft={kind:string;category:string;message:string;itemId?:string;playbackSessionId?:string;attachDiagnostics:boolean};
export type FeedbackSnapshot=Readonly<{
 phase:'loading'|'ready'|'unavailable'|'sending'|'sent';
 capabilities?:FeedbackCapabilities;
 result?:FeedbackSubmissionResult;
 error?:string;
}>;

/** One feedback dialog's lifetime. The message a person typed is never lost to a failed send:
 * the dialog returns to 'ready' with the error shown and the draft intact (the caller owns it). */
export class FeedbackService{
 private api:InboxApi;private operationId:()=>string;private online?:()=>boolean|undefined;private attempt='';private attemptClient?:ReturnType<FeedbackService['client']>;
 private state:FeedbackSnapshot=Object.freeze({phase:'loading'});private listeners=new Set<()=>void>();
 /** `online` reports whether the device itself is online, when the platform can tell. */
 constructor(api:InboxApi,operationId:()=>string=()=>randomId(),online?:()=>boolean|undefined){this.api=api;this.operationId=operationId;this.online=online;}
 /** The sending app as triage sees it: its platform, version and online state. */
 private client(){
  const identity=installationIdentity(),online=this.online?.();
  if(!identity||!feedbackClientWord.test(identity.platform)||!feedbackClientWord.test(identity.appVersion))return undefined;
  return {platform:identity.platform,appVersion:identity.appVersion,...(typeof online==='boolean'?{online}:{})};
 }
 getSnapshot=()=>this.state;
 subscribe=(fn:()=>void)=>{this.listeners.add(fn);return()=>{this.listeners.delete(fn);};};
 private publish(next:FeedbackSnapshot){this.state=Object.freeze(next);this.listeners.forEach(f=>f());}
 async open():Promise<void>{
  this.publish({phase:'loading'});
  try{
   const capabilities=parseFeedbackCapabilities(data(await this.api.request<RouteResponse<"get_feedback_capabilities">>(routePath("get_feedback_capabilities"))));
   this.publish(capabilities.canSubmit?{phase:'ready',capabilities}:{phase:'unavailable',capabilities,error:serviceText('feedback.off')});
  }catch(e){this.publish({phase:'unavailable',error:message(e,'feedback.unavailable','feedback')});}
 }
 /** Why a draft cannot be sent yet, or '' when it can. */
 problem(draft:FeedbackDraft):string{
  const c=this.state.capabilities;if(!c)return serviceText('feedback.unavailable');
  const kind=c.kinds.find(k=>k.id===draft.kind);if(!kind)return serviceText('feedback.chooseKind');
  if(!kind.categories.some(x=>x.id===draft.category))return serviceText('feedback.chooseCategory');
  // CD-46: the capability's min/max count Unicode code points (as users count), not UTF-16
  // units or bytes. The trimmed text measured here is exactly what submit() sends.
  const length=Array.from(draft.message.trim()).length;
  if(length<c.minMessageLength)return serviceText('feedback.tooShort',{count:c.minMessageLength});
  if(length>c.maxMessageLength)return serviceText('feedback.tooLong',{count:c.maxMessageLength});
  return '';
 }
 async submit(draft:FeedbackDraft):Promise<boolean>{
  const capabilities=this.state.capabilities;if(!capabilities||this.state.phase==='sending'||this.problem(draft))return false;
  // One operation id per draft: a retry after a lost response is the same report, not a second.
  // The sending app's facts are captured with it, so the retry is the same request.
  if(!this.attempt){this.attempt=this.operationId();this.attemptClient=this.client();}
  this.publish({phase:'sending',capabilities});
  try{
   const category=capabilities.kinds.find(k=>k.id===draft.kind)?.categories.find(c=>c.id===draft.category),client=this.attemptClient;
   const result=parseFeedbackSubmission(data(await this.api.request<RouteResponse<"post_feedback_reports">>(routePath("post_feedback_reports"),'POST',routeBody("post_feedback_reports", {
    operationId:this.attempt,kind:draft.kind,category:draft.category,message:draft.message.trim(),
    itemId:category?.wantsItem?draft.itemId??'':'',playbackSessionId:category?.wantsPlaybackSession?draft.playbackSessionId??'':'',
    attachDiagnostics:capabilities.diagnosticsSupported&&draft.attachDiagnostics,
    ...(client?{client}:{}),
   }))));
   this.attempt='';this.attemptClient=undefined;this.publish({phase:'sent',capabilities,result});return true;
  }catch(e){this.publish({phase:'ready',capabilities,error:message(e,'feedback.sendFailed','feedback','save')});return false;}
 }
}
