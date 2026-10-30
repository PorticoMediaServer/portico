import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../i18n/src/index.ts';
import {musicSignal,musicSignalAccessibleName,musicFormatBadges,albumMusicFormatBadges,signalGain,formatSignalText,type MusicSignalInput} from '../src/music-signal.ts';
import {audioEffectsEnabled,audioTransitionsEnabled,noAudioEffects} from '../src/audio-effects.ts';

const base:MusicSignalInput={source:{codec:'flac',lossless:true,bitDepth:24,sampleRate:96000,channels:2},server:{route:'original'},output:{platform:'browser',device:'browser'},eqOn:false,eqApplied:false,normalization:'off',levelingApplied:false};
const key=(v:ReturnType<typeof musicSignal>)=>v.state?.key;
test('812/b4: priority uses actual conversion, EQ, gain and observed output',()=>{
 assert.equal(key(musicSignal({...base,eqOn:true,eqApplied:false})), 'player.signal.line.eqUnavailable');
 assert.equal(key(musicSignal({...base,eqOn:true,eqApplied:true})), 'player.signal.line.eqOn');
 assert.equal(key(musicSignal({...base,eqOn:true,server:{route:'converted',quality:'320',delivered:{codec:'opus',bitrateBps:256000}}})), 'player.signal.line.convertedBitrate');
 assert.equal(key(musicSignal({...base,normalization:'track',levelingApplied:true,levelingGainDb:-3.2})), 'player.signal.line.loudness');
 assert.equal(key(musicSignal({...base,normalization:'track',levelingApplied:true,levelingGainDb:0})), undefined);
 assert.equal(key(musicSignal({...base,server:{route:'original',loudnessAppliedDb:-12}})), 'player.signal.line.loudness');
 assert.equal(signalGain(-3.2),'−3.2');assert.equal(signalGain(1.5),'+1.5');
 const leveling=musicSignal({...base,normalization:'track'}).steps.find(s=>s.id==='leveling')?.value;
 assert.equal(typeof leveling==='object'&&leveling.key,'player.signal.notApplied');
});
test('812: browser and Apple never claim bit-perfect, unknown output stays unknown',()=>{
 const observed={...base.output,bitPerfect:true,bitPerfectMode:'desktop-exclusive' as const,exclusive:true,sampleRate:96000,bitDepth:24};
 assert.equal(musicSignal({...base,output:observed}).pill.key,'player.signal.pill.notBitPerfect');
 const apple={...observed,platform:'apple' as const,device:'iphone' as const,sampleRateMatched:true};
 assert.equal(key(musicSignal({...base,output:apple})),'player.signal.rateMatched');
 assert.equal(musicSignal({...base,output:apple}).healthy,false);
 assert.equal(key(musicSignal({...base,output:{...apple,sampleRate:48000,resampledBy:'iphone'}})),'player.signal.line.resampled');
 const desktop={...observed,platform:'desktop' as const,device:'computer' as const};
 assert.equal(musicSignal({...base,output:desktop}).pill.key,'player.signal.pill.bitPerfect');
 assert.equal(musicSignal({...base,output:{...desktop,sampleRate:48000}}).pill.key,'player.signal.pill.notBitPerfect');
 assert.equal(musicSignal({...base,server:undefined,output:desktop}).pill.key,'player.signal.pill.notBitPerfect');
 assert.equal(musicSignal({...base,output:{...desktop,volume:.72}}).rule,'desktop-volume');
 assert.equal(musicSignal(base).steps.at(-1)?.value,undefined);
});
test('b4: EQ failure is green/untouched in the step and speaks the exact accessible phrase',()=>{
 const model=musicSignal({...base,source:{...base.source,bitDepth:16,sampleRate:44100},eqOn:true});
 const step=model.steps.find(s=>s.id==='eq')!;
 assert.equal(step.changed,false);assert.equal(typeof step.value==='object'&&step.value.key,'player.signal.notApplied');
 assert.equal(musicSignalAccessibleName(model,defaultI18n.t),'Signal path: FLAC 16-bit 44.1 kHz. EQ not available for this song');
 assert.equal(formatSignalText({key:'player.signal.line.resampled',values:{by:'iphone',rate:{key:'player.signal.rate',values:{khz:48}}}},defaultI18n.t),'Resampled to 48 kHz by this iPhone');
});
test('812: DSD conversion is folded once and notable badges are bounded',()=>{
 const model=musicSignal({...base,source:{codec:'dsd',dsdMultiple:64},server:{route:'converted',dsdConverted:true,delivered:{codec:'flac',sampleRate:176400}}});
 assert.equal(model.foldSourceIntoState,true);assert.equal(model.source,undefined);
 assert.equal(musicSignalAccessibleName(model,defaultI18n.t),'Signal path: DSD64 converted to PCM 176.4 kHz');
 assert.deepEqual(musicFormatBadges({...base.source,bitDepth:16,sampleRate:44100}),[]);
 assert.deepEqual(musicFormatBadges({codec:'aac',lossless:false,bitrateBps:256000}),[]);
 assert.equal(musicFormatBadges({...base.source,atmos:true,channels:8,layout:'7.1'}).length,2);
 assert.equal(musicFormatBadges({...base.source,atmos:true,channels:6}).length,1);
 assert.equal(musicFormatBadges({...base.source,atmos:true,channels:8}).length,1);
 assert.equal(albumMusicFormatBadges([base.source,base.source,{dsdMultiple:128},{atmos:true}]).length,2);
});
test('812: Smooth transitions alone starts neither a renderer nor an overlap',()=>{
 assert.equal(audioEffectsEnabled({...noAudioEffects,smoothTransitions:true}),false);
 assert.equal(audioTransitionsEnabled({...noAudioEffects,smoothTransitions:true}),false);
});

