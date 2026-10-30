import {invoke} from '@tauri-apps/api/core';
import {parseObservedPlayback,type ObservedPlayback} from '../../../packages/client-core/src/playback-v1/observed-playback.ts';

export type NativeObservation = Readonly<{observedPlayback?:Pick<ObservedPlayback,'audio'>;generation: number; state: 'idle' | 'loading' | 'playing' | 'paused' | 'buffering' | 'ended' | 'error'; positionMs: number; rate: number; volume: number; muted: boolean; droppedFrames: number; nativeSurfaceActive: boolean;eqApplied:boolean;levelingApplied:boolean}>;
export type SurfaceBounds = Readonly<{x: number; y: number; width: number; height: number}>;
export type NativeCommand = {action: string; generation?: number; source?: {url: string; generation: number; mode: string; startPositionMs: number; autoplay: boolean}; milliseconds?: number; direction?:-1|1; rate?: number; volume?: number; muted?: boolean;gain?:number};
export interface Bridge {command(value: NativeCommand): Promise<void>; observe(): Promise<NativeObservation>; surface?(bounds: SurfaceBounds, generation: number): Promise<void>}
export function observation(value: unknown): NativeObservation {
  if (!value || typeof value !== 'object') throw new Error('engine_failure');
  const x = value as Record<string, unknown>;
  if (!Number.isSafeInteger(x.generation) || Number(x.generation) < 0 || !['idle','loading','playing','paused','buffering','ended','error'].includes(String(x.state)) ||
      !['positionMs','rate','volume','droppedFrames'].every(key => typeof x[key] === 'number' && Number.isFinite(x[key]) && Number(x[key]) >= 0) || !['muted','nativeSurfaceActive','eqApplied','levelingApplied'].every(key=>typeof x[key]==='boolean')) throw new Error('engine_failure');
  // Explicit projection: no native fields can accidentally become UI/session telemetry.
  let output:Pick<ObservedPlayback,'audio'>={};
  try{
    const audio=parseObservedPlayback(x.observedPlayback)?.audio;
    if(audio?.handling==='pcm'&&(audio.sampleRate!==undefined||audio.channels!==undefined))output={audio:{handling:'pcm',
      ...(audio.sampleRate!==undefined?{sampleRate:audio.sampleRate}:{}),...(audio.channels!==undefined?{channels:audio.channels}:{})}};
  }catch{/* Invalid optional output never poisons playback or retains old facts. */}
  return {observedPlayback:Object.freeze(output),generation: Number(x.generation), state: x.state as NativeObservation['state'], positionMs: Number(x.positionMs), rate: Number(x.rate), volume: Number(x.volume), muted: x.muted as boolean, droppedFrames: Number(x.droppedFrames), nativeSurfaceActive: x.nativeSurfaceActive as boolean,eqApplied:x.eqApplied as boolean,levelingApplied:x.levelingApplied as boolean};
}
export const nativeBridge: Bridge = {
  command: command => invoke<void>('desktop_command', {command}),
  observe: () => invoke<unknown>('desktop_observe').then(observation),
  surface: (bounds,generation) => invoke<void>('desktop_surface', {bounds,generation}),
};
