/** The capability document: the one description of what this device can play.
 * A client publishes it with `PUT /v1/me/devices/current/capabilities`; the
 * server stores it as it is and plans every start and re-plan from it. There is
 * no second document and no translation.
 *
 * A document describes the device (display, audio output, the viewer's player
 * setting and overrides) and one profile per player engine: `native`, the
 * platform's own player, always; `mpv` where the app carries it. Each engine
 * says what it can really do: containers and codecs per transport, video codecs
 * with profile, level, bit depth and resolution, the dynamic ranges it decodes,
 * whether it tone-maps HDR itself, the audio it decodes and the audio it can
 * hand to a receiver undecoded, and the subtitle kinds it draws itself.
 *
 * Two sources of truth meet here. A **declared** table says what a platform's
 * player engine is known to take (AVPlayer has never played a Matroska file, and
 * no probe will change that). A **probed** fact was asked of the actual device at
 * run time (this browser decodes HEVC, this television's HDMI sink takes eight
 * channels, this Apple TV is set to a Dolby Vision display mode). An engine
 * profile starts from the declared table for its family and is narrowed or
 * widened by what the probe finds; every codec entry records which of the two it
 * rests on, so a wrong decision can be traced to its source.
 *
 * Wire version 2. Codec names are FFmpeg's (the server folds the common
 * synonyms). Levels use FFmpeg's numbering (H.264 4.1 is 41, HEVC 5.1 is 153).
 * A zero or an empty list means "not stated", which never rejects a route. */

export const capabilityDocumentVersion=2 as const;
export const maxCapabilityDocumentBytes=64*1024;

export type ProfileEvidence='declared'|'probed'|'mixed';
export type DynamicRange='sdr'|'hdr10'|'hdr10plus'|'hlg'|'dolby_vision';
export type ProfileTransportKind='direct'|'hls_ts'|'hls_fmp4';
/** The engines a document may describe, and the name a plan uses for the one it chose. */
export type EngineId='native'|'mpv';
/** The viewer's player setting on this device. */
export type EngineSetting='automatic'|'native'|'mpv';
/** Subtitle kinds an engine draws itself. */
import type {SubtitleRendering} from './subtitles.ts';
export const subtitleRenderingKinds:readonly SubtitleRendering[]=['text','ass','pgs','vobsub','dvb'];

export type ClientIdentity={family:string;form?:'tv'|'phone'|'tablet'|'desktop'|'browser'|'cast'|'speaker';platform?:string;platformVersion?:string;os?:string;osVersion?:string;model?:string;app?:string;appVersion?:string};
/** What the panel or HDMI sink shows, as distinct from what an engine decodes. */
export type ClientDisplay={width?:number;height?:number;dynamicRanges?:DynamicRange[];maxFrameRate?:number};
export type AudioVariant='dts_core'|'dts_hd_hra'|'dts_hd_ma'|'dtsx'|'dts_express'|'truehd'|'truehd_atmos'|'ac3'|'eac3'|'eac3_joc'|'ac4'|'lpcm';
export type ClientVideoCodec={chromaSubsampling?:('4:2:0'|'4:2:2'|'4:4:4'|'4:0:0')[];frameRateModes?:('constant'|'variable')[];codec:string;profiles?:string[];maxLevel?:number;bitDepths?:number[];maxWidth?:number;maxHeight?:number;maxFrameRate?:number;maxBitrateBps?:number;
 /** What the decoder decodes. Whether it can be shown is the display's list or the engine's `toneMapsHdr`. */
 dynamicRanges?:DynamicRange[];dolbyVisionProfiles?:number[];dolbyVisionProfile8Compatibility?:(1|2|4)[];interlaced?:boolean;
 /** The only sample entries the engine plays from an original file (AVFoundation: `hvc1`, not `hev1`). */
 codecTags?:string[];
 /** The decoder's operating points ("4K at 30, 1080p at 60"; 10-bit only to 1080p). When stated, a
  * picture must also fit one that applies to its bit depth; the fields above stay the overall ceilings. */
 limits?:ClientVideoLimit[];
 evidence?:ProfileEvidence};
/** One operating point of a video decoder. `bitDepths` absent: every depth the codec lists. */
export type ClientVideoLimit={bitDepths?:number[];maxWidth?:number;maxHeight?:number;maxFrameRate?:number;maxLevel?:number};
/** An audio codec the engine decodes itself. */
export type ClientAudioCodec={variants?:AudioVariant[];codec:string;maxChannels?:number;maxSampleRate?:number;maxBitDepth?:number;objectAudio?:('atmos'|'dtsx')[];evidence?:ProfileEvidence};
/** An audio codec the engine can hand to the audio output undecoded. Used only while
 * `audioOutput.route` is hdmi, arc, earc, optical or spdif and the viewer has not turned it off. */
export type ClientAudioPassthrough={variants?:AudioVariant[];codec:string;maxChannels?:number;objectAudio?:('atmos'|'dtsx')[];evidence?:ProfileEvidence};
export type ClientTransport={transport:ProfileTransportKind;containers?:string[];video?:string[];audio?:string[]};
export type ClientAudioOutput={route?:string;maxChannels?:number;spatial?:boolean};
/** The viewer's "if something looks or sounds wrong" switches. They only ever narrow. */
export type CapabilityOverrides={forceStereo?:boolean;disablePassthrough?:boolean;disableHdr?:boolean};
export type ClientAudioDecode={codec:string;containers:string[];maxSampleRate?:number;sampleRates?:number[];maxChannels?:number;maxBitDepth?:number;via?:string};
/** What one player engine on the device can really do. */
export type EngineCapabilities={
 /** The engine's own name and version: avplayer, exoplayer, hls.js, mpv. */
 name?:string;version?:string;evidence:ProfileEvidence;
 video?:ClientVideoCodec[];audio?:ClientAudioCodec[];audioPassthrough?:ClientAudioPassthrough[];transports?:ClientTransport[];
 subtitleRendering?:SubtitleRendering[];
 /** The engine shows HDR it decodes acceptably on a display that is not HDR. */
 toneMapsHdr?:boolean;
 /** The engine can choose between audio tracks inside an original file. */
 embeddedAudioSwitching?:boolean;
 /** A second engine (mpv) plays live channels: a timeshift window on the channel's own clock. The
  * native engine always does; a second engine that does not say so is never given a channel. */
 liveChannels?:boolean;
};
export type CapabilityDocument={
 version:typeof capabilityDocumentVersion;client:ClientIdentity;
 /** The viewer's player setting. Absent reads as automatic. */
 engineSetting?:EngineSetting;overrides?:CapabilityOverrides;
 display?:ClientDisplay;audioOutput?:ClientAudioOutput;maxBitrateBps?:number;
 engines:{native:EngineCapabilities;mpv?:EngineCapabilities};
 /** What the client's own music engine decodes (spec §18.1). */
 audioDecode?:ClientAudioDecode[];
};
/** The stored document with the revision `If-Match` takes (`GET` of the same route). */
export type StoredCapabilities=Readonly<{revision:number;capabilities:CapabilityDocument}>;

const token=/^[a-z0-9][a-z0-9_.+-]{0,31}$/;
const ranges:readonly string[]=['sdr','hdr10','hdr10plus','hlg','dolby_vision'];
const audioVariants:readonly string[]=['dts_core','dts_hd_hra','dts_hd_ma','dtsx','dts_express','truehd','truehd_atmos','ac3','eac3','eac3_joc','ac4','lpcm'];
const variantsAccepted=(v:unknown)=>v===undefined||Array.isArray(v)&&v.length<=16&&v.every(x=>audioVariants.includes(x as string));
const evidences:readonly string[]=['declared','probed','mixed'];
const text=(v:unknown,max:number)=>v===undefined||typeof v==='string'&&v.length<=max&&!/[\x00-\x1f\x7f]/.test(v);
const int=(v:unknown,max:number)=>v===undefined||Number.isSafeInteger(v)&&Number(v)>=0&&Number(v)<=max;
const num=(v:unknown,max:number)=>v===undefined||typeof v==='number'&&Number.isFinite(v)&&v>=0&&v<=max;
const tokens=(v:unknown,max:number)=>v===undefined||Array.isArray(v)&&v.length<=max&&v.every(x=>typeof x==='string'&&token.test(x));
const rangeList=(v:unknown)=>v===undefined||Array.isArray(v)&&v.length<=8&&v.every(x=>ranges.includes(x as string));
const bool=(v:unknown)=>v===undefined||typeof v==='boolean';

const limitAccepted=(l:ClientVideoLimit)=>!!l&&typeof l==='object'&&int(l.maxWidth,32768)&&int(l.maxHeight,32768)&&num(l.maxFrameRate,1000)&&int(l.maxLevel,1000)&&!!(l.maxWidth||l.maxHeight||l.maxFrameRate||l.maxLevel)
 &&(l.bitDepths===undefined||Array.isArray(l.bitDepths)&&l.bitDepths.length<=8&&l.bitDepths.every(x=>Number.isInteger(x)&&x>=1&&x<=16));

