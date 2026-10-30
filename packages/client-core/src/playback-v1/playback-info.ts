/** Committed source/delivery facts. Renderer, receiver and screen observations
 * live in Session.observedPlayback and are never inferred from this recipe. */
export type PlaybackRoute = 'original' | 'repackaged' | 'converted';
export type PlaybackPictureFacts = Readonly<{
  codec:string; container?:string; profile?:string; bitDepth?:number; width?:number; height?:number;
  frameRate?:number; frameRateNumerator?:number; frameRateDenominator?:number; frameRateMode?:string;
  interlaced?:boolean; chromaSubsampling?:string; dynamicRange?:string;
  dolbyVisionProfile?:number; dolbyVisionCompatibility?:number; dolbyVisionEnhancementType?:string;
  dolbyVisionCmVersion?:string; dolbyVisionRpu?:boolean; dolbyVisionBaseLayer?:boolean; hdr10Plus?:boolean; masterDisplay?:string; maxCll?:string;
  conversion?:Readonly<{kind:'profile7_to_81';sourceDolbyVisionProfile:number;sourceEnhancementType?:string;enhancementDiscarded:boolean;felResidualDiscarded:boolean}>;
}>;
export type PlaybackSoundFacts = Readonly<{
  codec:string;profile?:string;variant?:string;objectAudio?:string;channels?:number;layout?:string;sampleRate?:number;bitDepth?:number;lossless?:boolean;
  /** The committed delivered track's route, never a device-output observation. */
  delivery?:'passthrough'|'decoded';
}>;
export type PlaybackSubtitleFacts = Readonly<{format?:string;language?:string;title?:string;origin?:string;rendering?:'drawnByDevice'|'externalText'|'burnedIn'|'off'}>;
export type PlaybackMediaFacts = Readonly<{video?:PlaybackPictureFacts;audio?:PlaybackSoundFacts;subtitles?:PlaybackSubtitleFacts}>;
export type PlaybackReplacement = Readonly<{dynamicRange?:string;audioCodec?:string;channels?:number;layout?:string}>;
export type PlaybackInfo = Readonly<{
  route:PlaybackRoute; routes:Readonly<{picture?:PlaybackRoute;sound?:PlaybackRoute;subtitles?:PlaybackRoute}>;
  reason:Readonly<{kind:'compatible'|'container'|'format'|'quality'|'subtitles';videoCodec?:string;audioCodec?:string;container?:string;maxHeight?:number;maxBitrateKbps?:number}>;
  source:PlaybackMediaFacts;delivered:PlaybackMediaFacts;
  losses:readonly Readonly<{feature:'dolbyVision'|'hdr10plus'|'hdr'|'atmos'|'dtsx';replacement:PlaybackReplacement}>[];
  network:'home'|'away'|'unknown';estimatedBitrateKbps?:number;basis:'committed_delivery_plan';
}>;

