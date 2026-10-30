import type {PlaybackSession} from './index.ts';
import type {AudioRenderV2} from './playback-v1/audio-render.ts';

/** Render-only settings. No controller, lease, item list or observation writer. */
export type AudioEffectsSettings=Readonly<{gapless:boolean;crossfadeSeconds:number;normalization:'off'|'track'|'album';neverBoost?:boolean;smoothTransitions?:boolean;sampleRateMatching?:'smart'|'strict'|'off'}>;
/** Before the server's music preferences are known (and when a device has none cached): the
 * registry's own defaults, so gapless is on (X-02). */
export const defaultAudioEffects:AudioEffectsSettings=Object.freeze({gapless:true,crossfadeSeconds:0,normalization:'off',neverBoost:false,smoothTransitions:true});
/** Every effect off: what a viewer who turned them all off gets. */
export const noAudioEffects:AudioEffectsSettings=Object.freeze({gapless:false,crossfadeSeconds:0,normalization:'off'});
/** A prepared next track (§18.2): the version 2 on-device-decode plan (`audio`, §18.1)
 * that v1 queues prepare. */
export type PreparedAudio=Readonly<{token:string;expiresAtMs:number;itemId?:string;audio?:AudioRenderV2}>;
/** Only an unrequested pause is a listening notice. Engine recovery codes are diagnostic. */
export type ListeningNoticeCode='audio_autoplay_blocked'|'listening_policy_failed';
export const listeningNoticeKey=(code:ListeningNoticeCode):'web.player.pressPlay'|'listening.paused'=>code==='audio_autoplay_blocked'?'web.player.pressPlay':'listening.paused';
export type AudioProcessingBinding=Readonly<{intentId:number;sessionId:string;generation:number}>;
export type AudioProcessingApplied=Readonly<{eqApplied:boolean;levelingApplied:boolean; /** Measured gain the renderer actually applied; only meaningful with levelingApplied. */ levelingGainDb?:number}>;
export type AudioEffectsSnapshot=Readonly<{
 settings:AudioEffectsSettings;rendering:boolean;rate:number;eqApplied:boolean;levelingApplied:boolean; /** Applied leveling gain in dB, absent when unknown or not applied. */ levelingGainDb?:number;effectiveRate?:number;waiting?:boolean;
 prepared?:PreparedAudio;committed?:Readonly<{token:string;itemId:string;session:PlaybackSession}>;
}>;
export function parseAudioEffects(v:any):AudioEffectsSettings {
 if(!v||typeof v.gapless!=='boolean'||!Number.isFinite(v.crossfadeSeconds)||v.crossfadeSeconds<0||v.crossfadeSeconds>12||!['off','track','album'].includes(v.normalization)||Object.keys(v).some(k=>!['gapless','crossfadeSeconds','normalization','neverBoost','smoothTransitions','sampleRateMatching'].includes(k)))throw new Error('Invalid audio processing settings.');
 if(v.neverBoost!==undefined&&typeof v.neverBoost!=='boolean'||v.smoothTransitions!==undefined&&typeof v.smoothTransitions!=='boolean'||v.sampleRateMatching!==undefined&&!['smart','strict','off'].includes(v.sampleRateMatching))throw new Error('Invalid music processing settings.');
 return Object.freeze({...(v.neverBoost!==undefined?{neverBoost:v.neverBoost}:{}),...(v.smoothTransitions!==undefined?{smoothTransitions:v.smoothTransitions}:{}),...(v.sampleRateMatching!==undefined?{sampleRateMatching:v.sampleRateMatching}:{}),gapless:v.gapless,crossfadeSeconds:v.crossfadeSeconds,normalization:v.normalization});
}
export const audioEffectsEnabled=(s:AudioEffectsSettings)=>s.gapless||s.crossfadeSeconds>0||s.normalization!=='off';
export const audioTransitionsEnabled=(s:AudioEffectsSettings)=>s.gapless||s.crossfadeSeconds>0;
export function transitionTiming(now:number,end:number,seconds:number,rate:number){
 if(![now,end,seconds,rate].every(Number.isFinite)||seconds<0||seconds>12||rate<.25||rate>4)throw new Error('Invalid audio transition timing.');
 // Scheduling uses the engine clock. A late network result must not cut samples
 // from an already-playing incoming item or pretend a missed edge was gapless.
 const start=Math.max(now+.03,end-seconds),duration=Math.max(0,Math.min(seconds,end-start));
 return {start,duration,late:start>end};
}

/** Loudness-shaped overlap, never between tracks on the same album. Values
 * are server-measured momentary LUFS, not genre/metadata guesses. */
export function smoothTransitionSeconds(current:AudioRenderV2|undefined,next:AudioRenderV2|undefined,maxSeconds:number,sameAlbum:boolean):number{
 if(sameAlbum)return 0;if(!Number.isFinite(maxSeconds)||maxSeconds<0||maxSeconds>12)throw new Error('Invalid transition duration.');
 const tail=current?.loudnessEnvelope?.endLufs,head=next?.loudnessEnvelope?.startLufs;if(!tail?.length||!head?.length)return maxSeconds;
 const mean=(v:readonly number[])=>10*Math.log10(v.reduce((s,n)=>s+Math.pow(10,n/10),0)/v.length);
 const contrast=Math.abs(mean(tail)-mean(head));return Math.min(maxSeconds,Math.max(.25,maxSeconds/(1+contrast/12)));
}