function engineProblem(e:EngineCapabilities|undefined,path:string):string|null{
 if(!e||typeof e!=='object')return path;
 if(!evidences.includes(e.evidence))return path+'.evidence';
 if(!text(e.name,64)||!text(e.version,64))return path+'.name';
 if((e.video?.length??0)>32||(e.audio?.length??0)>48||(e.audioPassthrough?.length??0)>24||(e.transports?.length??0)>16)return path+'.bounds';
 for(const v of e.video??[]){
  if(typeof v.codec!=='string'||!token.test(v.codec)||(v.profiles??[]).length>32||!(v.profiles??[]).every(x=>typeof x==='string'&&token.test(x.trim().toLowerCase().replace(/ /g,'_')))||!int(v.maxLevel,1000)||!int(v.maxWidth,32768)||!int(v.maxHeight,32768)||!num(v.maxFrameRate,1000)||!int(v.maxBitrateBps,2_000_000_000)||!rangeList(v.dynamicRanges)||!tokens(v.codecTags,8)||!bool(v.interlaced))return path+'.video.'+v.codec;
  if((v.bitDepths??[]).length>8||!(v.bitDepths??[]).every(x=>Number.isInteger(x)&&x>=1&&x<=16))return path+'.video.'+v.codec+'.bitDepths';
  if(v.limits!==undefined&&!(Array.isArray(v.limits)&&v.limits.length<=16&&v.limits.every(limitAccepted)))return path+'.video.'+v.codec+'.limits';
  if((v.dolbyVisionProfiles??[]).length>16||!(v.dolbyVisionProfiles??[]).every(x=>Number.isInteger(x)&&x>=1&&x<=31))return path+'.video.'+v.codec+'.dolbyVisionProfiles';
  if(v.dolbyVisionProfile8Compatibility!==undefined&&!(Array.isArray(v.dolbyVisionProfile8Compatibility)&&v.dolbyVisionProfile8Compatibility.length<=3&&new Set(v.dolbyVisionProfile8Compatibility).size===v.dolbyVisionProfile8Compatibility.length&&v.dolbyVisionProfile8Compatibility.every(x=>[1,2,4].includes(x))))return path+'.video.'+v.codec+'.dolbyVisionProfile8Compatibility';
  if(v.chromaSubsampling!==undefined&&!(Array.isArray(v.chromaSubsampling)&&v.chromaSubsampling.length<=4&&v.chromaSubsampling.every(x=>['4:2:0','4:2:2','4:4:4','4:0:0'].includes(x))))return path+'.video.'+v.codec+'.chromaSubsampling';
  if(v.frameRateModes!==undefined&&!(Array.isArray(v.frameRateModes)&&v.frameRateModes.length<=2&&v.frameRateModes.every(x=>['constant','variable'].includes(x))))return path+'.video.'+v.codec+'.frameRateModes';
  if(v.evidence!==undefined&&!evidences.includes(v.evidence))return path+'.video.'+v.codec+'.evidence';
 }
 for(const a of e.audio??[]){
  if(!variantsAccepted(a.variants)||typeof a.codec!=='string'||!token.test(a.codec)||!int(a.maxChannels,64)||!int(a.maxSampleRate,1<<22)||!int(a.maxBitDepth,64)||!tokens(a.objectAudio,4))return path+'.audio.'+a.codec;
  if(a.evidence!==undefined&&!evidences.includes(a.evidence))return path+'.audio.'+a.codec+'.evidence';
 }
 for(const a of e.audioPassthrough??[]){
  if(!variantsAccepted(a.variants)||typeof a.codec!=='string'||!token.test(a.codec)||!int(a.maxChannels,64)||!tokens(a.objectAudio,4))return path+'.audioPassthrough.'+a.codec;
  if(a.evidence!==undefined&&!evidences.includes(a.evidence))return path+'.audioPassthrough.'+a.codec+'.evidence';
 }
 for(const t of e.transports??[]){
  if(!['direct','hls_ts','hls_fmp4'].includes(t.transport)||!tokens(t.containers,32)||!tokens(t.video,32)||!tokens(t.audio,48))return path+'.transports';
 }
 if(e.subtitleRendering!==undefined&&!(Array.isArray(e.subtitleRendering)&&e.subtitleRendering.length<=8&&e.subtitleRendering.every(x=>(subtitleRenderingKinds as readonly string[]).includes(x))))return path+'.subtitleRendering';
 if(!bool(e.toneMapsHdr)||!bool(e.embeddedAudioSwitching)||!bool(e.liveChannels))return path+'.flags';
 return null;
}

/** Mirrors the server's admission rules, so a document that passes here is one
 * the server accepts. Returns the first problem found, or null. */
export function capabilityDocumentProblem(d:CapabilityDocument):string|null{
 if(!d||d.version!==capabilityDocumentVersion)return 'version';
 if(!d.client||typeof d.client.family!=='string'||!token.test(d.client.family))return 'client.family';
 if(d.client.form!==undefined&&!token.test(d.client.form))return 'client.form';
 for(const key of ['platform','platformVersion','os','osVersion','model','app','appVersion'] as const)if(!text(d.client[key],64))return 'client.'+key;
 if(d.engineSetting!==undefined&&!['automatic','native','mpv'].includes(d.engineSetting))return 'engineSetting';
 const o=d.overrides??{};
 if(!bool(o.forceStereo)||!bool(o.disablePassthrough)||!bool(o.disableHdr))return 'overrides';
 const display=d.display??{};
 if(!int(display.width,32768)||!int(display.height,32768)||!num(display.maxFrameRate,1000)||!rangeList(display.dynamicRanges))return 'display';
 const out=d.audioOutput??{};
 if(out.route!==undefined&&out.route!==''&&!token.test(out.route)||!int(out.maxChannels,64)||!bool(out.spatial))return 'audioOutput';
 if(!int(d.maxBitrateBps,2_000_000_000))return 'maxBitrateBps';
 if((d.audioDecode?.length??0)>32)return 'audioDecode';
 for(const a of d.audioDecode??[]){
  if(typeof a.codec!=='string'||!token.test(a.codec)||!Array.isArray(a.containers)||!a.containers.length||!tokens(a.containers,16)||!int(a.maxSampleRate,1<<22)||!int(a.maxChannels,64)||!int(a.maxBitDepth,64)||(a.sampleRates!==undefined&&!(Array.isArray(a.sampleRates)&&a.sampleRates.length<=32&&a.sampleRates.every(r=>Number.isSafeInteger(r)&&r>0&&r<=1<<22)))||!text(a.via,32))return 'audioDecode.'+a.codec;
 }
 if(!d.engines||typeof d.engines!=='object')return 'engines';
 const native=engineProblem(d.engines.native,'engines.native');
 if(native)return native;
 if(d.engines.mpv!==undefined){const mpv=engineProblem(d.engines.mpv,'engines.mpv');if(mpv)return mpv;}
 if(JSON.stringify(d).length>maxCapabilityDocumentBytes)return 'size';
 return null;
}

/** The document with the viewer's device settings stamped on it: the player
 * setting and the overrides. A setting the device cannot honour (mpv on a device
 * without it) reads as automatic. */
export function withViewerSettings(d:CapabilityDocument,settings:{engine?:EngineSetting;overrides?:CapabilityOverrides}):CapabilityDocument{
 const engine=settings.engine==='mpv'&&!d.engines.mpv?'automatic':settings.engine??d.engineSetting??'automatic';
 const o=settings.overrides??d.overrides??{};
 return {...d,engineSetting:engine,overrides:{forceStereo:o.forceStereo===true,disablePassthrough:o.disablePassthrough===true,disableHdr:o.disableHdr===true}};
}

/** Whether an engine of the document draws a subtitle kind itself. */
export function engineRendersSubtitle(d:CapabilityDocument,engine:EngineId,kind:SubtitleRendering):boolean{
 return (d.engines[engine]??d.engines.native).subtitleRendering?.includes(kind)===true;
}

const v=(codec:string,more:Partial<ClientVideoCodec>={}):ClientVideoCodec=>({codec,evidence:'declared',...more});
const a=(codec:string,maxChannels:number,more:Partial<ClientAudioCodec>={}):ClientAudioCodec=>({codec,maxChannels,evidence:'declared',...more});
const pass=(codec:string,maxChannels:number,more:Partial<ClientAudioPassthrough>={}):ClientAudioPassthrough=>({codec,maxChannels,evidence:'declared',...more});
const audioFiles=(audio:string[],containers:string[]):ClientTransport=>({transport:'direct',containers,video:[],audio});
const textOnly:SubtitleRendering[]=['text'];
const defined=<T extends Record<string,unknown>>(o:T):T=>Object.fromEntries(Object.entries(o).filter(([,x])=>x!==undefined)) as T;

/* ------------------------------------------------------------------------- */
/* Declared tables, one per client family.                                     */
/* ------------------------------------------------------------------------- */

/** Identity plus the engine's own name and version, as the probes report them. */
export type ProbeIdentity=Partial<ClientIdentity>&{engine?:string;engineVersion?:string};
const identityOf=(i:ProbeIdentity):Partial<ClientIdentity>=>{const {engine:_e,engineVersion:_v,...rest}=i;return rest;};

