import {unreadableServerResponse} from './server-messages.ts';
/** Wire types and parsers for social playback (Watch Together groups) and remote
 * playback (Portico receiver handoff, Google Cast pairing).
 *
 * Every parser is total: it either returns a frozen value whose invariants the
 * rest of the client can rely on, or it throws. Nothing here reaches the network,
 * holds state, or touches a renderer. See `server/api/social.md`.
 */

export const SOCIAL_PROTOCOL='1.0';

export type SocialCounter=string;
export type GroupHostAuthority='host-only'|'anyone';
export type GroupState='creating'|'lobby'|'preparing'|'ready'|'waiting'|'playing'|'paused'|'host-reconnecting-playing'|'host-reconnecting-paused'|'degraded'|'ending'|'ended'|'failed';
export type GroupTimelineState='idle'|'playing'|'paused'|'stopped';
export type GroupReadiness='buffering'|'ready'|'lagging';
export type GroupMemberPresence='connected'|'away'|'disconnected';
export type GroupHostPresenceState='connected'|'reconnecting'|'absent';
export type GroupRepeatMode='none'|'one'|'all';
export type GroupTransportCommand='play'|'pause'|'stop'|'seek'|'next'|'previous'|'load'|'set-queue-position';

export type SocialRate=Readonly<{numerator:SocialCounter;denominator:SocialCounter}>;
export type GroupTimeline=Readonly<{itemId:string;currentEntryId:string;state:GroupTimelineState;anchorPositionUs:SocialCounter;anchorAt:string;rate:SocialRate;queuePosition:number;waitUntil:string|null}>;
export type GroupSync=Readonly<{noCorrectionUnderMs:number;rateCorrectionMinimum:string;rateCorrectionMaximum:string;rateCorrectionMaxMs:number;seekAtOrOverMs:number}>;
export type GroupSettings=Readonly<{shuffleEnabled:boolean;repeatMode:GroupRepeatMode}>;
export type GroupPermissions=Readonly<{isHost:boolean;canControl:boolean;canManageQueue:boolean;/** Optional on the wire; absent means the server does not allow a self-claim. */canClaimHost?:boolean}>;
/** The host's device and the v1 session it plays (B8a): `bound` while that device plays
 * `playbackId`, `fresh` when it plays nothing or no host device is bound yet (`deviceId` ''),
 * `retired` once the device is signed out. */
export type GroupAuthority=Readonly<{deviceId:string;playbackId:string|null;state:'fresh'|'bound'|'retired'}>;
export type GroupHostPresence=Readonly<{presence:GroupHostPresenceState;lastSeenAt:string;pauseAt:string|null;endAt:string|null}>;
export type GroupMember=Readonly<{id:string;displayName:string;/** Public members only; a private member never carries one. */username?:string;private:boolean;role:'host'|'member';state:'invited'|'joined'|'left'|'removed';readiness:GroupReadiness;positionUs:SocialCounter;reportedAt:string|null;presence:GroupMemberPresence;joinedAt:string}>;
export type GroupReadinessSummary=Readonly<{aggregate:GroupReadiness;ready:number;buffering:number;lagging:number;stale:number;memberCount:number}>;
export type GroupQueueEntry=Readonly<{entryId:string;position:number;itemId:string|null;unavailable:boolean;addedBy:string}>;
export type GroupQueueEligibility=Readonly<{blockedEntries:number;currentEntryBlocked:boolean;members:readonly Readonly<{memberId:string;displayName:string;blockedEntryIds:readonly string[]}>[]}>;
export type GroupQueue=Readonly<{revision:SocialCounter;position:number;entries:readonly GroupQueueEntry[];eligibility:GroupQueueEligibility|null}>;
export type Group=Readonly<{
 id:string;name:string;state:GroupState;hostAuthority:GroupHostAuthority;hostMemberId:string;
 revision:SocialCounter;playbackRevision:SocialCounter;settingsRevision:SocialCounter;governanceRevision:SocialCounter;queueRevision:SocialCounter;reconnectGeneration:SocialCounter;
 lastCommand:string;lastCommandId:string;endedReason:string;eventOrdinal:SocialCounter;createdAt:string;updatedAt:string;
 permissions:GroupPermissions;authority:GroupAuthority;host:GroupHostPresence;timeline:GroupTimeline;
 settings:GroupSettings;sync:GroupSync;readiness:GroupReadinessSummary;members:readonly GroupMember[];
 queue:GroupQueue|null;viewerMemberId:string}>;
