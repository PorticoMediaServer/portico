import {canonicalQualityLadder} from '../../contracts/src/types.ts';
import {unreadableServerResponse} from './server-messages.ts';
/** Server-authored delivery policy and per-session conversion diagnostics.
 *
 * The client renders these; it never derives them. Network class, quality rungs,
 * clamps and reason codes are all the server's conclusions, and a payload that
 * does not match the published shape is rejected rather than repaired. */

/** Where a request comes from, as the server decided it from the owner's home networks. */
export type NetworkClass='local'|'remote'|'unknown';
export type TransportClass='wifi'|'cellular'|'ethernet'|'unknown';
export type DeliveryMode='allow'|'prefer'|'never'|'require';
import type {PlanningPolicy} from './console.ts';

export const networkClasses:readonly NetworkClass[]=Object.freeze(['local','remote','unknown'] as const);
export const transportClasses:readonly TransportClass[]=Object.freeze(['wifi','cellular','ethernet','unknown'] as const);
const deliveryModes:readonly DeliveryMode[]=Object.freeze(['allow','prefer','never','require'] as const);
const planningPolicies:readonly PlanningPolicy[]=Object.freeze(['maximum_fidelity','maximum_compatibility','minimize_server_work'] as const);

/** One narrowing the server applied over the viewer's own choice. Clamps only
 * ever narrow, so `applied` is never greater than `requested`. */
export type DeliveryClamp=Readonly<{field:string;source:string;requested:number;applied:number}>;

export type ResolvedDeliveryPolicy=Readonly<{
 networkClass:NetworkClass;serverLocality:'local'|'remote'|'unknown';transportClass:TransportClass;
 /** Which saved default quality applies here: `quality.home` or `quality.remote`. `qualityMode` is its value. */
 preferenceLane:'home'|'remote';
 directPlay:DeliveryMode;directStream:DeliveryMode;transcode:DeliveryMode;qualityMode:string;
 maxVideoBitrateBps:number;maxAudioBitrateBps:number;maxVideoHeight:number;allowHDR:boolean;
 planningPolicy:PlanningPolicy;clamps:readonly DeliveryClamp[];
}>;

export type QualityRungKind='automatic'|'original'|'fixed';
export type QualityRung=Readonly<{
 id:string;kind:QualityRungKind;label:string;enabled:boolean;reason?:string;sameAsOriginal?:boolean;dataGBPerHour?:number;
 maxVideoBitrateBps?:number;maxAudioBitrateBps?:number;targetDisplayHeight?:number;
}>;

export type DeliveryStrategy='original'|'copy_remux'|'audio_conversion'|'video_conversion';
export type StreamAction='copy'|'convert'|'original'|'drop'|'burn_in'|'unknown';
/** Backends a running session can report; 'auto' is a configuration value, never an outcome. */
export type DeliveryBackend='software'|'videotoolbox'|'vaapi'|'qsv'|'nvenc'|'amf';
export type HardwareStage=Readonly<{operation:'decode'|'deinterlace'|'tone_map'|'subtitle_burn_in'|'scale'|'encode';execution:'hardware'|'software'}>;
/** What one conversion launch actually did. `unknown` is the server saying it
 * could not tell; it is never read as hardware, and this parser never turns a
 * value outside the published set into one either. */
export type HardwareExecutionClass='hardware'|'software'|'unknown';
export type HardwareLaunchTarget=Readonly<{name:string;execution:HardwareExecutionClass}>;
/** One successful conversion start. `startedAt` is RFC 3339. */
export type HardwareLaunch=Readonly<{sequence:number;generation:number;startedAt:string;encoder:HardwareLaunchTarget;decoder:HardwareLaunchTarget;renderNode?:string}>;
/** Execution evidence for a session's conversion starts, published to the
 * owner and admins only; an ordinary viewer's diagnostic omits it. Counts are
 * bounded by `launchCount`, and `fallbackCount` counts hardware launches the
 * server ran again in software. */