/** A browser playing through a `<video>` element and hls.js. This table is the
 * floor every browser meets; `probeWebProfile` is what finds HEVC, AV1, HDR and
 * surround where they exist. The browser draws text subtitles through the app. */
export function webDeclaredProfile(identity:ProbeIdentity={}):CapabilityDocument{
 return {
  version:2,client:{family:'web',form:'browser',app:'portico-web',...identityOf(identity)},engineSetting:'automatic',
  display:{dynamicRanges:['sdr']},audioOutput:{maxChannels:2},
  engines:{native:defined({
   name:identity.engine??'hls.js',version:identity.engineVersion,evidence:'declared' as const,
   video:[v('h264',{bitDepths:[8],maxWidth:4096,maxHeight:2160,maxFrameRate:60,dynamicRanges:['sdr']})],
   audio:[a('aac',2),a('mp3',2)],
   transports:[
    {transport:'direct' as const,containers:['mp4','m4v','mov'],video:['h264'],audio:['aac','mp3']},
    audioFiles(['aac','mp3'],['mp3','m4a','m4b','aac']),
    {transport:'hls_ts' as const,containers:['mpegts'],video:['h264'],audio:['aac','mp3']},
   ],
   subtitleRendering:[...textOnly],
  })},
 };
}

/** AVPlayer on iOS, iPadOS and tvOS. AVFoundation plays MP4-family containers and
 * HLS and nothing else: Matroska, AVI and MPEG-TS files are always repackaged.
 * HEVC is universal on supported hardware, under the `hvc1` sample entry only;
 * AV1, Dolby Vision and the display's HDR modes are per device and come from the
 * native probe. AVPlayer selects among a file's audio tracks itself. */
export function appleDeclaredProfile(options:{television:boolean;osVersion?:string;model?:string;appVersion?:string;phone?:boolean}):CapabilityDocument{
 const mp4Audio=['aac','mp3','ac3','eac3','alac','flac'];
 return {
  version:2,engineSetting:'automatic',
  client:defined({family:'apple',form:options.television?'tv' as const:options.phone===false?'tablet' as const:'phone' as const,platform:options.television?'tvos':'ios',os:options.television?'tvos':'ios',osVersion:options.osVersion,model:options.model,app:'portico-apple',appVersion:options.appVersion}),
  display:{dynamicRanges:['sdr']},audioOutput:{maxChannels:2},
  engines:{native:{
   name:'avplayer',evidence:'declared',
   video:[
    v('h264',{profiles:['baseline','constrained_baseline','main','high'],chromaSubsampling:['4:2:0'],bitDepths:[8],maxWidth:4096,maxHeight:2304,maxFrameRate:60,dynamicRanges:['sdr']}),
    v('hevc',{profiles:['main','main_10'],chromaSubsampling:['4:2:0'],bitDepths:[8,10],maxWidth:4096,maxHeight:2304,maxFrameRate:60,dynamicRanges:['sdr'],codecTags:['hvc1','dvh1']}),
   ],
   audio:[a('aac',8),a('mp3',2),a('ac3',6),a('eac3',8),a('alac',8),a('flac',8),a('pcm',8)],
   transports:[
    {transport:'direct',containers:['mp4','m4v','mov'],video:['h264','hevc'],audio:mp4Audio},
    audioFiles(['aac','mp3','alac','flac','pcm'],['mp3','m4a','m4b','aac','flac','wav','aiff']),
    {transport:'hls_ts',containers:['mpegts'],video:['h264'],audio:['aac','mp3','ac3','eac3']},
    {transport:'hls_fmp4',containers:['fmp4'],video:['h264','hevc'],audio:mp4Audio},
   ],
   subtitleRendering:[...textOnly],
   embeddedAudioSwitching:true,
  }},
 };
}

/** Media3 / ExoPlayer on Android phones, tablets and televisions. The extractor
 * set is wide (Matroska, MP4, TS, WebM, FLAC, Ogg, WAV), so most files play as
 * they are; which *codecs* decode is per device and is what `MediaCodecList`
 * answers. The app draws text subtitles; it declares no styled or image
 * rendering until it really has one. */
export function androidDeclaredProfile(options:{television?:boolean;family?:'android'|'androidtv'|'firetv';osVersion?:string;model?:string;tablet?:boolean}={}):CapabilityDocument{
 const family=options.family??(options.television?'androidtv':'android');
 const video=['h264','hevc','vp9','vp8','mpeg4'];
 const audio=['aac','mp3','opus','vorbis','flac','pcm'];
 return {
  version:2,engineSetting:'automatic',
  client:defined({family,form:family==='android'?(options.tablet?'tablet' as const:'phone' as const):'tv' as const,platform:'android',os:'android',osVersion:options.osVersion,model:options.model}),
  display:{dynamicRanges:['sdr']},audioOutput:{maxChannels:2},
  engines:{native:{
   name:'exoplayer',evidence:'declared',
   video:[v('h264',{bitDepths:[8],maxWidth:1920,maxHeight:1080,maxFrameRate:60,dynamicRanges:['sdr']}),v('hevc',{bitDepths:[8],maxWidth:1920,maxHeight:1080,dynamicRanges:['sdr']}),v('vp9',{bitDepths:[8],maxWidth:1920,maxHeight:1080,dynamicRanges:['sdr']}),v('vp8',{bitDepths:[8]}),v('mpeg4',{bitDepths:[8]})],
   audio:[a('aac',6),a('mp3',2),a('opus',6),a('vorbis',6),a('flac',8),a('pcm',8)],
   transports:[
    {transport:'direct',containers:['mp4','m4v','mov','mkv','webm','ts','m2ts'],video,audio},
    audioFiles(audio,['mp3','m4a','m4b','aac','flac','ogg','opus','wav']),
    {transport:'hls_ts',containers:['mpegts'],video:['h264','hevc'],audio:['aac','mp3','ac3','eac3']},
    {transport:'hls_fmp4',containers:['fmp4'],video:['h264','hevc','vp9','av1'],audio:['aac','mp3','ac3','eac3','flac','opus']},
   ],
   subtitleRendering:[...textOnly],
   embeddedAudioSwitching:true,
  }},
 };
}

/** Amazon Fire TV (Fire OS, Android-based). As Android TV; Dolby audio is passed
 * to the receiver where the HDMI sink reports it (the native probe). */
export const fireTvDeclaredProfile=(options:{osVersion?:string;model?:string}={})=>androidDeclaredProfile({...options,family:'firetv',television:true});

/** Amazon Vega OS (Fire TV without Android): a React Native app over Amazon's
 * W3C media player, which plays MP4 and HLS/DASH through MSE-like sources. It is
 * planned like a browser with HEVC until the device is probed. Dolby audio is
 * handed to the HDMI sink, so it is used only once the probe reports the route. */
export function vegaDeclaredProfile(options:{osVersion?:string;model?:string}={}):CapabilityDocument{
 const base=webDeclaredProfile({platform:'vega',os:'vega',osVersion:options.osVersion,model:options.model,engine:'w3cmedia',app:'portico-vega'});
 base.client.family='vega';base.client.form='tv';
 const e=base.engines.native;
 e.video!.push(v('hevc',{bitDepths:[8,10],maxWidth:3840,maxHeight:2160,maxFrameRate:60,dynamicRanges:['sdr']}));
 e.audioPassthrough=[pass('ac3',6),pass('eac3',8,{objectAudio:['atmos']})];
 e.transports=[
  {transport:'direct',containers:['mp4','m4v','mov'],video:['h264','hevc'],audio:['aac','mp3','ac3','eac3']},
  audioFiles(['aac','mp3'],['mp3','m4a','m4b','aac']),
  {transport:'hls_ts',containers:['mpegts'],video:['h264'],audio:['aac','mp3','ac3','eac3']},
  {transport:'hls_fmp4',containers:['fmp4'],video:['h264','hevc'],audio:['aac','mp3','ac3','eac3']},
 ];
 return base;
}

/** LG webOS: the platform media pipeline behind a `<video>` element. It plays
 * Matroska and TS directly and takes HEVC on every supported model year. Dolby
 * audio is listed for passthrough and used once a probe reports the route; DTS
 * disappeared in 2020–2022 sets and is left to the probe. */
