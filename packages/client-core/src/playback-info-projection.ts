import type {PlaybackInfo,PlaybackMediaFacts,PlaybackPictureFacts,PlaybackRoute,PlaybackSubtitleFacts} from './playback-v1/playback-info.ts';
import type {ObservedPlayback} from './playback-v1/observed-playback.ts';
import {videoAudioFormatName,videoAudioChannelName,videoPictureName,videoTrackName,type VideoTrackWords} from './video-track-labels.ts';

export type PlaybackInfoField = 'container'|'codec'|'hdr'|'bitDepth'|'resolution'|'frameRate'|'screen'|'format'|'channels'|'sampleRate'|'output'|'track'|'shown';
export type PlaybackInfoRow = Readonly<{field:PlaybackInfoField;source?:string;delivered?:string;changed:boolean}>;
export type PlaybackComparisonProjection = Readonly<{
 route:PlaybackRoute;explanation?:string;networkAndBitrate?:string;
 sections:readonly Readonly<{kind:'picture'|'sound'|'subtitles';route:PlaybackRoute;rows:readonly PlaybackInfoRow[];losses:readonly string[]}>[];
}>;
/** Translation ports contain frame copy. Values passed to them are facts, not
 * raw diagnostics or a fallback English sentence. No port supplies policy. */
export type PlaybackInfoWords = Readonly<{
 tracks:VideoTrackWords;locale?:string;standardRange:string;
 hls(container:string):string;
 bitDepth(bits:number):string;
 frameRate(value:string):string;
 sampleRate(khz:string):string;
 dolbyVision(profile?:string,enhancement?:string):string;
 convertedFromDolbyVision(delivered:string,profile:number,enhancement?:string):string;
 reason(reason:PlaybackInfo['reason']):string|undefined;
 loss(loss:PlaybackInfo['losses'][number]):string;
 network(network:PlaybackInfo['network'],measuredKbps?:number):string|undefined;
 screen(observed:NonNullable<ObservedPlayback['screen']>):string|undefined;
 output(observed:NonNullable<ObservedPlayback['audio']>):string|undefined;
 shown(rendering:NonNullable<ObservedPlayback['subtitles']>['rendering']):string;
}>;
const VIDEO:Readonly<Record<string,string>>={h264:'H.264',hevc:'HEVC',av1:'AV1',vp9:'VP9',vp8:'VP8',vc1:'VC-1',mpeg2video:'MPEG-2',mpeg1video:'MPEG-1',mpeg4:'MPEG-4',prores:'ProRes'};
const CONTAINERS:Readonly<Record<string,string>>={mp4:'MP4',mov:'MOV',matroska:'MKV',mkv:'MKV',webm:'WebM',mpegts:'MPEG-TS',mpeg:'MPEG-PS',avi:'AVI'};
const proper=(value:string|undefined)=>value?.trim()||undefined;
function container(value:string|undefined,words:PlaybackInfoWords):string|undefined {
 if(!value)return undefined;if(value==='fmp4_hls')return words.hls('MP4');if(value==='mpegts_hls')return words.hls('MPEG-TS');return CONTAINERS[value]??proper(value);
}
function hdr(v:PlaybackPictureFacts|undefined,words:PlaybackInfoWords):string|undefined {
 if(!v?.dynamicRange)return undefined;
 let name:string|undefined;
 switch(v.dynamicRange){
 case 'dolby_vision':{
  let profile=v.dolbyVisionProfile?String(v.dolbyVisionProfile):undefined;
  if(v.dolbyVisionProfile===8&&[1,2,4].includes(v.dolbyVisionCompatibility??0))profile=`8.${v.dolbyVisionCompatibility}`;
  name=words.dolbyVision(profile,proper(v.dolbyVisionEnhancementType));break;
 }
 case 'hdr10':name=v.hdr10Plus===true?'HDR10+':'HDR10';break;
 case 'hlg':name='HLG';break;
 case 'sdr':name=words.standardRange;break;
 }
 if(name&&v.conversion)name=words.convertedFromDolbyVision(name,v.conversion.sourceDolbyVisionProfile,proper(v.conversion.sourceEnhancementType));
 return name;
}
function subtitleName(value:PlaybackSubtitleFacts|undefined,words:PlaybackInfoWords):string|undefined {
 if(!value?.format)return undefined;
 const source=value.origin==='embedded'?'embedded':value.origin==='sidecar'?'sidecar':value.origin==='provider'?'downloaded':value.origin==='upload'?'other':undefined;
 return proper(videoTrackName({format:value.format,source,language:value.language,title:value.title},words.tracks).label);
}
function rows(source:Partial<Record<PlaybackInfoField,string|undefined>>,delivered:Partial<Record<PlaybackInfoField,string|undefined>>,fields:readonly PlaybackInfoField[]):readonly PlaybackInfoRow[] {
 return Object.freeze(fields.flatMap(field=>{
  const a=proper(source[field]),b=proper(delivered[field]);if(a===undefined&&b===undefined)return [];
  return [Object.freeze({field,...(a!==undefined?{source:a}:{}),...(b!==undefined?{delivered:b}:{}),changed:a!==undefined&&b!==undefined&&a!==b})];
 }));
}