test('Actual processing is independent of requested state and display rounding',()=>{
 const eq=musicSignal({...base,eqOn:false,eqApplied:true}).steps.find(s=>s.id==='eq')!;
 assert.equal(typeof eq.value==='object'&&eq.value.key,'player.signal.eq.on');
 const output={...base.output,platform:'desktop' as const,device:'computer' as const,exclusive:true,bitPerfect:true,bitPerfectMode:'desktop-exclusive' as const,sampleRate:96000,bitDepth:24};
 for(const input of [{...base,output,levelingApplied:true,levelingGainDb:.01},{...base,output,server:{route:'original' as const,loudnessAppliedDb:.01}}]){
  assert.equal(musicSignal(input).pill.key,'player.signal.pill.notBitPerfect');
 }
 assert.equal(musicSignal({...base,output}).pill.key,'player.signal.pill.bitPerfect');
 const measured=musicSignal({...base,server:{route:'converted',quality:'320',delivered:{codec:'opus',bitrateBps:256000}}});
 assert.equal(measured.state?.values?.kbps,256);
 const unknown=musicSignal({...base,server:{route:'converted',quality:'320',delivered:{codec:'opus'}}});
 assert.equal(unknown.state?.key,'player.signal.server.converted');
});

test('MS5: accepted baked gain is measured, not called off, when the current mode is off',()=>{
 const step=(input:MusicSignalInput)=>musicSignal(input).steps.find(value=>value.id==='leveling')?.value;
 const baked=step({...base,server:{route:'original',loudnessAppliedDb:-12},normalization:'off',levelingApplied:false});
 assert.equal(typeof baked==='object'&&baked.key,'player.signal.line.loudness');
 assert.equal(typeof baked==='object'?formatSignalText(baked,defaultI18n.t):undefined,'Loudness −12.0 dB');
 const zero=step({...base,server:{route:'original',loudnessAppliedDb:0},normalization:'off',levelingApplied:false});
 assert.equal(typeof zero==='object'&&zero.key,'player.signal.off');
 const absent=step({...base,server:{route:'original'},normalization:'off',levelingApplied:false});
 assert.equal(typeof absent==='object'&&absent.key,'player.signal.off');
 const renderer=step({...base,server:{route:'original'},normalization:'track',levelingApplied:true,levelingGainDb:-3.2});
 assert.equal(typeof renderer==='object'&&renderer.key,'player.signal.leveling.applied');
});