export function webosDeclaredProfile(options:{osVersion?:string;model?:string}={}):CapabilityDocument{
 const video=['h264','hevc','vp9','mpeg2video','mpeg4'];
 const audio=['aac','mp3','ac3','eac3','pcm','vorbis','opus','flac'];
 return {
  version:2,engineSetting:'automatic',
  client:defined({family:'webos',form:'tv' as const,platform:'webos',os:'webos',osVersion:options.osVersion,model:options.model}),
  display:{dynamicRanges:['sdr']},audioOutput:{maxChannels:2},
  engines:{native:{
   name:'webos-media',evidence:'declared',
   video:[v('h264',{bitDepths:[8],maxWidth:3840,maxHeight:2160,maxFrameRate:60,dynamicRanges:['sdr'],interlaced:true}),v('hevc',{bitDepths:[8,10],maxWidth:3840,maxHeight:2160,maxFrameRate:60,dynamicRanges:['sdr']}),v('vp9',{bitDepths:[8,10],maxWidth:3840,maxHeight:2160,dynamicRanges:['sdr']}),v('mpeg2video',{bitDepths:[8],maxWidth:1920,maxHeight:1080,interlaced:true}),v('mpeg4',{bitDepths:[8],maxWidth:1920,maxHeight:1080})],
   audio:[a('aac',6),a('mp3',2),a('pcm',6),a('vorbis',2),a('opus',2),a('flac',2)],
   audioPassthrough:[pass('ac3',6),pass('eac3',8,{objectAudio:['atmos']})],
   transports:[
    {transport:'direct',containers:['mp4','m4v','mov','mkv','ts','m2ts','webm','avi'],video,audio},
    audioFiles(['aac','mp3','flac','vorbis','pcm'],['mp3','m4a','aac','flac','ogg','wav']),
    {transport:'hls_ts',containers:['mpegts'],video:['h264','hevc'],audio:['aac','mp3','ac3','eac3']},
    {transport:'hls_fmp4',containers:['fmp4'],video:['h264','hevc'],audio:['aac','ac3','eac3']},
   ],
   subtitleRendering:[...textOnly],
  }},
 };
}

/** Samsung Tizen: AVPlay. Close to webOS; Samsung sets have never decoded DTS
 * since 2018 and never Dolby Vision, and they are the platform that shows HDR10+. */
export function tizenDeclaredProfile(options:{osVersion?:string;model?:string}={}):CapabilityDocument{
 const d=webosDeclaredProfile(options);
 d.client=defined({family:'tizen',form:'tv' as const,platform:'tizen',os:'tizen',osVersion:options.osVersion,model:options.model});
 d.engines.native.name='avplay';
 return d;
}

/** Roku: the SceneGraph Video node. Every current model decodes H.264 at 1080p60;
 * the 4K models add HEVC 10-bit, VP9, HDR10/HLG and, on the models that report
 * it, Dolby Vision 5 (`dvhe.05`) and 9 (`dvav.09`). AV1 models carry HDR10+ on
 * AV1. DTS and Dolby go to the HDMI sink as passthrough. The channel widens this
 * from roDeviceInfo at run time (VIDEO.md VC-5b). */
export function rokuDeclaredProfile(options:{osVersion?:string;model?:string;uhd?:boolean;av1?:boolean;dolbyVision?:boolean}={}):CapabilityDocument{
 const uhd=options.uhd!==false;
 const width=uhd?3840:1920,height=uhd?2160:1080;
 const dolby=uhd&&options.dolbyVision===true;
 const av1=uhd&&options.av1===true;
 const hdr:DynamicRange[]=uhd?['sdr','hdr10','hlg',...(av1?['hdr10plus'] as DynamicRange[]:[])]:['sdr'];
 const ranges:DynamicRange[]=dolby?[...hdr,'dolby_vision']:hdr;
 const video=[v('h264',{bitDepths:dolby?[8,10]:[8],maxWidth:width,maxHeight:height,maxLevel:uhd?51:42,maxFrameRate:60,dynamicRanges:dolby?['sdr','dolby_vision']:['sdr'],dolbyVisionProfiles:dolby?[9]:[],codecTags:dolby?['avc1','dva1','dvav']:[]})];
 if(uhd){
  video.push(v('hevc',{bitDepths:[8,10],maxWidth:width,maxHeight:height,maxLevel:153,maxFrameRate:60,dynamicRanges:ranges,dolbyVisionProfiles:dolby?[5]:[],codecTags:dolby?['dvh1','dvhe','hev1','hvc1']:[]}));
  video.push(v('vp9',{bitDepths:[8,10],maxWidth:width,maxHeight:height,maxFrameRate:60,dynamicRanges:hdr.filter(r=>r!=='hdr10plus')}));
  if(av1)video.push(v('av1',{bitDepths:[8,10],maxWidth:width,maxHeight:height,maxFrameRate:60,dynamicRanges:hdr}));
 }
 const names=video.map(x=>x.codec);
 const audio=['aac','mp3','ac3','eac3','flac','pcm','alac','opus','vorbis','dts'];
 return {
  version:2,engineSetting:'automatic',
  client:defined({family:'roku',form:'tv' as const,platform:'roku',os:'roku',osVersion:options.osVersion,model:options.model,app:'portico-roku'}),
  display:{width,height,dynamicRanges:ranges,maxFrameRate:60},audioOutput:{route:'hdmi',maxChannels:2},
  engines:{native:{
   name:'roku-video',evidence:'declared',video,
   audio:[a('aac',6),a('mp3',2),a('flac',2),a('pcm',2),a('alac',2),a('opus',2),a('vorbis',2)],
   audioPassthrough:[pass('ac3',6),pass('eac3',8,{objectAudio:['atmos']}),pass('dts',8,{variants:['dts_core']})],
   transports:[
    {transport:'direct',containers:['mp4','m4v','mov','mkv','ts','m2ts'],video:names,audio},
    audioFiles(['aac','mp3','flac','pcm','alac','opus','vorbis'],['mp3','m4a','aac','flac','wav','ogg']),
    {transport:'hls_ts',containers:['mpegts'],video:names,audio:['aac','mp3','ac3','eac3']},
    {transport:'hls_fmp4',containers:['fmp4'],video:names,audio:['aac','ac3','eac3']},
   ],
   subtitleRendering:[...textOnly],
  }},
 };
}

/** A Google Cast receiver. Models differ enough that the table is per model:
 * the first three generations are 1080p H.264 devices; Ultra and Google TV add
 * HEVC, VP9 and HDR. Audio beyond stereo AAC is passed to the HDMI sink, which
 * only the receiver can see, so the receiver widens this at run time. The Google
 * TV Streamer is the exception the bar names (VIDEO.md §5): its HDMI output takes
 * multichannel PCM, so a lossless track keeps its channels there, beside E-AC-3
 * Atmos passthrough. */
export function castDeclaredProfile(model:'chromecast'|'chromecast_ultra'|'google_tv_4k'|'google_tv_hd'|'google_tv_streamer'|'nest_hub'|'unknown'):CapabilityDocument{
 const streamer=model==='google_tv_streamer';
 const uhd=streamer||model==='chromecast_ultra'||model==='google_tv_4k';
 const hevc=uhd||model==='google_tv_hd';
 const height=uhd?2160:model==='nest_hub'?720:1080,width=uhd?3840:model==='nest_hub'?1280:1920;
 const hdr:DynamicRange[]=uhd?['sdr','hdr10','hlg',...(model==='google_tv_4k'||streamer?['hdr10plus','dolby_vision'] as DynamicRange[]:[])]:['sdr'];
 const video=[v('h264',{bitDepths:[8],maxWidth:width,maxHeight:height,maxLevel:uhd?51:41,maxFrameRate:uhd?60:30,dynamicRanges:['sdr']}),v('vp8',{bitDepths:[8],maxWidth:width,maxHeight:height})];
 if(hevc)video.push(v('hevc',{bitDepths:[8,10],maxWidth:width,maxHeight:height,maxLevel:uhd?153:123,maxFrameRate:60,dynamicRanges:hdr,dolbyVisionProfiles:model==='google_tv_4k'||streamer?[5,8]:[]}));
 if(hevc||model==='nest_hub')video.push(v('vp9',{bitDepths:uhd?[8,10]:[8],maxWidth:width,maxHeight:height,dynamicRanges:hdr.filter(r=>r!=='dolby_vision'&&r!=='hdr10plus')}));
 if(model==='google_tv_hd'||streamer)video.push(v('av1',{bitDepths:[8,10],maxWidth:width,maxHeight:height,dynamicRanges:hdr.filter(r=>r!=='dolby_vision')}));
 const names=video.map(x=>x.codec);
 const audio=streamer?[a('aac',8),a('mp3',2),a('opus',2),a('vorbis',2),a('flac',8),a('pcm',8)]:[a('aac',2),a('mp3',2),a('opus',2),a('vorbis',2),a('flac',2)];
 return {
  version:2,engineSetting:'automatic',
  client:{family:'cast',form:model==='nest_hub'?'speaker':'cast',platform:'cast',model,app:'portico-cast'},
  display:{width,height,dynamicRanges:hdr,maxFrameRate:uhd?60:30},audioOutput:{route:model==='nest_hub'?'speaker':'hdmi',maxChannels:streamer?8:2},
  engines:{native:{
   name:'caf-shaka',evidence:'declared',video,audio,...(streamer?{audioPassthrough:[pass('ac3',6),pass('eac3',8,{objectAudio:['atmos']})]}:{}),
   transports:[
    {transport:'direct',containers:['mp4','m4v'],video:names.filter(x=>x!=='vp8'&&x!=='vp9'),audio:streamer?['aac','mp3','ac3','eac3']:['aac','mp3']},
    {transport:'direct',containers:['webm'],video:names.filter(x=>x==='vp8'||x==='vp9'||x==='av1'),audio:['opus','vorbis']},
    audioFiles(streamer?['aac','mp3','opus','vorbis','flac','pcm']:['aac','mp3','opus','vorbis','flac'],['mp3','m4a','aac','flac','ogg','wav']),
    {transport:'hls_ts',containers:['mpegts'],video:['h264'],audio:streamer?['aac','mp3','ac3','eac3']:['aac','mp3']},
    {transport:'hls_fmp4',containers:['fmp4'],video:names.filter(x=>x==='h264'||x==='hevc'||x==='av1'),audio:streamer?['aac','mp3','ac3','eac3','flac']:['aac','mp3']},
   ],
   subtitleRendering:[...textOnly],
  }},
 };
}