/** One pure headless projection for every platform. No identity/URL, quality
 * ceiling, receiver capability, output rate or screen mode is synthesized. */
/** The losses this occurrence actually has: object-audio losses the planner assumed are dropped
 * when the same occurrence's output observation proves a copied bitstream kept its objects.
 * Every other loss stays as the server reported it. Notices and the comparison both use this. */
export function reconciledPlaybackLosses(info:PlaybackInfo,observed:ObservedPlayback|undefined):PlaybackInfo['losses']{
 if(!info.routes.sound)return info.losses;
 const source=info.source.audio,target=info.delivered.audio;
 const copied=info.routes.sound!=='converted'&&source?.codec===target?.codec;
 const sameFormat=(!source?.profile||!target?.profile||source.profile===target.profile)&&(!source?.variant||!target?.variant||source.variant===target.variant);
 return info.losses.filter(loss=>{
  if(loss.feature!=='atmos'&&loss.feature!=='dtsx')return true;
  // A same-occurrence output observation can disprove the planner's decode
  // assumption for a copied bitstream. A converted track still lost its objects.
  // Generic DTS/DTS-HD output does not prove that its DTS:X objects survived.
  const passthrough=loss.feature==='atmos'&&observed?.audio?.handling==='passthrough'&&observed.audio.codec===target?.codec&&sameFormat;
  return !(copied&&source?.objectAudio===loss.feature&&(observed?.audio?.objectAudio===loss.feature||passthrough));
 });
}
export function playbackInfoProjection(info:PlaybackInfo,observed:ObservedPlayback|undefined,words:PlaybackInfoWords):PlaybackComparisonProjection {
 const number=new Intl.NumberFormat(words.locale,{maximumFractionDigits:3});
 const picture=(v:PlaybackPictureFacts|undefined):Partial<Record<PlaybackInfoField,string|undefined>>=>{
  if(!v)return {};
  const rate=v.frameRateNumerator&&v.frameRateDenominator?v.frameRateNumerator/v.frameRateDenominator:v.frameRate;
  return {container:container(v.container,words),codec:proper([VIDEO[v.codec]??proper(v.codec),proper(v.profile)].filter(Boolean).join(' ')),hdr:hdr(v,words),bitDepth:v.bitDepth?words.bitDepth(v.bitDepth):undefined,resolution:proper(videoPictureName({width:v.width,height:v.height,interlaced:v.interlaced},words.tracks)),frameRate:rate?words.frameRate(number.format(rate)):undefined};
 };
 const sound=(v:PlaybackMediaFacts['audio']):Partial<Record<PlaybackInfoField,string|undefined>>=>v?{
  format:proper(videoAudioFormatName({codec:v.codec,profile:v.profile,variant:v.variant,objectAudio:v.objectAudio},words.tracks)),channels:videoAudioChannelName(v,words.tracks),sampleRate:v.sampleRate?words.sampleRate(number.format(v.sampleRate/1000)):undefined,
 }:{};
 const sections:PlaybackComparisonProjection['sections'][number][]=[];
 if(info.routes.picture){
  const delivered=picture(info.delivered.video);
  // A screen mode is a separate actual observation, never a source HDR label.
  if(observed?.screen)delivered.screen=words.screen(observed.screen);
  sections.push(Object.freeze({kind:'picture',route:info.routes.picture,rows:rows(picture(info.source.video),delivered,['container','codec','hdr','bitDepth','resolution','frameRate','screen']),losses:Object.freeze(info.losses.filter(loss=>loss.feature==='dolbyVision'||loss.feature==='hdr10plus'||loss.feature==='hdr').map(loss=>words.loss(loss)))}));
 }
 if(info.routes.sound){
  const delivered=sound(info.delivered.audio);
  if(observed?.audio)delivered.output=words.output(observed.audio);
  const losses=reconciledPlaybackLosses(info,observed).filter(loss=>loss.feature==='atmos'||loss.feature==='dtsx');
  sections.push(Object.freeze({kind:'sound',route:info.routes.sound,rows:rows(sound(info.source.audio),delivered,['format','channels','sampleRate','output']),losses:Object.freeze(losses.map(loss=>words.loss(loss)))}));
 }
 if(info.routes.subtitles){
  sections.push(Object.freeze({kind:'subtitles',route:info.routes.subtitles,rows:rows({track:subtitleName(info.source.subtitles,words)},{track:subtitleName(info.delivered.subtitles,words),shown:observed?.subtitles?words.shown(observed.subtitles.rendering):undefined},['track','shown']),losses:Object.freeze([])}));
 }
 const explanation=proper(words.reason(info.reason));
 // Only the actual renderer's delivered rate is labelled as delivered. A
 // partial/video-only or quality-mode estimate never fills this line.
 const networkAndBitrate=proper(words.network(info.network,observed?.deliveredBitrateKbps||undefined));
 return Object.freeze({route:info.route,...(explanation?{explanation}:{}),...(networkAndBitrate?{networkAndBitrate}:{}),sections:Object.freeze(sections)});
}
