import test from 'node:test';
import assert from 'node:assert/strict';
import {parseAudioEffects,smoothTransitionSeconds} from '../src/audio-effects.ts';
import {audioGainLinear,parseAudioRenderV2} from '../src/playback-v1/audio-render.ts';
const wire={version:2,id:'test',mode:'converted',url:'/v1/media/fixture/audio-converted',codec:'flac',container:'flac',sampleRate:88200,channels:6,bitDepth:24,durationFrames:88200,trim:{startFrames:0,endFrames:0,source:'flac'},sourceBitrateBps:4608000,loudnessEnvelope:{intervalMs:100,startLufs:[-30,-24],endLufs:[-18,-18]},gain:{source:'analysis',trackDb:6,trackPeak:.25}};
test('music processing validates additions, prevents double gain and suppresses album overlap',()=>{
 const plan=parseAudioRenderV2(wire)!;assert.equal(plan.sourceBitrateBps,4608000);assert.equal(audioGainLinear(plan,'track',0,true),1);assert.ok(audioGainLinear(plan,'track')>1);
 assert.equal(parseAudioRenderV2({...wire,loudnessAppliedDb:-6}),undefined);
 const baked=parseAudioRenderV2({...wire,gain:undefined,loudnessAppliedDb:-6})!;assert.equal(audioGainLinear(baked,'track'),1);
 assert.equal(smoothTransitionSeconds(plan,plan,8,true),0);assert.ok(smoothTransitionSeconds(plan,plan,8,false)<8);
 assert.equal(parseAudioEffects({gapless:true,crossfadeSeconds:0,normalization:'off',neverBoost:true,smoothTransitions:true,sampleRateMatching:'smart'}).neverBoost,true);
 for(const bad of [{neverBoost:'yes'},{smoothTransitions:1},{sampleRateMatching:'auto'},{eq:{}}])assert.throws(()=>parseAudioEffects({gapless:true,crossfadeSeconds:0,normalization:'off',...bad}));
 for(const e of [{intervalMs:100,startLufs:[NaN],endLufs:[]},{intervalMs:50,startLufs:[],endLufs:[]},{intervalMs:100,startLufs:Array(31).fill(-18),endLufs:[]}])assert.equal(parseAudioRenderV2({...wire,loudnessEnvelope:e}),undefined);
});