export type HardwareExecution=Readonly<{launchCount:number;softwareLaunchCount:number;unknownLaunchCount:number;fallbackCount:number;lastLaunch:HardwareLaunch}>;
export type SessionStreamDecision=Readonly<{kind:'video'|'audio'|'subtitle';inputCodec:string;outputCodec:string;action:StreamAction}>;

/** The per-session decision detail published on the occurrence read. */
export type DeliveryAudioRendition=Readonly<{failure?:string;ordinal:number;streamIndex:number;action:string;codec:string;channels:number;bitrateBps:number;sampleRate?:number;bitDepth?:number;channelLayout?:string;language:string;label:string;title?:string;commentary?:boolean;default:boolean}>;
export type SessionDelivery=Readonly<{
 audioRenditions?:readonly DeliveryAudioRendition[];
 mode:'direct'|'hls'|'remote';strategy:DeliveryStrategy;reasonCodes:readonly string[];policy:ResolvedDeliveryPolicy|null;
 qualityId:string;hardware:Readonly<{backend:DeliveryBackend;stages:readonly HardwareStage[];execution?:HardwareExecution}>;
 streams:readonly SessionStreamDecision[];throttled:boolean;convertedThroughMs:number;
 toneMap:boolean;toneMapAlgorithm?:string;targetDisplayHeight?:number;targetVideoBitrateBps?:number;targetAudioBitrateBps?:number;
 playedRetentionMs:number;throttleBufferMs:number;
 /** Why this route: the device the plan was made for, the source as the planner
  * saw it, and every route with the rules that rejected it. */
 decision?:DecisionTrace;
 audioChannels?:number;downmix?:string;deinterlace?:boolean;outputContainer?:string;
 /** The class of a conversion failure, or of a hardware failure recovered in software. */
 failure?:string;
}>;

export type RouteRejection=Readonly<{code:string;detail?:string}>;
export type RouteEvaluation=Readonly<{route:DeliveryStrategy;transport?:string;admissible:boolean;chosen:boolean;rejections:readonly RouteRejection[]}>;
export type DecisionTrace=Readonly<{
 client:Readonly<{family:string;platform?:string;model?:string;engine?:string;evidence:string;revision:string;published:boolean}>;
 container:string;bitRate?:number;
 video?:Readonly<{rotation?:number;sampleAspectRatio?:string;codec:string;profile?:string;level?:number;bitDepth?:number;width?:number;height?:number;frameRate?:number;interlaced?:boolean;dynamicRange?:string;dolbyVisionProfile?:number;bitRate?:number}>;
 audio?:Readonly<{streamIndex:number;codec:string;channels?:number;language?:string;objectAudio?:string;chosenBy:string;tracks:number}>;
 routes:readonly RouteEvaluation[];notes:readonly string[];
}>;

class DeliveryFailure extends Error{code:string;constructor(message:string){super(message);this.code='invalid_delivery_payload';}}
function invalid():never{throw new DeliveryFailure(unreadableServerResponse);}
const obj=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
const text=(v:unknown,max=256):v is string=>typeof v==='string'&&v.length<=max&&!/[\x00-\x1f\x7f]/.test(v);
const count=(v:unknown,max=Number.MAX_SAFE_INTEGER):v is number=>Number.isSafeInteger(v)&&Number(v)>=0&&Number(v)<=max;
function list(v:unknown,max:number):unknown[]{if(!Array.isArray(v)||v.length>max)invalid();return v;}
function oneOf<T extends string>(v:unknown,allowed:readonly T[]):T{if(typeof v!=='string'||!(allowed as readonly string[]).includes(v))invalid();return v as T;}

function clamp(v:unknown):DeliveryClamp{
 if(!obj(v)||!text(v.field,64)||!text(v.source,64)||!count(v.requested)||!count(v.applied))invalid();
 // A clamp that widened is not a clamp; refuse it rather than display it.
 if(v.applied>v.requested&&v.requested>0)invalid();
 return Object.freeze({field:v.field,source:v.source,requested:v.requested,applied:v.applied});
}