export type GroupSnapshot=Readonly<{protocolVersion:string;serverTime:string;group:Group}>;
export type GroupTransportReceipt=Readonly<{protocolVersion:string;groupId:string;idempotencyKey:string;disposition:'accepted'|'duplicate';command:GroupTransportCommand;groupState:GroupState;revision:SocialCounter;playbackRevision:SocialCounter;queueRevision:SocialCounter;recordedAt:string;timeline:GroupTimeline;settings:GroupSettings;override:Readonly<{reason:string;blockedMemberIds:readonly string[]}>|null}>;
export type GroupInvite=Readonly<{id:string;groupId:string;code:string;expiresAt:string;maxUses:number;uses:number;recipientProfileId:string}>;

export type HandoffState='prepared'|'committing'|'committed'|'rolled_back'|'expired'|'failed';
export type HandoffOutcome='waiting'|'pending'|'accepted'|'rejected';
export type Handoff=Readonly<{id:string;receiverId:string;grantId:string;requestId:string;state:HandoffState;outcome:HandoffOutcome;reason:string;revision:SocialCounter;itemId:string;sourcePlaybackId:string;receiverPlaybackId:string;requestedPositionUs:SocialCounter;readyPositionUs:SocialCounter;committedPositionUs:SocialCounter;sourceRetired:boolean;createdAt:string;expiresAt:string;settledAt:string|null}>;
export type ReceiverKind='portico'|'cast';
export type ReceiverGrantPolicy='per-device'|'always-ask'|'open';
export type Receiver=Readonly<{id:string;kind:ReceiverKind;deviceId:string;displayName:string;platform:string;keyFingerprint:string;supportedCommands:readonly string[];grantPolicy:ReceiverGrantPolicy;authorizationRevision:SocialCounter;state:'active'|'retired';presence:'online'|'offline';lastSeenAt:string;createdAt:string}>;
export type ReceiverGrant=Readonly<{id:string;receiverId:string;controllerDeviceId:string;controllerDisplayName:string;receiverKeyFingerprint:string;allowedCommands:readonly string[];authorizationRevision:SocialCounter;state:'pending'|'accepted'|'declined'|'revoked'|'expired';expiresAt:string;createdAt:string;decidedAt:string|null}>;
export type CastReceiverSession=Readonly<{protocolVersion:string;serverTime:string;grantSemantics:'initial'|'extension'|'rotation';deviceToken:string;device:Readonly<{id:string;deviceId:string;displayName:string;receiverId:string;state:string;generation:SocialCounter;expiresAt:string}>;scope:Readonly<{accountId:string;profileId:string;authority:string;capabilities:readonly string[]}>;session:Readonly<{accessToken:string;expiresAt:string}>;applicationId:string}>;

/** Event names on `GET /v1/groups/{id}/events`. `heartbeat` and `resume-gap` are
 * the two kinds that carry no `id:`, so neither moves a client's `Last-Event-ID`
 * resume point; every other kind, `group.snapshot` included, does. */
export const GROUP_EVENT_KINDS=Object.freeze(['group.snapshot','group.transport','group.settings','group.members','group.readiness','group.queue','group.host','group.ended','heartbeat','resume-gap'] as const);
export type GroupEventKind=(typeof GROUP_EVENT_KINDS)[number];
export const GROUP_RESUMABLE_EVENT_KINDS=Object.freeze(['group.transport','group.settings','group.members','group.readiness','group.queue','group.host','group.ended'] as const);