/* ------------------------------------------------------------------------- */
/* The mpv engine.                                                             */
/* ------------------------------------------------------------------------- */

/** What the app's mpv build reports about itself. Every field is optional. */
export type MpvFacts={
 version?:string;
 /** Decoders the build lacks (a trimmed FFmpeg): removed from the tables. */
 missingVideo?:string[];missingAudio?:string[];
 /** The build draws styled subtitles (libass) and image subtitles. Absent: it does. */
 libass?:boolean;imageSubtitles?:boolean;
 /** The build passes audio through to the receiver (audio-spdif). Absent: it does not. */
 passthrough?:boolean;
 /** The build shows Dolby Vision (gpu-next with the reshaping shader); the profiles it takes. */
 dolbyVisionProfiles?:number[];
 /** The tallest picture mpv decodes in software smoothly on this device. Absent: 1080. */
 softwareMaxHeight?:number;
};

/** The mpv engine for a device, built beside its native engine. mpv reads any
 * container, decodes almost any codec, switches audio tracks inside a file,
 * tone-maps HDR itself and draws text, styled and image subtitles itself. What
 * it cannot change is the device's hardware: heavy codecs (H.264, HEVC, VP9,
 * AV1) are held to the native engine's hardware-decoder limits where the native
 * engine has them, and to a software ceiling where it does not. */
export function mpvEngine(native:EngineCapabilities,facts:MpvFacts={}):EngineCapabilities{
 const softHeight=facts.softwareMaxHeight??1080,softWidth=Math.round(softHeight*16/9);
 const hard=new Map((native.video??[]).map(x=>[x.codec,x]));
 const heavy=(codec:string,tenBit:boolean,hdr:boolean):ClientVideoCodec=>{
  const hw=hard.get(codec);
  // Hardware decoding where the platform has it; mpv also takes 10-bit H.264 in software.
  const width=hw?.maxWidth??softWidth,height=hw?.maxHeight??softHeight;
  const entry:ClientVideoCodec={codec,bitDepths:tenBit?[8,10]:[8],maxWidth:width,maxHeight:height,maxFrameRate:hw?.maxFrameRate??60,dynamicRanges:hdr&&tenBit?['sdr','hdr10','hlg']:['sdr'],interlaced:true,evidence:hw?.evidence??'declared'};
  if(hw?.maxLevel)entry.maxLevel=hw.maxLevel;
  if(codec==='hevc'&&facts.dolbyVisionProfiles?.length){entry.dolbyVisionProfiles=[...facts.dolbyVisionProfiles];entry.dynamicRanges=[...entry.dynamicRanges!,'dolby_vision'];}
  return entry;
 };
 const hevcTen=hard.get('hevc')?.bitDepths?.includes(10)??false;
 const video:ClientVideoCodec[]=[
  heavy('h264',true,false),
  heavy('hevc',hevcTen||!hard.has('hevc'),true),
  heavy('vp9',hard.get('vp9')?.bitDepths?.includes(10)??false,true),
  heavy('av1',hard.get('av1')?.bitDepths?.includes(10)??false,true),
  ...['mpeg2video','mpeg4','vc1','vp8','wmv3','theora','msmpeg4v3'].map(codec=>v(codec,{bitDepths:[8],maxWidth:1920,maxHeight:1080,dynamicRanges:['sdr'],interlaced:true})),
 ].filter(x=>!(facts.missingVideo??[]).includes(x.codec));
 // Without a hardware decoder, H.264 10-bit and the heavy codecs stay within the software ceiling.
 for(const x of video)if(['h264','hevc','vp9','av1'].includes(x.codec)&&!hard.has(x.codec)){x.maxWidth=softWidth;x.maxHeight=softHeight;}
 const audio:ClientAudioCodec[]=[
  a('aac',8),a('mp3',2),a('mp2',2),a('ac3',6),a('eac3',8,{variants:['eac3','eac3_joc']}),a('dts',8,{variants:['dts_core','dts_hd_hra','dts_hd_ma','dtsx','dts_express']}),a('truehd',8,{variants:['truehd','truehd_atmos']}),a('flac',8),a('alac',8),a('opus',8),a('vorbis',8),a('pcm',8),a('wmav2',2),a('wmapro',8),
 ].filter(x=>!(facts.missingAudio??[]).includes(x.codec));
 const videoNames=video.map(x=>x.codec),audioNames=audio.map(x=>x.codec);
 const rendering:SubtitleRendering[]=['text'];
 if(facts.libass!==false)rendering.push('ass');
 if(facts.imageSubtitles!==false)rendering.push('pgs','vobsub','dvb');
 return defined({
  name:'mpv',version:facts.version,evidence:'declared' as const,video,audio,
  audioPassthrough:facts.passthrough===true?(native.audioPassthrough??[]).map(x=>({...x})):[],
  transports:[
   {transport:'direct' as const,containers:['mkv','mp4','m4v','mov','webm','avi','ts','m2ts','mpg','mpeg','wmv','asf','flv','ogv','3gp'],video:videoNames,audio:audioNames},
   audioFiles(audioNames,['mp3','m4a','m4b','aac','flac','ogg','opus','wav','aiff','wma','mka']),
   {transport:'hls_ts' as const,containers:['mpegts'],video:videoNames.filter(x=>['h264','hevc','mpeg2video'].includes(x)),audio:audioNames.filter(x=>['aac','mp3','mp2','ac3','eac3'].includes(x))},
   {transport:'hls_fmp4' as const,containers:['fmp4'],video:videoNames.filter(x=>['h264','hevc','av1','vp9'].includes(x)),audio:audioNames.filter(x=>['aac','mp3','ac3','eac3','flac','opus','alac'].includes(x))},
  ],
  subtitleRendering:rendering,toneMapsHdr:true,embeddedAudioSwitching:true,
 });
}

/** The document with an mpv engine added beside its native one. */
export function withMpvEngine(d:CapabilityDocument,facts:MpvFacts={}):CapabilityDocument{
 const next={...d,engines:{...d.engines,mpv:mpvEngine(d.engines.native,facts)}};
 return capabilityDocumentProblem(next)?d:next;
}

/* ------------------------------------------------------------------------- */
/* Browser probe.                                                              */
/* ------------------------------------------------------------------------- */

export type DecodingAnswer={supported:boolean;smooth:boolean;powerEfficient:boolean};
export type VideoDecodingQuestion={contentType:string;width:number;height:number;bitrate:number;framerate:number;transferFunction?:'srgb'|'pq'|'hlg';colorGamut?:'srgb'|'p3'|'rec2020';hdrMetadataType?:'smpteSt2086'|'smpteSt2094-10'|'smpteSt2094-40'};

/** Everything the browser probe asks, as an interface, so it runs under test
 * without a browser and so a webview-based client can answer differently. */
export type WebProbeEnvironment={
 /** `HTMLMediaElement.canPlayType`. */
 canPlayType(type:string):string;
 /** `MediaSource.isTypeSupported` (or ManagedMediaSource); null when there is no MSE at all. */
 mediaSourceSupports:((type:string)=>boolean)|null;
 /** `navigator.mediaCapabilities.decodingInfo` for a media-source video configuration. */
 decodingInfo?:(question:VideoDecodingQuestion,type?:'file'|'media-source')=>Promise<DecodingAnswer>;
 /** `matchMedia(query).matches`. */
 matchMedia?:(query:string)=>boolean;
 /** `screen.width/height` multiplied by `devicePixelRatio`. */
 screen?:{width:number;height:number};
 /** `AudioContext.destination.maxChannelCount`. */
 audioOutputChannels?:number;
 /** `'audioTracks' in HTMLMediaElement.prototype`. */
 embeddedAudioTracks?:boolean;
 /** The engine tone-maps HDR well on a standard display (Safari; Chrome on macOS). COMPAT-05. */
 tonemapsHdr?:boolean;
 identity?:ProbeIdentity;
};

const mp4=(codecs:string)=>`video/mp4; codecs="${codecs}"`;
const mp4Audio=(codecs:string)=>`audio/mp4; codecs="${codecs}"`;