export function parseResolvedDeliveryPolicy(raw:unknown):ResolvedDeliveryPolicy{
 if(!obj(raw))invalid();
 const networkClass=oneOf(raw.networkClass,networkClasses);
 const preferenceLane=oneOf(raw.preferenceLane,['home','remote'] as const);
 // Inside the owner's home networks the home default applies; everywhere else,
 // and wherever the server could not tell, the remote one.
 const expected=networkClass==='local'?'home':'remote';
 if(preferenceLane!==expected)invalid();
 if(typeof raw.allowHDR!=='boolean')invalid();
 if(!count(raw.maxVideoBitrateBps)||!count(raw.maxAudioBitrateBps)||!count(raw.maxVideoHeight,4320))invalid();
 if(!text(raw.qualityMode,64))invalid();
 return Object.freeze({
  networkClass,
  serverLocality:oneOf(raw.serverLocality,['local','remote','unknown'] as const),
  transportClass:oneOf(raw.transportClass,transportClasses),
  preferenceLane,
  directPlay:oneOf(raw.directPlay,deliveryModes),
  directStream:oneOf(raw.directStream,deliveryModes),
  transcode:oneOf(raw.transcode,deliveryModes),
  qualityMode:raw.qualityMode,
  maxVideoBitrateBps:raw.maxVideoBitrateBps,
  maxAudioBitrateBps:raw.maxAudioBitrateBps,
  maxVideoHeight:raw.maxVideoHeight,
  allowHDR:raw.allowHDR,
  planningPolicy:oneOf(raw.planningPolicy,planningPolicies),
  clamps:Object.freeze(list(raw.clamps??[],16).map(clamp)),
 });
}

export function parseQualityRung(raw:unknown):QualityRung{
 if(!obj(raw)||!text(raw.id,64)||!/^[A-Za-z0-9_-]+$/.test(raw.id)||!text(raw.label,128)||typeof raw.enabled!=='boolean')invalid();
 const kind=oneOf(raw.kind,['automatic','original','fixed'] as const);
 for(const key of ['maxVideoBitrateBps','maxAudioBitrateBps','targetDisplayHeight'])if(raw[key]!==undefined&&!count(raw[key],key==='targetDisplayHeight'?4320:400_000_000))invalid();
 // Automatic and original are the server deciding and the source respectively;
 // neither carries a ceiling of its own, and a fixed rung always does.
 const ceilings=raw.maxVideoBitrateBps!==undefined||raw.maxAudioBitrateBps!==undefined||raw.targetDisplayHeight!==undefined;
 if(kind==='fixed'?!ceilings:ceilings)invalid();
 if(raw.reason!==undefined&&!text(raw.reason,64))invalid();
 if(raw.enabled&&raw.reason!==undefined&&raw.reason!=='')invalid();
 return Object.freeze({
  id:raw.id,kind,label:raw.label,enabled:raw.enabled,sameAsOriginal:raw.sameAsOriginal===true,...(typeof raw.dataGBPerHour==='number'&&Number.isFinite(raw.dataGBPerHour)&&raw.dataGBPerHour>=0?{dataGBPerHour:raw.dataGBPerHour}:{}),
  ...(raw.reason===undefined||raw.reason===''?{}:{reason:raw.reason as string}),
  ...(raw.maxVideoBitrateBps===undefined?{}:{maxVideoBitrateBps:raw.maxVideoBitrateBps as number}),
  ...(raw.maxAudioBitrateBps===undefined?{}:{maxAudioBitrateBps:raw.maxAudioBitrateBps as number}),
  ...(raw.targetDisplayHeight===undefined?{}:{targetDisplayHeight:raw.targetDisplayHeight as number}),
 });
}

/** A published rung set has exactly one automatic rung, and it is first. */
export function parseQualityRungs(raw:unknown):readonly QualityRung[]{
 const rungs=list(raw,16).map(parseQualityRung);
 if(rungs.length===0)return Object.freeze(rungs);
 if(rungs.filter(r=>r.kind==='automatic').length!==1||rungs[0].kind!=='original'||rungs[1]?.kind!=='automatic')invalid();
 if(new Set(rungs.map(r=>r.id)).size!==rungs.length)invalid();
 return Object.freeze(rungs);
}