const obj=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
function fail(what:string):never{throw new Error(unreadableServerResponse);}
const id=(v:unknown):v is string=>typeof v==='string'&&/^[A-Za-z0-9_-]{1,128}$/.test(v);
const nullableId=(v:unknown):v is string|null=>v===null||id(v);
const counter=(v:unknown):v is string=>typeof v==='string'&&/^(0|[1-9][0-9]{0,18})$/.test(v);
const text=(v:unknown,max:number):v is string=>typeof v==='string'&&v.length<=max;
const stampOrNull=(v:unknown):v is string|null=>v===null||text(v,64);
const oneOf=<T extends string>(v:unknown,choices:readonly T[]):v is T=>typeof v==='string'&&(choices as readonly string[]).includes(v);
const index=(v:unknown):v is number=>Number.isSafeInteger(v)&&Number(v)>=0&&Number(v)<=100000;

function envelope(v:unknown,what:string):Record<string,unknown>{
 if(!obj(v)||v.protocolVersion!==SOCIAL_PROTOCOL||!text(v.serverTime,64))fail(what);
 return v;
}

function parseRate(v:unknown):SocialRate{
 if(!obj(v)||!counter(v.numerator)||!counter(v.denominator)||v.numerator==='0'||v.denominator==='0')fail('group rate');
 return Object.freeze({numerator:v.numerator,denominator:v.denominator});
}

function parseTimeline(v:unknown):GroupTimeline{
 if(!obj(v)||!text(v.itemId,128)||!text(v.currentEntryId,128)||!oneOf(v.state,['idle','playing','paused','stopped'] as const)||!counter(v.anchorPositionUs)||!text(v.anchorAt,64)||!index(v.queuePosition)||!stampOrNull(v.waitUntil))fail('group timeline');
 return Object.freeze({itemId:v.itemId,currentEntryId:v.currentEntryId,state:v.state,anchorPositionUs:v.anchorPositionUs,anchorAt:v.anchorAt,rate:parseRate(v.rate),queuePosition:v.queuePosition,waitUntil:v.waitUntil});
}

function parseSync(v:unknown):GroupSync{
 const ms=(x:unknown)=>Number.isSafeInteger(x)&&Number(x)>=0&&Number(x)<=600000;
 if(!obj(v)||!ms(v.noCorrectionUnderMs)||!ms(v.rateCorrectionMaxMs)||!ms(v.seekAtOrOverMs)||!text(v.rateCorrectionMinimum,16)||!text(v.rateCorrectionMaximum,16))fail('group sync policy');
 // The bands must be ordered, or a client cannot pick a correction at all.
 if(Number(v.noCorrectionUnderMs)>Number(v.seekAtOrOverMs))fail('group sync policy');
 return Object.freeze({noCorrectionUnderMs:v.noCorrectionUnderMs as number,rateCorrectionMinimum:v.rateCorrectionMinimum,rateCorrectionMaximum:v.rateCorrectionMaximum,rateCorrectionMaxMs:v.rateCorrectionMaxMs as number,seekAtOrOverMs:v.seekAtOrOverMs as number});
}

function parseSettings(v:unknown):GroupSettings{
 if(!obj(v)||typeof v.shuffleEnabled!=='boolean'||!oneOf(v.repeatMode,['none','one','all'] as const))fail('group settings');
 return Object.freeze({shuffleEnabled:v.shuffleEnabled,repeatMode:v.repeatMode});
}

function parseMember(v:unknown):GroupMember{
 if(!obj(v)||!id(v.id)||!text(v.displayName,256)||typeof v.private!=='boolean'||!oneOf(v.role,['host','member'] as const)||!oneOf(v.state,['invited','joined','left','removed'] as const)||!oneOf(v.readiness,['buffering','ready','lagging'] as const)||!counter(v.positionUs)||!stampOrNull(v.reportedAt)||!oneOf(v.presence,['connected','away','disconnected'] as const)||!text(v.joinedAt,64)||v.username!==undefined&&!text(v.username,256))fail('group member');
 return Object.freeze({id:v.id,displayName:v.displayName,...(!v.private&&typeof v.username==='string'?{username:v.username}:{}),private:v.private,role:v.role,state:v.state,readiness:v.readiness,positionUs:v.positionUs,reportedAt:v.reportedAt,presence:v.presence,joinedAt:v.joinedAt});
}