/** `uhd` (and `uhdTenBit`) name a level that covers 3840×2160: a decoder that answers by level refuses 4K at the plain probe's level 4.0. */
type VideoProbe={codec:string;contentType:string;tenBit?:string;uhd:string;uhdTenBit?:string;dolbyVision?:Record<number,string>;hdr?:boolean};
const videoProbes:VideoProbe[]=[
 {codec:'h264',contentType:mp4('avc1.640028'),tenBit:mp4('avc1.6e0028'),uhd:mp4('avc1.640033'),uhdTenBit:mp4('avc1.6e0033')},
 {codec:'hevc',contentType:mp4('hvc1.1.6.L120.90'),tenBit:mp4('hvc1.2.4.L153.B0'),uhd:mp4('hvc1.1.6.L153.90'),dolbyVision:{5:mp4('dvh1.05.06'),8:mp4('dvh1.08.06')},hdr:true},
 {codec:'av1',contentType:mp4('av01.0.08M.08'),tenBit:mp4('av01.0.12M.10.0.110.09.16.09.0'),uhd:mp4('av01.0.12M.08'),hdr:true},
 {codec:'vp9',contentType:'video/webm; codecs="vp09.00.40.08"',tenBit:'video/webm; codecs="vp09.02.40.10"',uhd:'video/webm; codecs="vp09.00.51.08"',uhdTenBit:'video/webm; codecs="vp09.02.51.10"',hdr:true},
];
const audioProbes:{codec:string;mse:string[];element:string[];mp4?:string[];channels:number;objectAudio?:('atmos')[]}[]=[
 {codec:'aac',mse:[mp4Audio('mp4a.40.2')],element:[mp4Audio('mp4a.40.2')],channels:6},
 {codec:'mp3',mse:['audio/mpeg',mp4Audio('mp3')],element:['audio/mpeg'],channels:2},
 {codec:'ac3',mse:[mp4Audio('ac-3')],element:[mp4Audio('ac-3')],channels:6},
 {codec:'eac3',mse:[mp4Audio('ec-3')],element:[mp4Audio('ec-3')],channels:8},
 {codec:'flac',mse:[mp4Audio('flac'),mp4Audio('fLaC')],element:['audio/flac'],mp4:[mp4Audio('flac'),mp4Audio('fLaC')],channels:6},
 {codec:'opus',mse:[mp4Audio('opus'),'audio/webm; codecs="opus"'],element:['audio/ogg; codecs="opus"','audio/webm; codecs="opus"'],mp4:[mp4Audio('opus')],channels:6},
 {codec:'vorbis',mse:['audio/webm; codecs="vorbis"'],element:['audio/ogg; codecs="vorbis"'],channels:6},
 {codec:'alac',mse:[mp4Audio('alac')],element:[mp4Audio('alac')],channels:6},
 {codec:'pcm',mse:[],element:['audio/wav; codecs="1"'],channels:2},
];

const plays=(answer:string)=>answer==='probably'||answer==='maybe';

/** Asks the browser what it can actually play. Every question is guarded: a
 * browser that throws, hangs or lacks an API simply contributes nothing, and the
 * result is never narrower than the declared floor for what the floor covers. */
export async function probeWebProfile(env:WebProbeEnvironment):Promise<CapabilityDocument>{
 const document=webDeclaredProfile(env.identity);
 const mse=env.mediaSourceSupports;
 const safe=<T>(f:()=>T,fallback:T):T=>{try{return f();}catch{return fallback;}};
 const element=(type:string)=>safe(()=>plays(env.canPlayType(type)),false);
 const source=(type:string)=>mse?safe(()=>mse(type),false):false;
 const ask=async(q:VideoDecodingQuestion,type:'file'|'media-source'='media-source'):Promise<DecodingAnswer|null>=>{
  if(!env.decodingInfo)return null;
  let timer:ReturnType<typeof setTimeout>|undefined;
  try{return await Promise.race([env.decodingInfo(q,type),new Promise<null>(resolve=>{timer=setTimeout(()=>resolve(null),1500);})]);}catch{return null;}
  finally{clearTimeout(timer);}
 };
 const hdrDisplay=safe(()=>env.matchMedia?.('(dynamic-range: high)')??false,false);
 const nativeHls=element('application/vnd.apple.mpegurl');
 const toneMaps=env.tonemapsHdr===true;

 const video:ClientVideoCodec[]=[];
 const direct:string[]=[],fragmented:string[]=[],webm:string[]=[];
 let h264Denied=false;
 for(const probe of videoProbes){
  // HEVC support is a runtime fact on every OS/browser, including Windows. Sample entries
  // are distinct: a positive hev1 answer never grants hvc1 (or the other way round).
  const types=probe.codec==='hevc'?[probe.contentType,probe.contentType.replace('hvc1.','hev1.')]:[probe.contentType];
  const inElement=types.some(element),inSource=types.some(source);
  if(!inElement&&!inSource)continue;
  const entry:ClientVideoCodec={codec:probe.codec,bitDepths:[8],dynamicRanges:['sdr'],evidence:'probed',chromaSubsampling:['4:2:0']};
  if(probe.codec==='hevc')entry.codecTags=types.filter(t=>element(t)||source(t)).map(t=>t.includes('hev1.')?'hev1':'hvc1');
  const tag=probe.codec==='hevc'&&!entry.codecTags!.includes('hvc1')?'hev1':'hvc1';
  const typed=(type:string)=>probe.codec==='hevc'?type.replace('hvc1.',tag+'.'):type;
  const ten=probe.tenBit&&typed(probe.tenBit);
  if(ten&&(element(ten)||source(ten)))entry.bitDepths=[8,10];
  const transport=inSource?'media-source' as const:'file' as const;
  const limits:ClientVideoLimit[]=[];
  let answered=false;
  for(const depth of entry.bitDepths!){
   const base=depth===10?ten!:typed(probe.contentType);
   const uhd=typed(depth===10?probe.uhdTenBit??probe.tenBit!:probe.uhd);
   // Codec level strings must cover the operating point being asked about. No screen-size cap:
   // a small window can still send 8K to another display, but only a smooth runtime answer grants it.
   const eight=probe.codec==='hevc'?uhd.replace(/L\d+/, 'L186'):probe.codec==='av1'?uhd.replace(/av01\.0\.\d+M/, 'av01.0.17M'):probe.codec==='vp9'?uhd.replace(/vp09\.(\d+)\.\d+/, 'vp09.$1.62'):uhd.replace(/0028|0033/, '003e');
   const points=[[7680,4320,60,eight],[7680,4320,30,eight],[3840,2160,60,uhd],[3840,2160,30,uhd],[1920,1080,60,base],[1920,1080,30,base],[1280,720,30,base]] as const;
   for(const [width,height,framerate,contentType] of points){
    if(limits.some(l=>l.bitDepths?.includes(depth)&&(l.maxWidth??0)>=width&&(l.maxHeight??0)>=height&&(l.maxFrameRate??0)>=framerate))continue;
    const answer=await ask({contentType,width,height,framerate,bitrate:Math.round(width*height*framerate*0.12)},transport);
    answered ||= answer!==null;
    if(answer?.supported&&answer.smooth)limits.push({bitDepths:[depth],maxWidth:width,maxHeight:height,maxFrameRate:framerate});
   }
  }
  if(!limits.length&&answered){if(probe.codec==='h264')h264Denied=true;continue;}
  if(limits.length){
   entry.bitDepths=[...new Set(limits.flatMap(l=>l.bitDepths!))];
   entry.maxWidth=Math.max(...limits.map(l=>l.maxWidth!));entry.maxHeight=Math.max(...limits.map(l=>l.maxHeight!));entry.maxFrameRate=Math.max(...limits.map(l=>l.maxFrameRate!));entry.limits=limits;
  }else{entry.maxWidth=1920;entry.maxHeight=1080;entry.maxFrameRate=30;}
  if(probe.hdr&&entry.bitDepths!.includes(10)&&probe.tenBit){
   // What the decoder decodes is stated here; whether the result is shown well is the display's
   // list and the engine's `toneMapsHdr`. A browser that neither has an HDR display nor is known
   // to tone-map well (Firefox, some Windows GPUs) can still say "supported and smooth" for a
   // picture it shows flat and grey, so there the range is not claimed and the server converts.
   const pq=await ask({contentType:ten!,width:entry.maxWidth!>=3840?3840:1920,height:entry.maxHeight!>=2160?2160:1080,bitrate:25_000_000,framerate:30,transferFunction:'pq',colorGamut:'rec2020',hdrMetadataType:'smpteSt2086'},transport);
   if(pq?.supported&&pq.smooth&&(hdrDisplay||toneMaps))entry.dynamicRanges=['sdr','hdr10'];
   const hlg=await ask({contentType:ten!,width:1920,height:1080,bitrate:15_000_000,framerate:30,transferFunction:'hlg',colorGamut:'rec2020'},transport);
   if(hlg?.supported&&hlg.smooth&&entry.dynamicRanges!.includes('hdr10'))entry.dynamicRanges=[...entry.dynamicRanges!,'hlg'];
  }
  if(probe.dolbyVision&&hdrDisplay){
   const profiles:number[]=[];
   for(const [profile,type] of Object.entries(probe.dolbyVision)){
    if(!element(type)&&!source(type))continue;
    const report=await ask({contentType:type,width:1920,height:1080,framerate:30,bitrate:15_000_000,transferFunction:'pq',colorGamut:'rec2020'},source(type)?'media-source':'file');
    // A MIME answer alone may mean only the HDR10 base layer. Require a matching runtime report.
    if(report?.supported&&report.smooth)profiles.push(Number(profile));
   }
   if(profiles.length){entry.codecTags=[...new Set([...(entry.codecTags??[]),'dvh1'])];entry.dolbyVisionProfiles=profiles;entry.dynamicRanges=[...new Set([...entry.dynamicRanges!,'dolby_vision' as DynamicRange])];}
  }
  video.push(entry);
  if(probe.codec==='vp9'){if(inElement)webm.push('vp9');}
  else{if(inElement)direct.push(probe.codec);if(inSource||nativeHls&&(probe.codec==='h264'||probe.codec==='hevc'))fragmented.push(probe.codec);}
  if(probe.codec==='av1'&&element('video/webm; codecs="av01.0.08M.08"'))webm.push('av1');
 }
 const floor=document.engines.native;
 if(!h264Denied&&!video.some(x=>x.codec==='h264'))video.unshift(floor.video![0]!);

 const outputChannels=Number.isSafeInteger(env.audioOutputChannels)&&env.audioOutputChannels!>0?Math.min(8,env.audioOutputChannels!):2;
 const audio:ClientAudioCodec[]=[],directAudio:string[]=[],mp4DirectAudio:string[]=[],sourceAudio:string[]=[],tsAudio:string[]=[];
 for(const probe of audioProbes){
  const inElement=probe.element.some(element),inSource=probe.mse.some(source);
  // Codec support in Ogg, WebM or a standalone FLAC file says nothing about
  // MP4 sample entries. Direct files and MSE each need their own MP4 answer.
  const inMp4Element=probe.mp4?probe.mp4.some(element):inElement;
  const inMp4Source=probe.mp4?probe.mp4.some(source):inSource;
  if(!inElement&&!inSource&&!inMp4Element&&!(probe.codec==='aac'||probe.codec==='mp3'))continue;
  audio.push({codec:probe.codec,maxChannels:probe.channels,evidence:inElement||inSource||inMp4Element?'probed':'declared',...(probe.objectAudio&&(inElement||inSource)?{objectAudio:probe.objectAudio}:{})});
  if(inElement||probe.codec==='aac'||probe.codec==='mp3')directAudio.push(probe.codec);
  if((inMp4Element||probe.codec==='aac'||probe.codec==='mp3')&&['aac','mp3','ac3','eac3','flac','opus','alac'].includes(probe.codec))mp4DirectAudio.push(probe.codec);
  if(inMp4Source)sourceAudio.push(probe.codec);
  // hls.js demuxes AAC, MP3 and (where the browser decodes them) AC-3 and E-AC-3 from MPEG-TS.
  if(['aac','mp3'].includes(probe.codec)||['ac3','eac3'].includes(probe.codec)&&(inSource||nativeHls&&inElement))tsAudio.push(probe.codec);
 }

 const transports:ClientTransport[]=[{transport:'direct',containers:['mp4','m4v','mov'],video:direct.length?direct:['h264'],audio:mp4DirectAudio}];
 if(webm.length)transports.push({transport:'direct',containers:['webm'],video:webm,audio:directAudio.filter(x=>x==='opus'||x==='vorbis')});
 const fileContainers:Record<string,string[]>={mp3:['mp3'],aac:['m4a','m4b','aac'],flac:['flac'],opus:['opus','ogg'],vorbis:['ogg'],pcm:['wav'],alac:['m4a']};
 const containers=[...new Set(directAudio.flatMap(x=>fileContainers[x]??[]))];
 transports.push(audioFiles(directAudio.filter(x=>fileContainers[x]),containers));
 if(mse||nativeHls){
  transports.push({transport:'hls_ts',containers:['mpegts'],video:['h264'],audio:tsAudio});
  const fmp4Audio=nativeHls&&!mse?mp4DirectAudio.filter(x=>x!=='opus'):sourceAudio.filter(x=>x!=='vorbis'&&x!=='pcm');
  transports.push({transport:'hls_fmp4',containers:['fmp4'],video:fragmented.length?fragmented:['h264'],audio:fmp4Audio.length?fmp4Audio:['aac']});
 }
 const probed:CapabilityDocument={
  ...document,
  display:{...(env.screen?{width:Math.round(env.screen.width),height:Math.round(env.screen.height)}:{}),dynamicRanges:hdrDisplay?['sdr','hdr10','hlg',...(video.some(v=>v.dynamicRanges?.includes('dolby_vision'))?['dolby_vision' as const]:[])]:['sdr']},
  audioOutput:{maxChannels:outputChannels},
  engines:{native:{...floor,evidence:'probed',video,audio,transports,toneMapsHdr:toneMaps,embeddedAudioSwitching:env.embeddedAudioTracks===true}},
 };
 // A probe must never produce a document the server refuses: the declared floor
 // is always valid, and is what playback falls back to.
 return capabilityDocumentProblem(probed)?document:probed;
}