const code=(v:unknown):v is string=>text(v,64)&&/^[a-z0-9_:]+$/.test(v);
const optionalText=(v:unknown,max=64)=>v===undefined||text(v,max);
const optionalCount=(v:unknown,max=Number.MAX_SAFE_INTEGER)=>v===undefined||count(v,max);
const optionalNumber=(v:unknown)=>v===undefined||typeof v==='number'&&Number.isFinite(v)&&v>=0;

/** The trace is an explanation. It is parsed strictly enough to be safe to
 * render, and a trace that does not fit is dropped on its own rather than taking
 * the rest of the technical details with it. */
export function parseDecisionTrace(raw:unknown):DecisionTrace|undefined{
 try{
  if(!obj(raw)||!obj(raw.client)||!text(raw.client.family,32)||!text(raw.client.evidence,16)||!text(raw.client.revision,64)||typeof raw.client.published!=='boolean'||!text(raw.container,32))return undefined;
  const c=raw.client;
  if(!optionalText(c.platform)||!optionalText(c.model)||!optionalText(c.engine))return undefined;
  let video:DecisionTrace['video'];
  if(raw.video!==undefined){
   const v=raw.video;
   if(!obj(v)||!text(v.codec,32)||!optionalCount(v.rotation,360)||!optionalText(v.sampleAspectRatio,24)||!optionalText(v.profile)||!optionalCount(v.level,1000)||!optionalCount(v.bitDepth,16)||!optionalCount(v.width,65536)||!optionalCount(v.height,65536)||!optionalNumber(v.frameRate)||!optionalText(v.dynamicRange,16)||!optionalCount(v.dolbyVisionProfile,31)||!optionalCount(v.bitRate))return undefined;
   video=Object.freeze({rotation:v.rotation as number|undefined,sampleAspectRatio:v.sampleAspectRatio as string|undefined,codec:v.codec,profile:v.profile as string|undefined,level:v.level as number|undefined,bitDepth:v.bitDepth as number|undefined,width:v.width as number|undefined,height:v.height as number|undefined,frameRate:v.frameRate as number|undefined,interlaced:v.interlaced===true,dynamicRange:v.dynamicRange as string|undefined,dolbyVisionProfile:v.dolbyVisionProfile as number|undefined,bitRate:v.bitRate as number|undefined});
  }
  let audio:DecisionTrace['audio'];
  if(raw.audio!==undefined){
   const a=raw.audio;
   if(!obj(a)||!Number.isSafeInteger(a.streamIndex)||!text(a.codec,32)||!optionalCount(a.channels,64)||!optionalText(a.language,35)||!optionalText(a.objectAudio,16)||!text(a.chosenBy,32)||!count(a.tracks,128))return undefined;
   audio=Object.freeze({streamIndex:a.streamIndex as number,codec:a.codec,channels:a.channels as number|undefined,language:a.language as string|undefined,objectAudio:a.objectAudio as string|undefined,chosenBy:a.chosenBy,tracks:a.tracks});
  }
  const routes=list(raw.routes??[],8).map(r=>{
   if(!obj(r)||typeof r.admissible!=='boolean'||typeof r.chosen!=='boolean'||!optionalText(r.transport,16))invalid();
   const rejections=list(r.rejections??[],32).map(x=>{if(!obj(x)||!code(x.code)||!optionalText(x.detail,128))invalid();return Object.freeze({code:x.code,...(x.detail?{detail:x.detail as string}:{})});});
   return Object.freeze({route:oneOf(r.route,['original','copy_remux','audio_conversion','video_conversion'] as const),...(r.transport?{transport:r.transport as string}:{}),admissible:r.admissible,chosen:r.chosen,rejections:Object.freeze(rejections)});
  });
  const notes=list(raw.notes??[],16).filter(code);
  return Object.freeze({client:Object.freeze({family:c.family as string,platform:c.platform as string|undefined,model:c.model as string|undefined,engine:c.engine as string|undefined,evidence:c.evidence as string,revision:c.revision as string,published:c.published as boolean}),container:raw.container,bitRate:optionalCount(raw.bitRate)?raw.bitRate as number|undefined:undefined,video,audio,routes:Object.freeze(routes),notes:Object.freeze(notes)});
 }catch{return undefined;}
}

