import type {WebPlaybackRuntimeSnapshot,WebShellPlaybackRuntime} from '../../platform/playback';
import type {QualityChoice} from '@core/playback-v1/choices.ts';
import {audioStreamIndex,fixedQuality,isQualityChoice,qualityChoiceOf} from '@core/playback-v1/choices.ts';
import {videoTrackName,videoTrackWords} from '@core/video-track-labels.ts';
import {subtitleReason} from '@core/subtitles.ts';
import {sameFence,type QueuePlaybackFence} from '../../../../packages/player-core/src/index.ts';
import type {I18n} from '@i18n';
import type {SubtitlePreferences} from '@core/preference-effects.ts';
import type {TrackChoice} from '../../kit/media';
import {lossSentences} from './loss-copy';

export type WatchRadioChoice=Readonly<{id:string;label:string;detail?:string;selected:boolean;enabled:boolean}>;
export type WatchPanelModel=Readonly<{
 fence:QueuePlaybackFence;audio:readonly TrackChoice[];subtitles:readonly TrackChoice[];subtitleAdditions:readonly TrackChoice[];
 quality:readonly WatchRadioChoice[];speed:readonly WatchRadioChoice[];
 qualityLabel?:string;qualityAccessibleLabel?:string;qualityNotes:readonly string[];audioNotes:readonly string[];/** The one route line the retained panel binding still reads. */qualityNote?:string;automatic:boolean;audioLabel?:string;subtitleLabel?:string;
 subtitleTiming?:Readonly<{offsetSeconds:number;shift(deltaMs:number):Promise<void>}>;
 /** Draws the search row only when the server says why it cannot be used. */
 subtitleSearch?:Readonly<{reason:string}>;
 /** The style this playback draws with; a change lasts until it stops. */
 subtitleStyle?:Readonly<{size:SubtitlePreferences['size'];background:SubtitlePreferences['background'];set(patch:Readonly<{size?:SubtitlePreferences['size'];background?:SubtitlePreferences['background']}>):void}>;
 sleepTimer?:Readonly<{minutes:number;set(minutes:number):void}>;
 /** The image track's own mixed forced lines; absent when it has none. */
 subtitleForced?:Readonly<{only:boolean;set(on:boolean):void}>;
 isCurrent():boolean;selectAudio(id:string):Promise<void>;selectSubtitle(id:string):Promise<void>;
 selectQuality(id:string):Promise<void>;selectSpeed(id:string):void;
}>;

function heightLabel(height:number,t:I18n['t']):string {
 return height===2160?t('quality.4k'):t('quality.height',{height});
}
/** Inert projection of the actual selected grouped version and the current source's offers.
 * No labels are parsed, no default track is treated as selected, and no options are invented. */