test('MS-MUSIC-07: a raw passthrough flag cannot hide actual EQ or loudness processing',()=>{
 const output={...base.output,passthrough:true};
 const eq=musicSignal({...base,output,eqOn:true,eqApplied:true});
 assert.equal(eq.state?.key,'player.signal.line.eqOn');
 const eqStep=eq.steps.find(s=>s.id==='eq')!;
 assert.equal(eqStep.changed,true);assert.equal(typeof eqStep.value==='object'&&eqStep.value.key,'player.signal.eq.on');
 assert.notEqual((eq.steps.find(s=>s.id==='output')?.value as {key?:string}|undefined)?.key,'player.signal.line.passthrough');
 const leveled=musicSignal({...base,output,normalization:'track',levelingApplied:true,levelingGainDb:-3.2});
 const leveling=leveled.steps.find(s=>s.id==='leveling')!;
 assert.equal(leveling.changed,true);assert.equal(typeof leveling.value==='object'&&leveling.value.key,'player.signal.leveling.applied');
 assert.notEqual((leveled.steps.find(s=>s.id==='output')?.value as {key?:string}|undefined)?.key,'player.signal.line.passthrough');
 const baked=musicSignal({...base,output,server:{route:'original',loudnessAppliedDb:-12}});
 const bakedStep=baked.steps.find(s=>s.id==='leveling')!;
 assert.equal(typeof bakedStep.value==='object'&&bakedStep.value.key,'player.signal.line.loudness');
 assert.notEqual((baked.steps.find(s=>s.id==='output')?.value as {key?:string}|undefined)?.key,'player.signal.line.passthrough');
});
test('MS-MUSIC-07: a genuinely untouched raw passthrough still says passed through',()=>{
 const output={...base.output,passthrough:true};
 const model=musicSignal({...base,output});
 assert.equal(model.state?.key,'player.signal.line.passthrough');
 assert.equal((model.steps.find(s=>s.id==='output')?.value as {key?:string}|undefined)?.key,'player.signal.line.passthrough');
 assert.equal(model.steps.find(s=>s.id==='leveling')?.changed,false);
 assert.equal(model.steps.find(s=>s.id==='eq')?.changed,false);
 assert.equal(typeof model.steps.find(s=>s.id==='leveling')?.value,'object');
 assert.equal(typeof model.steps.find(s=>s.id==='eq')?.value,'object');
 assert.equal((model.steps.find(s=>s.id==='leveling')?.value as {key?:string}).key,'player.signal.notApplied');
 assert.equal((model.steps.find(s=>s.id==='eq')?.value as {key?:string}).key,'player.signal.notApplied');
});
test('MS-MUSIC-07: lost object audio is an actual output fact even when the raw flag says passthrough',()=>{
 const output={...base.output,passthrough:true,atmosLost:true,decodedLayout:'7.1'};
 const model=musicSignal({...base,output});
 assert.equal(model.rule,4);
 assert.equal(model.state?.key,'player.signal.output.decoded');
 const out=model.steps.find(s=>s.id==='output')!;
 assert.equal(typeof out.value==='object'&&out.value.key,'player.signal.output.decoded');
 assert.notEqual((out.value as {key?:string}).key,'player.signal.line.passthrough');
});

test('MS-MUSIC-08: a raw flag cannot call conversion or a repackage with baked gain passthrough',()=>{
 const output={...base.output,passthrough:true};
 const converted=musicSignal({...base,output,server:{route:'converted',quality:'320',delivered:{codec:'opus',bitrateBps:256000}}});
 assert.equal(converted.state?.key,'player.signal.line.convertedBitrate');
 assert.equal(converted.steps.find(s=>s.id==='server')?.changed,true);
 assert.notEqual((converted.steps.find(s=>s.id==='output')?.value as {key?:string}|undefined)?.key,'player.signal.line.passthrough');
 const repackaged=musicSignal({...base,output,server:{route:'repackaged',delivered:{codec:'flac',sampleRate:96000},loudnessAppliedDb:-7}});
 assert.equal(repackaged.steps.find(s=>s.id==='server')?.changed,false);
 const leveling=repackaged.steps.find(s=>s.id==='leveling')!;
 assert.equal(typeof leveling.value==='object'&&leveling.value.key,'player.signal.line.loudness');
 assert.notEqual((repackaged.steps.find(s=>s.id==='output')?.value as {key?:string}|undefined)?.key,'player.signal.line.passthrough');
});
test('MS-MUSIC-08: a raw flag without server proof or observed output facts stays unknown',()=>{
 const model=musicSignal({...base,server:undefined,output:{...base.output,passthrough:true}});
 assert.equal(model.state,undefined);
 assert.equal(model.steps.find(s=>s.id==='output')?.value,undefined);
 assert.equal((model.steps.find(s=>s.id==='leveling')?.value as {key?:string}).key,'player.signal.off');
 assert.equal((model.steps.find(s=>s.id==='eq')?.value as {key?:string}).key,'player.signal.off');
});
test('MS-MUSIC-08: an untouched repackage retains its observed passthrough',()=>{
 const output={...base.output,passthrough:true};
 const model=musicSignal({...base,output,server:{route:'repackaged',delivered:{codec:'flac',sampleRate:96000,bitDepth:24}}});
 assert.equal(model.steps.find(s=>s.id==='server')?.changed,false);
 assert.equal(model.state?.key,'player.signal.line.passthrough');
 assert.equal((model.steps.find(s=>s.id==='output')?.value as {key?:string}|undefined)?.key,'player.signal.line.passthrough');
 assert.equal((model.steps.find(s=>s.id==='leveling')?.value as {key?:string}).key,'player.signal.notApplied');
});