export function parseDeliveryAudioRenditions(raw:unknown):readonly DeliveryAudioRendition[]|undefined {
 if(!Array.isArray(raw)||raw.length<1||raw.length>16)return undefined;
 const seen=new Set<number>();
 for(let i=0;i<raw.length;i++){const r=raw[i];if(!obj(r)||r.ordinal!==i||!count(r.streamIndex,65535)||seen.has(r.streamIndex)||!['copy','convert'].includes(String(r.action))||!text(r.codec,32)||!count(r.channels,64)||!count(r.bitrateBps,4000000)||!text(r.language,63)||!text(r.label,256)||typeof r.default!=='boolean'||r.title!==undefined&&!text(r.title,4096)||r.commentary!==undefined&&typeof r.commentary!=='boolean'||r.failure!==undefined&&!text(r.failure,64)||r.bitDepth!==undefined&&(typeof r.bitDepth!=='number'||![16,24,32].includes(r.bitDepth))||r.sampleRate!==undefined&&!count(r.sampleRate,192000)||r.channelLayout!==undefined&&!(r.action==='copy'?typeof r.channelLayout==='string'&&/^[a-zA-Z0-9_.()+ -]{1,64}$/.test(r.channelLayout):['mono','stereo','5.1','5.1(side)','7.1'].includes(String(r.channelLayout))))return undefined;seen.add(r.streamIndex);}
 if(raw.filter(r=>r.default).length!==1)return undefined;
 return Object.freeze(raw.map(r=>Object.freeze({...r}))) as readonly DeliveryAudioRendition[];
}
const hardwareExecutions:readonly HardwareExecutionClass[]=Object.freeze(['hardware','software','unknown'] as const);
/** RFC 3339 with an explicit offset; the server's launch time, not free text. */
const rfc3339=/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/;
const executionName=(v:unknown):v is string=>text(v,64)&&v.length>0&&v.trim()===v;
const launchTarget=(v:unknown):HardwareLaunchTarget|undefined=>{
 if(!obj(v)||!executionName(v.name))return undefined;
 const execution=hardwareExecutions.find(e=>e===v.execution);
 return execution?Object.freeze({name:v.name,execution}):undefined;
};
/** Execution evidence is an addition to the diagnostic and is read leniently:
 * a block that does not fit is left out on its own, never a reason to lose the
 * rest. It is never repaired, and an execution outside the published set is not
 * evidence of hardware. */
export function parseHardwareExecution(raw:unknown):HardwareExecution|undefined{
 try{
  if(!obj(raw))return undefined;
  const {launchCount,softwareLaunchCount,unknownLaunchCount,fallbackCount}=raw;
  if(!count(launchCount,1_000_000)||!count(softwareLaunchCount,1_000_000)||!count(unknownLaunchCount,1_000_000)||!count(fallbackCount,1_000_000))return undefined;
  // Successful Starts only: a block with no launch explains nothing, and no
  // subset count may exceed the launches it is drawn from.
  if(launchCount<1||softwareLaunchCount>launchCount||unknownLaunchCount>launchCount||softwareLaunchCount+unknownLaunchCount>launchCount||fallbackCount>launchCount)return undefined;
  const last=raw.lastLaunch;
  if(!obj(last)||!count(last.sequence,1_000_000)||!count(last.generation,1_000_000))return undefined;
  // Sequence numbering is the server's; either base fits a bounded launch set.
  if(last.sequence>launchCount)return undefined;
  if(typeof last.startedAt!=='string'||last.startedAt.length>40||!rfc3339.test(last.startedAt)||Number.isNaN(Date.parse(last.startedAt)))return undefined;
  const encoder=launchTarget(last.encoder),decoder=launchTarget(last.decoder);
  if(!encoder||!decoder)return undefined;
  if(last.renderNode!==undefined&&!executionName(last.renderNode))return undefined;
  return Object.freeze({launchCount,softwareLaunchCount,unknownLaunchCount,fallbackCount,lastLaunch:Object.freeze({sequence:last.sequence,generation:last.generation,startedAt:last.startedAt,encoder,decoder,...(last.renderNode===undefined?{}:{renderNode:last.renderNode})})});
 }catch{return undefined;}
}