export function watchPanelModel(runtime:WebShellPlaybackRuntime,snapshot:WebPlaybackRuntimeSnapshot,i18n:I18n):WatchPanelModel|null {
 const fence=snapshot.fence,session=snapshot.session,offers=snapshot.offers,options=snapshot.options,current=offers?.current;
 if(snapshot.disposed||!fence||fence.kind!=='queue'||!session||!offers||!current||!options
  ||session.kind!=='vod'||session.id!==fence.sessionId||session.itemId!==fence.itemId||session.queue?.entryId!==fence.entryId
  ||session.presentation.generation!==fence.generation||options.itemId!==fence.itemId||offers.scope.itemId!==fence.itemId
  ||offers.scope.serverId!==snapshot.authority.serverId
  ||current.sessionId!==fence.sessionId||current.generation!==fence.generation||current.sourceId!==fence.sourceId)return null;
 const version=options.versions.find(v=>v.id===session.versionId),source=offers.sources.find(s=>s.id===fence.sourceId);
 if(!version||!source||!source.available)return null;
 // OffersService owns its detail viewer fence. The Home authority fence intentionally
 // has a different scope and must never be compared with that opaque token.
 const words=videoTrackWords(i18n.t,i18n.locale),t=i18n.t;
 const isCurrent=()=>{const live=runtime.getSnapshot();return !live.disposed&&live.authority===snapshot.authority&&sameFence(live.fence,fence)&&live.session?.versionId===version.id&&live.session.revision===session.revision&&live.options===options&&live.offers===offers;};
 const check=()=>{if(!isCurrent())throw new DOMException('Playback source retired','AbortError');};
 const audio=Object.freeze(version.audio.map(track=>{
  const name=videoTrackName(track,words),enabled=source.streams.some(s=>s.type==='audio'&&s.enabled&&s.index===audioStreamIndex(track.id));
  return Object.freeze({id:track.id,primary:name.first,secondary:name.rest||undefined,fullName:name.label,selected:session.presentation.audioTrackId===track.id,unavailable:!enabled});
 }));
 const plan=offers.subtitlePlan,matched=!!plan&&plan.sessionId===fence.sessionId&&plan.generation===fence.generation&&plan.sourceId===fence.sourceId;
 const subtitleIds=new Map<string,string|null>();const subtitleRows:TrackChoice[]=[],subtitleAdditions:TrackChoice[]=[];
 if(matched){
  subtitleIds.set('off',null);subtitleRows.push({id:'off',primary:t('player.opt.off'),fullName:t('player.opt.off'),selected:plan.mode==='off',unavailable:!plan.offAvailable});
  for(const resource of plan.resources.filter(r=>r.sourceId===fence.sourceId&&!r.retired)){
   const facts={...resource,sdh:resource.hearingImpaired,source:resource.origin==='embedded'?'embedded' as const:resource.origin==='sidecar'?'sidecar' as const:'downloaded' as const};
   const name=videoTrackName(facts,words);
   // desktop-player f3: a track from outside the file names where it came from on its second line.
   const origin=resource.origin==='provider'||resource.origin==='upload'?t('player.opt.origin',{origin:resource.origin}):undefined;
   const secondary=[name.rest,origin].filter(Boolean).join(', ')||undefined;
   const id='resource:'+resource.id;subtitleIds.set(id,resource.id);
   const selected=plan.mode==='track'&&plan.selected?.id===resource.id, unavailable=!resource.enabled||!Number.isSafeInteger(Number(resource.offsetUs)/1000);
   // f3b: a track the file carries but the session has not taken stays under the list with Add.
   if(resource.discoveryId!==undefined&&!selected)subtitleAdditions.push({id,primary:name.first,secondary,fullName:name.label,selected:false,
    addable:!unavailable,unavailable,...(unavailable?{reason:subtitleReason(resource.reason,t)}:{})});
   else subtitleRows.push({id,primary:name.first,secondary,fullName:name.label,selected,unavailable});
  }
 }
 const selected=qualityChoiceOf(session.presentation.quality),qualityRows:WatchRadioChoice[]=[],qualityIds=new Map<string,QualityChoice>();
 // Requested mode chooses the radio; only an actual bound rung names the chip.
 const actualId=session.presentation.quality.id??current.qualityId,actualStep=fixedQuality(actualId);
 const qualityLabel=actualStep?heightLabel(actualStep.height,t):actualId==='original'?t('next.kit.quality.original'):undefined;
 const action=session.presentation.decision?.video?.action;
 // desktop-player f3: each quality footer fact keeps its own line, the network limit then the route.
 const limit=offers.deliveryPolicy&&offers.deliveryPolicy.maxVideoHeight>0?heightLabel(offers.deliveryPolicy.maxVideoHeight,t):undefined;
 const networkNote=limit?(offers.deliveryPolicy!.networkClass==='local'?t('next.kit.quality.home',{quality:limit})
  :offers.deliveryPolicy!.networkClass==='remote'?t('next.kit.quality.away',{quality:limit}):undefined):undefined;
 const routeNote=action==='transcode'?(qualityLabel?t('player.quality.converting',{quality:qualityLabel}):undefined):action?t('player.quality.playingOriginal'):undefined;
 // f3b: while the owner has conversion off, only the original can play and the footer says why.
 const conversionOff=offers.transcodingEnabled===false;
 const qualityNotes=Object.freeze([networkNote,routeNote,...(conversionOff?[t('player.quality.conversionOff')]:[])].filter((note):note is string=>!!note));
 // f3b: a version with one audio track says so in place of a choice. Frame 7: the
 // audio footer also says what this device does with the chosen track when something
 // is lost, in the notice's own words.
 const audioLoss=session.playbackInfo?lossSentences(i18n,session.playbackInfo,session.observedPlayback,'sound'):undefined;
 const audioNotes=Object.freeze([...(version.audio.length===1?[t('player.opt.oneAudioTrack')]:[]),
  ...(audioLoss?[`${audioLoss.loss} ${audioLoss.replacement}`.trim()]:[])]);
 for(const offer of source.qualities){
  const choice=offer.kind==='automatic'?'automatic':offer.kind==='original'?'original':isQualityChoice(offer.id)?offer.id:undefined;
  if(!choice)continue;qualityIds.set(offer.id,choice);
  const step=fixedQuality(choice),label=choice==='automatic'?t('next.kit.quality.automatic'):choice==='original'?t('next.kit.quality.original'):step?heightLabel(step.height,t):undefined;
  if(!label)continue;
  const enabled=offer.enabled&&options.qualities.some(option=>option.applicable!==false&&qualityChoiceOf(option)===choice);
  // f3b: with conversion off every converted step says who turned it off, not a bit rate.
  const detail=conversionOff&&choice!=='original'?t('player.ownerOff'):choice==='automatic'&&qualityLabel?t('next.kit.quality.adjusts',{quality:qualityLabel}):choice==='original'?t('next.kit.quality.originalDetail'):
   typeof offer.maxVideoBitrateBps==='number'?t('web.player.quality.rate',{mbps:offer.maxVideoBitrateBps/1e6}):undefined;
  qualityRows.push({id:offer.id,label,detail,selected:selected===choice,enabled});
 }
 // These ports are capabilities: a host that predates one simply has no such control, so a
 // stand-in runtime without the method reads as absent instead of being called into a throw.
 const readStyle=typeof runtime.subtitleStyle==='function'?runtime.subtitleStyle.bind(runtime):undefined;
 const readSleep=typeof runtime.sleepTimer==='function'?runtime.sleepTimer.bind(runtime):undefined;
 const writeSleep=typeof runtime.setSleepTimer==='function'?runtime.setSleepTimer.bind(runtime):undefined;
 const writeStyle=typeof runtime.setSubtitleStyle==='function'?runtime.setSubtitleStyle.bind(runtime):undefined;
 const writeTiming=typeof runtime.setSubtitleOffset==='function'?runtime.setSubtitleOffset.bind(runtime):undefined;
 const readForced=typeof runtime.subtitleForced==='function'?runtime.subtitleForced.bind(runtime):undefined;
 const writeForced=typeof runtime.setSubtitleForcedOnly==='function'?runtime.setSubtitleForcedOnly.bind(runtime):undefined;
 const timingPlan=matched?plan:null;
 // The plan's own signed correction is exact to the millisecond; a value the replan wire
 // cannot carry is no control at all, never a silent zero or a rounded clock.
 const timingBase=timingPlan&&timingPlan.mode==='track'&&timingPlan.selected?.enabled?(()=>{const offsetUs=Number(timingPlan.offsetUs);return Number.isSafeInteger(offsetUs)?offsetUs/1000:undefined;})():undefined;
 const subtitleTiming=timingBase!==undefined&&writeTiming&&Number.isSafeInteger(timingBase)&&Math.abs(timingBase)<=600000?Object.freeze({offsetSeconds:timingBase/1000,
  shift:async(deltaMs:number)=>{check();if(!Number.isSafeInteger(deltaMs)||deltaMs===0)throw new Error('playback_subtitle_unavailable');
   const next=timingBase+deltaMs;
   if(!Number.isSafeInteger(next)||Math.abs(next)>600000)throw new Error('playback_subtitle_unavailable');
   await writeTiming(fence,next);}}):undefined;
 const readProvider=typeof runtime.subtitleProvider==='function'?runtime.subtitleProvider.bind(runtime):undefined;
 // desktop-player f3: a search that cannot run stays visible with the server's own reason.
 const style=matched?readStyle?.(fence):undefined;
 const sleep=matched?readSleep?.(fence):undefined;
 const sleepTimer=matched&&writeSleep?Object.freeze({minutes:sleep?.minutes??0,set:(minutes:number)=>{check();writeSleep(fence,minutes);}}):undefined;
 const subtitleStyle=style&&writeStyle?Object.freeze({...style,set:(patch:Readonly<{size?:SubtitlePreferences['size'];background?:SubtitlePreferences['background']}>)=>{check();writeStyle(fence,patch);}}):undefined;
 const forced=matched?readForced?.(fence):undefined;
 const subtitleForced=forced&&writeForced?Object.freeze({only:forced.only,set:(on:boolean)=>{check();writeForced(fence,on);}}):undefined;
 const provider=matched?readProvider?.(fence):undefined;
 const subtitleSearch=provider&&!provider.enabled?Object.freeze({reason:subtitleReason(provider.reason,t)}):undefined;
 let speeds:readonly number[]=[];try{speeds=runtime.speedChoices(fence);}catch{/* source changed */}
 const speed=Object.freeze(speeds.map(rate=>Object.freeze({id:String(rate),label:rate===1?t('player.opt.normalSpeed'):t('next.dock.rate',{rate}),selected:snapshot.presentation?.rate===rate,enabled:true})));
 return Object.freeze({fence,audio,subtitles:Object.freeze(subtitleRows),subtitleAdditions:Object.freeze(subtitleAdditions),quality:Object.freeze(qualityRows),speed,automatic:selected==='automatic',qualityLabel,qualityNotes,audioNotes,qualityNote:routeNote,...(subtitleTiming?{subtitleTiming}:{}),...(subtitleSearch?{subtitleSearch}:{}),...(subtitleStyle?{subtitleStyle}:{}),...(sleepTimer?{sleepTimer}:{}),...(subtitleForced?{subtitleForced}:{}),
  qualityAccessibleLabel:qualityLabel?(selected==='automatic'?t('player.quality.automaticNow',{quality:qualityLabel}):qualityLabel):undefined,
  audioLabel:audio.find(r=>r.selected)?.primary,subtitleLabel:subtitleRows.find(r=>r.selected)?.primary,isCurrent,
  selectAudio:async id=>{check();if(!audio.some(r=>r.id===id&&!r.unavailable))throw new Error('playback_audio_unavailable');await runtime.selectAudio(fence,id);},
  selectSubtitle:async id=>{check();if(![...subtitleRows,...subtitleAdditions].some(r=>r.id===id&&!r.unavailable)||!subtitleIds.has(id))throw new Error('playback_subtitle_unavailable');await runtime.selectSubtitle(fence,subtitleIds.get(id)!);},
  selectQuality:async id=>{check();const choice=qualityIds.get(id);if(!choice||!qualityRows.some(r=>r.id===id&&r.enabled))throw new Error('playback_quality_unavailable');await runtime.setQuality(fence,choice);},
  selectSpeed:id=>{check();const row=speed.find(r=>r.id===id);if(!row)throw new Error('playback_speed_unavailable');runtime.setSpeed(fence,Number(row.id));},
 });
}

export type PhoneOptionRow=Readonly<{id:'subtitles'|'audio'|'quality'|'speed';label:string;value?:string;detail?:string}>;
/** The drawn phone row list: one row per real control, carrying its current choice only. */
export function phoneOptionRows(model:WatchPanelModel,t:I18n['t']):readonly PhoneOptionRow[]{
 return [
  ...(model.subtitles.length?[{id:'subtitles' as const,label:t('player.opt.subtitles'),value:model.subtitleLabel??t('player.opt.off')}]:[]),
  ...(model.audio.length?[{id:'audio' as const,label:t('player.opt.audio'),...(model.audioLabel?{value:model.audioLabel}:{})}]:[]),
  ...(model.quality.length?[{id:'quality' as const,label:t('player.opt.quality'),value:model.automatic?t('next.kit.quality.automatic'):model.qualityLabel,...(model.automatic&&model.qualityLabel?{detail:model.qualityLabel}:{})}]:[]),
  ...(model.speed.length?[{id:'speed' as const,label:t('player.opt.speed'),value:model.speed.find(row=>row.selected)?.label}]:[]),
 ];
}