function parseReadinessSummary(v:unknown,members:readonly GroupMember[]):GroupReadinessSummary{
 const count=(x:unknown)=>Number.isSafeInteger(x)&&Number(x)>=0&&Number(x)<=1024;
 if(!obj(v)||!oneOf(v.aggregate,['ready','buffering','lagging'] as const)||!count(v.ready)||!count(v.buffering)||!count(v.lagging)||!count(v.stale)||!count(v.memberCount))fail('group readiness');
 const joined=members.filter(m=>m.state==='joined'&&m.presence!=='disconnected').length;
 if(Number(v.memberCount)!==joined)fail('group readiness');
 if(Number(v.ready)+Number(v.buffering)+Number(v.lagging)+Number(v.stale)!==joined)fail('group readiness');
 // The aggregate is the worst state present; a server that claims `ready` while
 // somebody is lagging or stale would let a client start out of sync.
 const worst=Number(v.lagging)>0||Number(v.stale)>0?'lagging':Number(v.buffering)>0||joined===0?'buffering':'ready';
 if(v.aggregate!==worst)fail('group readiness');
 return Object.freeze({aggregate:v.aggregate,ready:v.ready as number,buffering:v.buffering as number,lagging:v.lagging as number,stale:v.stale as number,memberCount:v.memberCount as number});
}

/** An unavailable entry must be an opaque placeholder: only `entryId` and
 * `position` survive. A server that sends both `unavailable` and an `itemId` is
 * leaking, and a placeholder without a position has lost the ordering the
 * contract requires be preserved. */
export function parseGroupQueue(raw:unknown):GroupQueue{
 if(!obj(raw)||!counter(raw.revision)||!index(raw.position)||!Array.isArray(raw.entries)||raw.entries.length>1024)fail('group queue');
 const seen=new Set<string>();
 const entries=raw.entries.map((entry,at)=>{
  if(!obj(entry)||!id(entry.entryId)||!index(entry.position)||!nullableId(entry.itemId)||typeof entry.unavailable!=='boolean'||!text(entry.addedBy,128))fail('group queue');
  if(entry.position!==at)fail('group queue');
  if(entry.unavailable&&(entry.itemId!==null||entry.addedBy!==''))fail('group queue');
  if(!entry.unavailable&&entry.itemId===null)fail('group queue');
  if(seen.has(entry.entryId))fail('group queue');
  seen.add(entry.entryId);
  return Object.freeze({entryId:entry.entryId,position:entry.position,itemId:entry.itemId,unavailable:entry.unavailable,addedBy:entry.addedBy});
 });
 let eligibility:GroupQueueEligibility|null=null;
 if(raw.eligibility!==null&&raw.eligibility!==undefined){
  const e=raw.eligibility;
  if(!obj(e)||!index(e.blockedEntries)||typeof e.currentEntryBlocked!=='boolean'||!Array.isArray(e.members)||e.members.length>64)fail('group queue eligibility');
  const members=e.members.map(m=>{
   if(!obj(m)||!id(m.memberId)||!text(m.displayName,256)||!Array.isArray(m.blockedEntryIds)||m.blockedEntryIds.length>1024||m.blockedEntryIds.some(x=>!id(x)))fail('group queue eligibility');
   return Object.freeze({memberId:m.memberId,displayName:m.displayName,blockedEntryIds:Object.freeze([...m.blockedEntryIds as string[]])});
  });
  eligibility=Object.freeze({blockedEntries:e.blockedEntries as number,currentEntryBlocked:e.currentEntryBlocked,members:Object.freeze(members)});
 }
 return Object.freeze({revision:raw.revision,position:raw.position,entries:Object.freeze(entries),eligibility});
}