export function parseSessionDelivery(raw:unknown):SessionDelivery{
 if(!obj(raw))invalid();
 if(!text(raw.qualityId,64)||typeof raw.throttled!=='boolean'||typeof raw.toneMap!=='boolean')invalid();
 if(!count(raw.convertedThroughMs,Number.MAX_SAFE_INTEGER))invalid();
 if(!count(raw.playedRetentionMs,86_400_000)||!count(raw.throttleBufferMs,3_600_000))invalid();
 if(raw.toneMapAlgorithm!==undefined&&!text(raw.toneMapAlgorithm,32))invalid();
 for(const key of ['targetDisplayHeight','targetVideoBitrateBps','targetAudioBitrateBps'])if(raw[key]!==undefined&&!count(raw[key]))invalid();
 const hardware=obj(raw.hardware)?raw.hardware:invalid();
 const stages=list(hardware.stages??[],16).map(v=>{
  if(!obj(v))invalid();
  return Object.freeze({
 operation:oneOf(v.operation,['decode','deinterlace','tone_map','subtitle_burn_in','scale','encode'] as const),execution:oneOf(v.execution,['hardware','software'] as const)});
 });
 const streams=list(raw.streams??[],8).map(v=>{
  if(!obj(v)||!text(v.inputCodec,64)||!text(v.outputCodec,64))invalid();
  return Object.freeze({kind:oneOf(v.kind,['video','audio','subtitle'] as const),inputCodec:v.inputCodec,outputCodec:v.outputCodec,action:oneOf(v.action,['copy','convert','original','drop','burn_in','unknown'] as const)});
 });
 const reasonCodes=list(raw.reasonCodes??[],16).map(v=>{if(!text(v,64)||!/^[a-z0-9_]+$/.test(v))invalid();return v as string;});
 const execution=parseHardwareExecution(hardware.execution);
 return Object.freeze({
  audioRenditions:parseDeliveryAudioRenditions(raw.audioRenditions),
  mode:oneOf(raw.mode,['direct','hls','remote'] as const),
  strategy:oneOf(raw.strategy,['original','copy_remux','audio_conversion','video_conversion'] as const),
  reasonCodes:Object.freeze(reasonCodes),
  policy:raw.policy==null?null:parseResolvedDeliveryPolicy(raw.policy),
  qualityId:raw.qualityId,
  hardware:Object.freeze({backend:oneOf(hardware.backend,['software','videotoolbox','vaapi','qsv','nvenc','amf'] as const),stages:Object.freeze(stages),...(execution?{execution}:{})}),
  streams:Object.freeze(streams),
  throttled:raw.throttled,
  convertedThroughMs:raw.convertedThroughMs,
  toneMap:raw.toneMap,
  ...(raw.toneMapAlgorithm===undefined?{}:{toneMapAlgorithm:raw.toneMapAlgorithm as string}),
  ...(raw.targetDisplayHeight===undefined?{}:{targetDisplayHeight:raw.targetDisplayHeight as number}),
  ...(raw.targetVideoBitrateBps===undefined?{}:{targetVideoBitrateBps:raw.targetVideoBitrateBps as number}),
  ...(raw.targetAudioBitrateBps===undefined?{}:{targetAudioBitrateBps:raw.targetAudioBitrateBps as number}),
  playedRetentionMs:raw.playedRetentionMs,
  throttleBufferMs:raw.throttleBufferMs,
  // Additions are read leniently: an explanation field that does not fit is
  // left out, never a reason to lose the rest.
  ...(()=>{const decision=parseDecisionTrace(raw.decision);return decision?{decision}:{};})(),
  ...(count(raw.audioChannels,64)&&raw.audioChannels>0?{audioChannels:raw.audioChannels}:{}),
  ...(code(raw.downmix)?{downmix:raw.downmix}:{}),
  ...(raw.deinterlace===true?{deinterlace:true}:{}),
  ...(code(raw.outputContainer)?{outputContainer:raw.outputContainer}:{}),
  ...(code(raw.failure)?{failure:raw.failure}:{}),
 });
}

