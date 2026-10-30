import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultAudioEffects,noAudioEffects,parseAudioEffects,transitionTiming,audioEffectsEnabled,audioTransitionsEnabled} from '../src/audio-effects.ts';
const scope={serverId:'server',authority:'local' as const,accountId:'account',profileId:'profile',controllerId:'controller',controllerEpoch:'epoch',commandLaneId:'lane'};
const command={requestId:'prepare',sequence:'1',expectedRevision:'1',currentPlaybackId:'session-a',reason:'completion',entryId:'',startSeconds:0,prepareAudio:true};
const entry=(id:string)=>({id,itemId:'song',editionId:null,partId:null,sourceContext:{kind:'item',id:'song',revision:null,entryId:null},hidden:false,removed:false});
const view=()=>({queue:{id:'queue',scope,revision:'1',highestSeenSequence:'0',replayWindow:{results:256,bodies:32},repeat:'one',shuffled:true,currentEntryId:'entry-a',currentPlaybackId:'session-a',entries:[entry('entry-a'),entry('entry-b')]},highestSequence:'1',current:{id:'session-a',generation:1,state:'playing'},next:{entryId:'entry-b',available:true,reason:'ready'},postPlay:{nextEntryId:'entry-b',available:true,reason:'ready',countdownMs:10000,autoplay:true,passoutCheckDue:false,automaticAdvances:0},items:[{entryId:'entry-a',itemId:'song',title:'Song',kind:'song',libraryId:'library'},{entryId:'entry-b',itemId:'song',title:'Song',kind:'song',libraryId:'library'}]});
test('gapless, crossfade and normalization are independent preferences',()=>{
 assert.equal(audioEffectsEnabled(noAudioEffects),false);
 assert.equal(defaultAudioEffects.gapless,true,'gapless is on until the viewer turns it off (X-02)');
 for(const patch of [{gapless:true},{crossfadeSeconds:4},{normalization:'album'}])assert(audioEffectsEnabled(parseAudioEffects({...defaultAudioEffects,...patch})));
 assert.equal(audioTransitionsEnabled({...noAudioEffects,normalization:'track'}),false);
 for(const patch of [{gapless:1},{crossfadeSeconds:13},{crossfadeSeconds:NaN},{normalization:'auto'},{untrusted:true}])assert.throws(()=>parseAudioEffects({...defaultAudioEffects,...patch}));
});
test('scheduling uses the render clock, clamps late overlaps, and never backdates a missed edge',()=>{
 assert.deepEqual(transitionTiming(10,20,0,1),{start:20,duration:0,late:false});
 assert.deepEqual(transitionTiming(10,20,4,1),{start:16,duration:4,late:false});
 const late=transitionTiming(21,20,4,1);assert.equal(late.duration,0);assert.equal(late.late,true);assert(late.start>21);
 assert.throws(()=>transitionTiming(1,2,13,1));assert.throws(()=>transitionTiming(NaN,2,1,1));
});