type RecordValue = Record<string,unknown>;
function isRecord(v:unknown):v is RecordValue { return !!v&&typeof v==='object'&&!Array.isArray(v); }
function record(v:unknown):RecordValue { if(!isRecord(v))throw new Error('invalid playback comparison');return v; }
function text(v:unknown):string { if(typeof v!=='string'||v.length>500||/[\u0000-\u001f\u007f]/u.test(v))throw new Error('invalid comparison text');return v; }
function choice<T extends string>(v:unknown,values:readonly T[]):T { const found=values.find(value=>value===v);if(found===undefined)throw new Error('invalid comparison choice');return found; }
function strings(v:RecordValue,keys:readonly string[]):Record<string,string> { const out:Record<string,string>={};for(const key of keys)if(v[key]!==undefined)out[key]=text(v[key]);return out; }
function numbers(v:RecordValue,limits:Readonly<Record<string,number>>,fractional=false):Record<string,number> {
  const out:Record<string,number>={};for(const [key,max] of Object.entries(limits)){const value=v[key];if(value===undefined)continue;if(typeof value!=='number'||!Number.isFinite(value)||value<0||value>max||!fractional&&!Number.isSafeInteger(value))throw new Error('invalid comparison number');out[key]=value;}return out;
}
function booleans(v:RecordValue,keys:readonly string[]):Record<string,boolean> { const out:Record<string,boolean>={};for(const key of keys)if(v[key]!==undefined){if(typeof v[key]!=='boolean')throw new Error('invalid comparison boolean');out[key]=v[key];}return out; }
const routeValues=['original','repackaged','converted'] as const;
function picture(value:unknown):PlaybackPictureFacts {
  const v=record(value);let conversion:PlaybackPictureFacts['conversion'];
  if(v.conversion!==undefined){const c=record(v.conversion);const n=numbers(c,{sourceDolbyVisionProfile:255});if(n.sourceDolbyVisionProfile===undefined||typeof c.enhancementDiscarded!=='boolean'||typeof c.felResidualDiscarded!=='boolean')throw new Error('invalid picture conversion');conversion=Object.freeze({kind:choice(c.kind,['profile7_to_81']),sourceDolbyVisionProfile:n.sourceDolbyVisionProfile,...strings(c,['sourceEnhancementType']),enhancementDiscarded:c.enhancementDiscarded,felResidualDiscarded:c.felResidualDiscarded});}
  return Object.freeze({codec:text(v.codec),...strings(v,['container','profile','frameRateMode','chromaSubsampling','dynamicRange','dolbyVisionEnhancementType','dolbyVisionCmVersion','masterDisplay','maxCll']),...numbers(v,{bitDepth:32,width:32768,height:32768,frameRateNumerator:2147483647,frameRateDenominator:2147483647,dolbyVisionProfile:255,dolbyVisionCompatibility:15}),...numbers(v,{frameRate:1000},true),...booleans(v,['interlaced','hdr10Plus','dolbyVisionRpu','dolbyVisionBaseLayer']),...(conversion?{conversion}:{})});
}
function sound(value:unknown):PlaybackSoundFacts { const v=record(value);return Object.freeze({codec:text(v.codec),...strings(v,['profile','variant','objectAudio','layout']),...numbers(v,{channels:128,sampleRate:1536000,bitDepth:64}),...booleans(v,['lossless']),...(v.delivery!==undefined?{delivery:choice(v.delivery,['passthrough','decoded'])}:{})}); }
function subtitle(value:unknown):PlaybackSubtitleFacts { const v=record(value);return Object.freeze({...strings(v,['format','language','title','origin']),...(v.rendering!==undefined?{rendering:choice(v.rendering,['drawnByDevice','externalText','burnedIn','off'])}:{})}); }
function media(value:unknown):PlaybackMediaFacts { const v=record(value);return Object.freeze({...(v.video!==undefined?{video:picture(v.video)}:{}),...(v.audio!==undefined?{audio:sound(v.audio)}:{}),...(v.subtitles!==undefined?{subtitles:subtitle(v.subtitles)}:{})}); }

/** Strict, bounded, immutable and allowlisted; malformed optional reports may
 * be omitted by parseSession without invalidating the session's authority. */
export function parsePlaybackInfo(value:unknown):PlaybackInfo|undefined {
  if(value===undefined)return undefined;
  const v=record(value),r=record(v.routes),reason=record(v.reason);
  const routes=Object.freeze({...(r.picture!==undefined?{picture:choice(r.picture,routeValues)}:{}),...(r.sound!==undefined?{sound:choice(r.sound,routeValues)}:{}),...(r.subtitles!==undefined?{subtitles:choice(r.subtitles,routeValues)}:{})});
  const route=choice(v.route,routeValues),known=Object.values(routes);
  const strongest=known.includes('converted')?'converted':known.includes('repackaged')?'repackaged':known.includes('original')?'original':undefined;
  if(route!==strongest)throw new Error('inconsistent comparison routes');
  if(!Array.isArray(v.losses)||v.losses.length>8)throw new Error('invalid comparison losses');
  const losses=Object.freeze(v.losses.map(value=>{const loss=record(value),replacement=record(loss.replacement);return Object.freeze({feature:choice(loss.feature,['dolbyVision','hdr10plus','hdr','atmos','dtsx']),replacement:Object.freeze({...strings(replacement,['dynamicRange','audioCodec','layout']),...numbers(replacement,{channels:128})})});}));
  return Object.freeze({route,routes,reason:Object.freeze({kind:choice(reason.kind,['compatible','container','format','quality','subtitles']),...strings(reason,['videoCodec','audioCodec','container']),...numbers(reason,{maxHeight:32768,maxBitrateKbps:1000000})}),source:media(v.source),delivered:media(v.delivered),losses,network:choice(v.network,['home','away','unknown']),...numbers(v,{estimatedBitrateKbps:1000000}),basis:choice(v.basis,['committed_delivery_plan'])});
}