/** The GET /v1/playback/delivery-policy body. */
export type DeliveryPolicyDocument=Readonly<{serverId:string;policy:ResolvedDeliveryPolicy;ladder:readonly Readonly<{id:string;label:string;targetDisplayHeight:number;maxVideoBitrateBps:number;maxAudioBitrateBps:number;dataGBPerHour:number}>[]}>;

export function parseDeliveryPolicyDocument(raw:unknown):DeliveryPolicyDocument{
 if(!obj(raw)||!text(raw.serverId,256))invalid();
 const ladder=list(raw.ladder??[],16).map(v=>{
  if(!obj(v)||!text(v.id,64)||!text(v.label,128)||!count(v.targetDisplayHeight,4320)||!count(v.maxVideoBitrateBps)||!count(v.maxAudioBitrateBps))invalid();
  return Object.freeze({id:v.id,label:v.label,targetDisplayHeight:v.targetDisplayHeight,maxVideoBitrateBps:v.maxVideoBitrateBps,maxAudioBitrateBps:v.maxAudioBitrateBps,dataGBPerHour:typeof v.dataGBPerHour==='number'?v.dataGBPerHour:0});
 });
 if(ladder.length!==canonicalQualityLadder.length||ladder.some((r,i)=>r.id!==canonicalQualityLadder[i].id||r.maxVideoBitrateBps!==canonicalQualityLadder[i].videoKbps*1000))invalid();
 return Object.freeze({serverId:raw.serverId,policy:parseResolvedDeliveryPolicy(raw.policy),ladder:Object.freeze(ladder)});
}

/** The header a client uses to declare what it is connected over. The server
 * decides what that means; a declaration can only narrow the resolved lane. */
export const transportClassHeader='X-Portico-Transport-Class';
export const deviceClassHeader='X-Portico-Device-Class';
export const appLanguageHeader='X-Portico-App-Language';

export function normalizeTransportClass(value:string|null|undefined):TransportClass{
 switch((value??'').trim().toLowerCase()){
  case 'wifi':case 'wi-fi':return 'wifi';
  case 'cellular':case 'mobile':case 'wwan':return 'cellular';
  case 'ethernet':case 'wired':return 'ethernet';
 }
 return 'unknown';
}

/** What the app knows about its surface and its connection, held once for every
 * transport that talks to the selected server. The playback control and queue
 * transports have their own fetch and never saw the API client's copy, so the
 * request that actually plans a session arrived without either header and every
 * per-network and per-device-class preference resolved to "unknown". */
let sharedTransportClass:TransportClass='unknown';
let sharedAppLanguage='';
let sharedDeviceClass:''|'web'|'mobile'|'tablet'|'television'='';
export function setDeliveryContext(context:{transportClass?:string|null;deviceClass?:''|'web'|'mobile'|'tablet'|'television';appLanguage?:string}){
 if(context.transportClass!==undefined)sharedTransportClass=normalizeTransportClass(context.transportClass);
 if(context.deviceClass!==undefined)sharedDeviceClass=context.deviceClass;
 if(context.appLanguage!==undefined)sharedAppLanguage=context.appLanguage ? Intl.getCanonicalLocales(context.appLanguage)[0] ?? '' : '';
}
export function deliveryContextHeaders():Record<string,string>{
 return {...(sharedTransportClass==='unknown'?{}:{[transportClassHeader]:sharedTransportClass}),...(sharedDeviceClass?{[deviceClassHeader]:sharedDeviceClass}:{}),...(sharedAppLanguage?{[appLanguageHeader]:sharedAppLanguage}:{})};
}