export function parseGroup(raw:unknown):Group{
 if(!obj(raw))fail('group');
 if(!id(raw.id)||!text(raw.name,512)||!oneOf(raw.state,['creating','lobby','preparing','ready','waiting','playing','paused','host-reconnecting-playing','host-reconnecting-paused','degraded','ending','ended','failed'] as const))fail('group');
 if(!oneOf(raw.hostAuthority,['host-only','anyone'] as const)||!id(raw.hostMemberId))fail('group');
 for(const key of ['revision','playbackRevision','settingsRevision','governanceRevision','queueRevision','reconnectGeneration','eventOrdinal'])if(!counter(raw[key]))fail('group');
 if(!text(raw.lastCommand,64)||!text(raw.lastCommandId,128)||!text(raw.endedReason,64)||!text(raw.createdAt,64)||!text(raw.updatedAt,64)||!text(raw.viewerMemberId,128))fail('group');
 const permissions=raw.permissions;
 if(!obj(permissions)||typeof permissions.isHost!=='boolean'||typeof permissions.canControl!=='boolean'||typeof permissions.canManageQueue!=='boolean')fail('group permissions');
 // A host always has control; a server claiming otherwise is inconsistent.
 if(permissions.isHost&&!permissions.canControl)fail('group permissions');
 if(permissions.canClaimHost!==undefined&&typeof permissions.canClaimHost!=='boolean')fail('group permissions');
 const authority=raw.authority;
 if(!obj(authority)||!text(authority.deviceId,128)||!nullableId(authority.playbackId)||!oneOf(authority.state,['fresh','bound','retired'] as const))fail('group authority');
 const host=raw.host;
 if(!obj(host)||!oneOf(host.presence,['connected','reconnecting','absent'] as const)||!text(host.lastSeenAt,64)||!stampOrNull(host.pauseAt)||!stampOrNull(host.endAt))fail('group host');
 if(!Array.isArray(raw.members)||raw.members.length>256)fail('group');
 const members=raw.members.map(parseMember);
 if(members.filter(m=>m.role==='host').length>1)fail('group');
 const timeline=parseTimeline(raw.timeline);
 // `state` and `timeline.state` are two views of one fact; disagreement means a
 // client cannot decide whether to extrapolate the anchor.
 const expected=raw.state==='playing'||raw.state==='host-reconnecting-playing'?'playing':raw.state==='lobby'||raw.state==='creating'?'idle':raw.state==='ended'||raw.state==='failed'?'stopped':'paused';
 if(timeline.state!==expected)fail('group');
 return Object.freeze({
  id:raw.id,name:raw.name,state:raw.state,hostAuthority:raw.hostAuthority,hostMemberId:raw.hostMemberId,
  revision:raw.revision as string,playbackRevision:raw.playbackRevision as string,settingsRevision:raw.settingsRevision as string,governanceRevision:raw.governanceRevision as string,queueRevision:raw.queueRevision as string,
  reconnectGeneration:raw.reconnectGeneration as string,lastCommand:raw.lastCommand,lastCommandId:raw.lastCommandId,
  endedReason:raw.endedReason,eventOrdinal:raw.eventOrdinal as string,createdAt:raw.createdAt,updatedAt:raw.updatedAt,
  permissions:Object.freeze({isHost:permissions.isHost,canControl:permissions.canControl,canManageQueue:permissions.canManageQueue,canClaimHost:permissions.canClaimHost===true}),
  authority:Object.freeze({deviceId:authority.deviceId,playbackId:authority.playbackId,state:authority.state}),
  host:Object.freeze({presence:host.presence,lastSeenAt:host.lastSeenAt,pauseAt:host.pauseAt,endAt:host.endAt}),
  timeline,settings:parseSettings(raw.settings),sync:parseSync(raw.sync),
  readiness:parseReadinessSummary(raw.readiness,members),members:Object.freeze(members),
  queue:raw.queue===null||raw.queue===undefined?null:parseGroupQueue(raw.queue),
  viewerMemberId:raw.viewerMemberId,
 });
}