/* ------------------------------------------------------------------------- */
/* Native probes.                                                              */
/* ------------------------------------------------------------------------- */

/** What the Apple native module reports. Every field is optional: an older
 * native build, or a simulator, simply reports less. */
export type AppleNativeFacts={
 model?:string;osVersion?:string;
 /** `VTIsHardwareDecodeSupported` per codec. */
 hardwareDecode?:{hevc?:boolean;av1?:boolean;dolbyVision?:boolean};
 /** `AVPlayer.availableHDRModes`, and whether the display is currently eligible. */
 hdrModes?:{hdr10?:boolean;hlg?:boolean;dolbyVision?:boolean;hdr10Plus?:boolean};
 /** Public AVURLAsset MIME eligibility, tested per exact sample entry. */
 videoEligibility?:{dolbyVision5?:boolean;dolbyVision81?:boolean;dolbyVision84?:boolean;hdr10Plus?:boolean};
 eligibleForHDRPlayback?:boolean;
 screen?:{width:number;height:number;maxFrameRate?:number};
 /** `AVAudioSession.maximumOutputNumberOfChannels`, the current route's port type, and spatial audio. */
 audio?:{maxOutputChannels?:number;route?:string;spatial?:boolean;currentOutputSampleRate?:number;outputNumberOfChannels?:number;maximumOutputNumberOfChannels?:number;routeUID?:string;eac3JocEligible?:boolean};
};

/** AVAudioSession port types folded to the route names the server knows. */
function appleRoute(port:string):string{
 const p=port.toLowerCase().replace(/[^a-z0-9]/g,'');
 if(p.includes('hdmi'))return 'hdmi';
 if(p.includes('airplay'))return 'airplay';
 if(p.includes('bluetooth'))return 'bluetooth';
 if(p.includes('headphone')||p.includes('headset'))return 'headphones';
 if(p.includes('speaker')||p.includes('receiver'))return 'speaker';
 if(p.includes('usb'))return 'usb';
 return p.slice(0,32);
}

export function appleProfile(options:{television:boolean;osVersion?:string;model?:string;appVersion?:string;phone?:boolean},facts:AppleNativeFacts|null):CapabilityDocument{
 const declared=appleDeclaredProfile({...options,model:facts?.model??options.model,osVersion:facts?.osVersion??options.osVersion});
 if(!facts)return declared;
 const document:CapabilityDocument=JSON.parse(JSON.stringify(declared));
 const engine=document.engines.native;
 engine.evidence='mixed';
 const hevc=engine.video!.find(x=>x.codec==='hevc')!;
 if(facts.hardwareDecode?.hevc===false){
  // Software HEVC on an old device stutters at 4K; keep it to 1080p.
  hevc.maxWidth=1920;hevc.maxHeight=1080;hevc.evidence='probed';
 }else if(facts.hardwareDecode?.hevc)hevc.evidence='probed';
 // HDR10+ needs an exact native eligibility fact as well as hardware HEVC;
 // an HDR10 display or a model name alone cannot grant dynamic metadata.
 const hdr10Plus=facts.hardwareDecode?.hevc===true&&facts.videoEligibility?.hdr10Plus===true;
 // What the screen shows right now.
 const display:DynamicRange[]=['sdr'];
 if(facts.eligibleForHDRPlayback!==false){
  if(facts.hdrModes?.hdr10)display.push('hdr10');
  if(hdr10Plus&&facts.hdrModes?.hdr10Plus===true)display.push('hdr10plus');
  if(facts.hdrModes?.hlg)display.push('hlg');
  if(facts.hdrModes?.dolbyVision)display.push('dolby_vision');
 }
 // What the device *decodes* is separate from what the screen *shows*. With hardware HEVC Main10,
 // AVPlayer decodes HDR10 and HLG and tone-maps them for an SDR screen itself, so the server must
 // not convert those titles: the decoder lists the ranges, the display lists what it shows, and
 // `toneMapsHdr` says the engine bridges the two.
 const hardwareHevc=facts.hardwareDecode?.hevc===true;
 engine.toneMapsHdr=hardwareHevc;
 hevc.dynamicRanges=hardwareHevc?[...new Set<DynamicRange>(['sdr','hdr10','hlg',...(hdr10Plus?['hdr10plus' as const]:[]),...display])]:[...display];
 // Exact AVURLAsset MIME eligibility describes parser support, separately
 // from available display modes and the actual applied-output observations.
 const dvProfiles:number[]=[];
 if(facts.videoEligibility?.dolbyVision5===true)dvProfiles.push(5);
 const dv8Compatibility:(1|2|4)[]=[];
 if(facts.videoEligibility?.dolbyVision81===true)dv8Compatibility.push(1);
 if(facts.videoEligibility?.dolbyVision84===true)dv8Compatibility.push(4);
 if(dv8Compatibility.length)dvProfiles.push(8);
 hevc.dolbyVisionProfiles=dvProfiles;
 if(dv8Compatibility.length)hevc.dolbyVisionProfile8Compatibility=dv8Compatibility;
 if(dvProfiles.length){if(!hevc.dynamicRanges.includes('dolby_vision'))hevc.dynamicRanges=[...hevc.dynamicRanges,'dolby_vision'];}
 else hevc.dynamicRanges=hevc.dynamicRanges.filter(x=>x!=='dolby_vision');
 // The Apple TV HD (AppleTV5,x) and devices without hardware HEVC decode H.264 up to 1080p only;
 // the declared 4K floor would send them files they stutter on.
 const h264=engine.video!.find(x=>x.codec==='h264');
 if(h264&&(/^AppleTV5,/.test(facts.model??'')||facts.hardwareDecode?.hevc===false)){h264.maxWidth=1920;h264.maxHeight=1080;h264.evidence='probed';}
 if(facts.hardwareDecode?.av1){
  engine.video!.push({codec:'av1',profiles:['main'],chromaSubsampling:['4:2:0'],bitDepths:[8,10],maxWidth:4096,maxHeight:2304,maxFrameRate:60,dynamicRanges:hardwareHevc?['sdr','hdr10','hlg']:display.filter(x=>x!=='dolby_vision'&&x!=='hdr10plus'),evidence:'probed'});
  for(const t of engine.transports!)if((t.transport==='direct'&&t.video?.length)||t.transport==='hls_fmp4')t.video=[...t.video!,'av1'];
 }
 document.display={...(facts.screen?{width:Math.round(facts.screen.width),height:Math.round(facts.screen.height),maxFrameRate:facts.screen.maxFrameRate??60}:{}),dynamicRanges:display};
 const channels=[facts.audio?.maximumOutputNumberOfChannels,facts.audio?.maxOutputChannels,facts.audio?.outputNumberOfChannels].find(n=>typeof n==='number'&&Number.isInteger(n)&&n>=1&&n<=16)??2;
 const route=appleRoute(facts.audio?.route??'');
 document.audioOutput={maxChannels:channels,...(route&&token.test(route)?{route}:{}),spatial:facts.audio?.spatial===true};
 const eac3=engine.audio!.find(a=>a.codec==='eac3');
 const spatialRoute=['hdmi','airplay','bluetooth','headphones','speaker','usb'].includes(route)&&facts.audio?.spatial===true;
 if(eac3)eac3.objectAudio=facts.audio?.eac3JocEligible===true&&spatialRoute?['atmos']:undefined;
 return capabilityDocumentProblem(document)?declared:document;
}

