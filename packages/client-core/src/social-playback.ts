import {unreadableServerResponse} from './server-messages.ts';
/** Wire types and parsers for remote playback (Portico receiver handoff, Google Cast pairing).
 *
 * Every parser is total: it either returns a frozen value whose invariants the
 * rest of the client can rely on, or it throws. Nothing here reaches the network,
 * holds state, or touches a renderer. See `server/api/social.md`.
 */

export const SOCIAL_PROTOCOL='1.0';

export type SocialCounter=string;
export type HandoffState='prepared'|'committing'|'committed'|'rolled_back'|'expired'|'failed';
export type HandoffOutcome='waiting'|'pending'|'accepted'|'rejected';
export type Handoff=Readonly<{id:string;receiverId:string;grantId:string;requestId:string;state:HandoffState;outcome:HandoffOutcome;reason:string;revision:SocialCounter;itemId:string;sourcePlaybackId:string;receiverPlaybackId:string;requestedPositionUs:SocialCounter;readyPositionUs:SocialCounter;committedPositionUs:SocialCounter;sourceRetired:boolean;createdAt:string;expiresAt:string;settledAt:string|null}>;
export type ReceiverKind='portico'|'cast';
export type ReceiverGrantPolicy='per-device'|'always-ask'|'open';
export type Receiver=Readonly<{id:string;kind:ReceiverKind;deviceId:string;displayName:string;platform:string;keyFingerprint:string;supportedCommands:readonly string[];grantPolicy:ReceiverGrantPolicy;authorizationRevision:SocialCounter;state:'active'|'retired';presence:'online'|'offline';lastSeenAt:string;createdAt:string}>;
export type ReceiverGrant=Readonly<{id:string;receiverId:string;controllerDeviceId:string;controllerDisplayName:string;receiverKeyFingerprint:string;allowedCommands:readonly string[];authorizationRevision:SocialCounter;state:'pending'|'accepted'|'declined'|'revoked'|'expired';expiresAt:string;createdAt:string;decidedAt:string|null}>;
export type CastReceiverSession=Readonly<{protocolVersion:string;serverTime:string;grantSemantics:'initial'|'extension'|'rotation';deviceToken:string;device:Readonly<{id:string;deviceId:string;displayName:string;receiverId:string;state:string;generation:SocialCounter;expiresAt:string}>;scope:Readonly<{accountId:string;profileId:string;authority:string;capabilities:readonly string[]}>;session:Readonly<{accessToken:string;expiresAt:string}>;applicationId:string}>;

const obj=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
function fail(_what:string):never{throw new Error(unreadableServerResponse);}
const id=(v:unknown):v is string=>typeof v==='string'&&/^[A-Za-z0-9_-]{1,128}$/.test(v);
const counter=(v:unknown):v is string=>typeof v==='string'&&/^(0|[1-9][0-9]{0,18})$/.test(v);
const text=(v:unknown,max:number):v is string=>typeof v==='string'&&v.length<=max;
const stampOrNull=(v:unknown):v is string|null=>v===null||text(v,64);
const oneOf=<T extends string>(v:unknown,choices:readonly T[]):v is T=>typeof v==='string'&&(choices as readonly string[]).includes(v);

function envelope(v:unknown,what:string):Record<string,unknown>{
 if(!obj(v)||v.protocolVersion!==SOCIAL_PROTOCOL||!text(v.serverTime,64))fail(what);
 return v;
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