export function parseGroupSnapshot(raw:unknown):GroupSnapshot{
 const v=envelope(raw,'group snapshot');
 return Object.freeze({protocolVersion:SOCIAL_PROTOCOL,serverTime:v.serverTime as string,group:parseGroup(v.group)});
}

export function parseGroupList(raw:unknown):readonly Group[]{
 const v=envelope(raw,'group list');
 if(!Array.isArray(v.groups)||v.groups.length>256)fail('group list');
 return Object.freeze(v.groups.map(parseGroup));
}

export function parseGroupTransportReceipt(raw:unknown):GroupTransportReceipt{
 const v=envelope(raw,'group transport receipt');
 if(!oneOf(v.groupState,['lobby','preparing','ready','waiting','playing','paused','host-reconnecting-playing','host-reconnecting-paused'] as const)||!id(v.groupId)||!text(v.idempotencyKey,120)||!oneOf(v.disposition,['accepted','duplicate'] as const)||!oneOf(v.command,['play','pause','stop','seek','next','previous','load','set-queue-position'] as const)||!counter(v.revision)||!counter(v.playbackRevision)||!counter(v.queueRevision)||!text(v.recordedAt,64))fail('group transport receipt');
 let override:GroupTransportReceipt['override']=null;
 if(v.override!==null&&v.override!==undefined){
  const o=v.override;
  if(!obj(o)||!text(o.reason,64)||!Array.isArray(o.blockedMemberIds)||o.blockedMemberIds.some(x=>!id(x)))fail('group transport receipt');
  override=Object.freeze({reason:o.reason,blockedMemberIds:Object.freeze([...o.blockedMemberIds as string[]])});
 }
 return Object.freeze({protocolVersion:SOCIAL_PROTOCOL,groupId:v.groupId,idempotencyKey:v.idempotencyKey,disposition:v.disposition,command:v.command,groupState:v.groupState as GroupState,revision:v.revision,playbackRevision:v.playbackRevision,queueRevision:v.queueRevision,recordedAt:v.recordedAt,timeline:parseTimeline(v.timeline),settings:parseSettings(v.settings),override});
}

export function parseGroupInvite(raw:unknown):GroupInvite{
 const v=envelope(raw,'group invitation');
 const i=v.invite;
 const uses=(x:unknown)=>Number.isSafeInteger(x)&&Number(x)>=0&&Number(x)<=1024;
 if(!obj(i)||!id(i.id)||!id(i.groupId)||typeof i.code!=='string'||!/^[A-Z2-9]{4,16}$/.test(i.code)||!text(i.expiresAt,64)||!uses(i.maxUses)||!uses(i.uses)||!text(i.recipientProfileId,128))fail('group invitation');
 return Object.freeze({id:i.id,groupId:i.groupId,code:i.code,expiresAt:i.expiresAt,maxUses:i.maxUses as number,uses:i.uses as number,recipientProfileId:i.recipientProfileId});
}

export function parseReceiver(raw:unknown):Receiver{
 if(!obj(raw)||!id(raw.id)||!oneOf(raw.kind,['portico','cast'] as const)||!text(raw.deviceId,128)||!text(raw.displayName,256)||!text(raw.platform,64)||!text(raw.keyFingerprint,64)||!Array.isArray(raw.supportedCommands)||raw.supportedCommands.some(c=>!text(c,32))||!oneOf(raw.grantPolicy,['per-device','always-ask','open'] as const)||!counter(raw.authorizationRevision)||!oneOf(raw.state,['active','retired'] as const)||!oneOf(raw.presence,['online','offline'] as const)||!text(raw.lastSeenAt,64)||!text(raw.createdAt,64))fail('receiver');
 return Object.freeze({id:raw.id,kind:raw.kind,deviceId:raw.deviceId,displayName:raw.displayName,platform:raw.platform,keyFingerprint:raw.keyFingerprint,supportedCommands:Object.freeze([...raw.supportedCommands as string[]]),grantPolicy:raw.grantPolicy,authorizationRevision:raw.authorizationRevision,state:raw.state,presence:raw.presence,lastSeenAt:raw.lastSeenAt,createdAt:raw.createdAt});
}