/** What an Android native module reports from `MediaCodecList`, `Display` and
 * `AudioManager`. Kept here so the Android clients share one merge with tests. */
export type AndroidNativeFacts={
 model?:string;osVersion?:string;
 decoders?:{codec:string;hardware?:boolean;maxWidth?:number;maxHeight?:number;maxFrameRate?:number;maxBitrateBps?:number;profiles?:string[];maxLevel?:number;tenBit?:boolean;hdr?:DynamicRange[];dolbyVisionProfiles?:number[];
  /** The decoder's operating points (`getSupportedFrameRatesFor` at the common picture sizes), when they differ. */
  limits?:{maxWidth:number;maxHeight:number;maxFrameRate:number}[]}[];
 /** `maxChannels` absent or 0 means the decoder states no limit. */
 audioDecoders?:{codec:string;maxChannels?:number}[];
 /** `Display.getHdrCapabilities().getSupportedHdrTypes()`. */
 displayHdr?:DynamicRange[];
 screen?:{width:number;height:number;maxFrameRate?:number};
 /** `AudioManager.getDevices(GET_DEVICES_OUTPUTS)` encodings of the *active* sink: what the receiver takes as a bitstream right now. */
 passthrough?:{codec:string;maxChannels?:number;objectAudio?:('atmos'|'dtsx')[];variants?:AudioVariant[]}[];
 /** The active output: hdmi, arc, earc, optical, speaker, headphones, bluetooth, usb. */
 audio?:{maxOutputChannels?:number;route?:string};
 /** The app's own renderers beyond text (a real ASS or PGS renderer in the app). */
 subtitleRendering?:SubtitleRendering[];
};

export function androidProfile(options:{family?:'android'|'androidtv'|'firetv';television?:boolean;osVersion?:string;model?:string;tablet?:boolean},facts:AndroidNativeFacts|null):CapabilityDocument{
 const declared=androidDeclaredProfile({...options,model:facts?.model??options.model,osVersion:facts?.osVersion??options.osVersion});
 if(!facts?.decoders?.length)return declared;
 const base=declared.engines.native;
 const display:DynamicRange[]=['sdr',...(facts.displayHdr??[]).filter(x=>x!=='sdr')];
 // A decoder lists what it decodes. ExoPlayer does not tone-map for an SDR panel, so a range the
 // display cannot show is simply not presentable: the planner reads that from `display`.
 // A codec with only a software decoder states that decoder's limits, which say what it can
 // decode, not what it can decode in real time: it is held to 1080p.
 const soft=(d:{hardware?:boolean},value:number|undefined,cap:number)=>d.hardware===false?Math.min(value??cap,cap):value;
 const video:ClientVideoCodec[]=facts.decoders.map(d=>defined({codec:d.codec,profiles:d.profiles,maxLevel:d.maxLevel,bitDepths:d.tenBit?[8,10]:[8],maxWidth:soft(d,d.maxWidth,1920),maxHeight:soft(d,d.maxHeight,1080),maxFrameRate:d.maxFrameRate,maxBitrateBps:d.maxBitrateBps,dynamicRanges:['sdr' as DynamicRange,...(d.hdr??[]).filter(x=>x!=='sdr')],dolbyVisionProfiles:d.dolbyVisionProfiles??[],
  limits:d.limits?.length?d.limits.slice(0,16).map(l=>({maxWidth:soft(d,l.maxWidth,1920),maxHeight:soft(d,l.maxHeight,1080),maxFrameRate:l.maxFrameRate})):undefined,evidence:'probed' as const}));
 const decoded=new Map((facts.audioDecoders??[]).map(x=>[x.codec,x]));
 // A decoder that states no channel limit keeps the declared one, or eight: it is not stereo.
 const channelsOf=(codec:string,fallback:number)=>decoded.get(codec)?.maxChannels||fallback;
 const audio:ClientAudioCodec[]=(base.audio??[]).filter(x=>!facts.audioDecoders||decoded.has(x.codec)).map(x=>({...x,maxChannels:channelsOf(x.codec,x.maxChannels??8),evidence:decoded.has(x.codec)?'probed' as const:'declared' as const}));
 for(const d of facts.audioDecoders??[])if(!audio.some(x=>x.codec===d.codec))audio.push({codec:d.codec,maxChannels:d.maxChannels||8,evidence:'probed'});
 // Passthrough is beside decoding, never instead of it: a codec the device also decodes stays decodable.
 const audioPassthrough:ClientAudioPassthrough[]=(facts.passthrough??[]).map(p=>defined({codec:p.codec,maxChannels:p.maxChannels??8,objectAudio:p.objectAudio,variants:p.variants,evidence:'probed' as const}));
 const videoNames=video.map(x=>x.codec),audioNames=[...new Set([...audio.map(x=>x.codec),...audioPassthrough.map(x=>x.codec)])];
 const transports=(base.transports??[]).map(t=>t.transport==='direct'&&t.video?.length?{...t,video:videoNames,audio:audioNames}:{...t,video:(t.video??[]).filter(x=>videoNames.includes(x)),audio:(t.audio??[]).filter(x=>audioNames.includes(x))});
 const passthroughChannels=Math.max(0,...audioPassthrough.map(x=>x.maxChannels??8));
 const route=(facts.audio?.route??'').toLowerCase();
 const rendering=[...new Set<SubtitleRendering>(['text',...(facts.subtitleRendering??[]).filter(x=>(subtitleRenderingKinds as readonly string[]).includes(x))])];
 const document:CapabilityDocument={
  ...declared,
  display:{...(facts.screen?defined({width:Math.round(facts.screen.width),height:Math.round(facts.screen.height),maxFrameRate:facts.screen.maxFrameRate}):{}),dynamicRanges:display},
  audioOutput:{maxChannels:Math.max(2,Math.min(16,Math.max(facts.audio?.maxOutputChannels??2,passthroughChannels))),...(route&&token.test(route)?{route}:{})},
  engines:{native:{...base,evidence:'mixed',video,audio,audioPassthrough,transports,subtitleRendering:rendering}},
 };
 return capabilityDocumentProblem(document)?declared:document;
}

/** Engine failure classes a player reports with `PATCH /v1/playback/sessions/{id}` `failure`
 * (or a timeline `error`). They are recorded, not interpreted: any of them closes the way the
 * file was being delivered, for this file on this device and this engine. */
export type PlayerFailureCode='decode_error'|'source_not_supported'|'audio_decode_error'|'manifest_incompatible'|'stalled_without_data'|'engine_error';