export function parseReceiverList(raw:unknown):readonly Receiver[]{
 const v=envelope(raw,'receiver directory');
 if(!Array.isArray(v.receivers)||v.receivers.length>256)fail('receiver directory');
 return Object.freeze(v.receivers.map(parseReceiver));
}

export function parseReceiverGrant(raw:unknown):ReceiverGrant{
 const source=obj(raw)&&obj((raw as Record<string,unknown>).grant)?(raw as Record<string,unknown>).grant:raw;
 const g=source;
 if(!obj(g)||!id(g.id)||!id(g.receiverId)||!text(g.controllerDeviceId,128)||!text(g.controllerDisplayName,256)||!text(g.receiverKeyFingerprint,64)||!Array.isArray(g.allowedCommands)||g.allowedCommands.some(c=>!text(c,32))||!counter(g.authorizationRevision)||!oneOf(g.state,['pending','accepted','declined','revoked','expired'] as const)||!text(g.expiresAt,64)||!text(g.createdAt,64)||!stampOrNull(g.decidedAt))fail('receiver authorization');
 // A grant that cannot load cannot be a handoff target; refusing it here stops a
 // client from preparing a handoff the server will reject.
 if(g.state==='accepted'&&!(g.allowedCommands as string[]).includes('load'))fail('receiver authorization');
 return Object.freeze({id:g.id,receiverId:g.receiverId,controllerDeviceId:g.controllerDeviceId,controllerDisplayName:g.controllerDisplayName,receiverKeyFingerprint:g.receiverKeyFingerprint,allowedCommands:Object.freeze([...g.allowedCommands as string[]]),authorizationRevision:g.authorizationRevision,state:g.state,expiresAt:g.expiresAt,createdAt:g.createdAt,decidedAt:g.decidedAt});
}

/** The handoff invariants a client may rely on without re-deriving them:
 * `sourceRetired` is true only for a committed handoff, `outcome: accepted`
 * belongs only to `state: committed`, and a committed handoff always names the
 * receiver occurrence and the position it actually committed at. */
export function parseHandoff(raw:unknown):Handoff{
 const source=obj(raw)&&obj((raw as Record<string,unknown>).handoff)?(raw as Record<string,unknown>).handoff:raw;
 const h=source;
 if(!obj(h)||!id(h.id)||!id(h.receiverId)||!id(h.grantId)||!text(h.requestId,128)||!oneOf(h.state,['prepared','committing','committed','rolled_back','expired','failed'] as const)||!oneOf(h.outcome,['waiting','pending','accepted','rejected'] as const)||!text(h.reason,64)||!counter(h.revision)||!text(h.itemId,128)||!id(h.sourcePlaybackId)||!text(h.receiverPlaybackId,128))fail('handoff');
 if(!counter(h.requestedPositionUs)||!counter(h.readyPositionUs)||!counter(h.committedPositionUs)||typeof h.sourceRetired!=='boolean'||!text(h.createdAt,64)||!text(h.expiresAt,64)||!stampOrNull(h.settledAt))fail('handoff');
 if(h.sourceRetired!==(h.state==='committed'))fail('handoff');
 if((h.outcome==='accepted')!==(h.state==='committed'))fail('handoff');
 if(h.state==='committed'&&(h.receiverPlaybackId===''||h.committedPositionUs!==h.readyPositionUs))fail('handoff');
 if(h.outcome==='pending'&&h.receiverPlaybackId==='')fail('handoff');
 return Object.freeze({id:h.id,receiverId:h.receiverId,grantId:h.grantId,requestId:h.requestId,state:h.state,outcome:h.outcome,reason:h.reason,revision:h.revision,itemId:h.itemId,sourcePlaybackId:h.sourcePlaybackId,receiverPlaybackId:h.receiverPlaybackId,requestedPositionUs:h.requestedPositionUs,readyPositionUs:h.readyPositionUs,committedPositionUs:h.committedPositionUs,sourceRetired:h.sourceRetired,createdAt:h.createdAt,expiresAt:h.expiresAt,settledAt:h.settledAt});
}

export function parseCastReceiverSession(raw:unknown):CastReceiverSession{
 const v=envelope(raw,'cast receiver session');
 if(!oneOf(v.grantSemantics,['initial','extension','rotation'] as const)||typeof v.deviceToken!=='string'||v.deviceToken.length<43||v.deviceToken.length>256)fail('cast receiver session');
 const d=v.device,s=v.scope,session=v.session;
 if(!obj(d)||!id(d.id)||!text(d.deviceId,128)||!text(d.displayName,256)||!text(d.receiverId,128)||!text(d.state,32)||!counter(d.generation)||!text(d.expiresAt,64))fail('cast receiver session');
 if(!obj(s)||!text(s.accountId,128)||!text(s.profileId,128)||!text(s.authority,32)||!Array.isArray(s.capabilities)||s.capabilities.some(c=>!text(c,32)))fail('cast receiver session');
 if(!obj(session)||typeof session.accessToken!=='string'||session.accessToken.length<43||session.accessToken.length>2048||!text(session.expiresAt,64))fail('cast receiver session');
 // The two credentials must be different values: one is a bearer and one is
 // only ever presented to /v1/cast/reconnect.
 if(session.accessToken===v.deviceToken)fail('cast receiver session');
 if(!text(v.applicationId,64))fail('cast receiver session');
 return Object.freeze({protocolVersion:SOCIAL_PROTOCOL,serverTime:v.serverTime as string,grantSemantics:v.grantSemantics,deviceToken:v.deviceToken,
  device:Object.freeze({id:d.id,deviceId:d.deviceId,displayName:d.displayName,receiverId:d.receiverId,state:d.state,generation:d.generation,expiresAt:d.expiresAt}),
  scope:Object.freeze({accountId:s.accountId,profileId:s.profileId,authority:s.authority,capabilities:Object.freeze([...s.capabilities as string[]])}),
  session:Object.freeze({accessToken:session.accessToken,expiresAt:session.expiresAt}),applicationId:v.applicationId});
}

/** The position a member should be at now, in microseconds. Extrapolates only
 * while the group is playing; a paused anchor is already the answer. */
export function groupTargetPositionUs(timeline:GroupTimeline,serverTimeMs:number,atMs:number):bigint{
 const anchor=BigInt(timeline.anchorPositionUs);
 if(timeline.state!=='playing')return anchor;
 const elapsed=BigInt(Math.max(0,Math.round(atMs-serverTimeMs)));
 return anchor+elapsed*1000n*BigInt(timeline.rate.numerator)/BigInt(timeline.rate.denominator);
}

export type GroupCorrection=Readonly<{action:'none'|'rate'|'seek';rate?:number;seekToUs?:string;holdMs?:number}>;

/** Which correction the §13 bands call for, given the drift in microseconds
 * (positive when the member is behind the group). A client applies this rather
 * than inventing its own thresholds. */
export function groupCorrection(sync:GroupSync,driftUs:bigint,targetUs:bigint):GroupCorrection{
 const magnitude=driftUs<0n?-driftUs:driftUs;
 const ms=Number(magnitude/1000n);
 if(ms<sync.noCorrectionUnderMs)return Object.freeze({action:'none'});
 if(ms>=sync.seekAtOrOverMs)return Object.freeze({action:'seek',seekToUs:targetUs.toString()});
 const bound=driftUs>0n?Number(sync.rateCorrectionMaximum):Number(sync.rateCorrectionMinimum);
 return Object.freeze({action:'rate',rate:bound,holdMs:sync.rateCorrectionMaxMs});
}
