import test from 'node:test';import assert from 'node:assert/strict';
import {PlayerSessionChoices} from '../src/index.ts';import {identity,scope,fence,channelFence} from './fixtures.ts';
test('a choice lasts one playback through queue entries, a drawer pick, remount, and replay',()=>{
 const c=new PlayerSessionChoices();c.begin(identity);c.bindSource(fence);assert.equal(c.get('forcedOnly',identity,false),false);assert.equal(c.has('forcedOnly',identity),false);
 c.set('forcedOnly',identity,true);c.set('subtitleTrackId',identity,'s2',fence);c.set('audioTrackId',identity,'a1',fence);c.set('audioLanguage',identity,'ja',fence);c.set('subtitleLanguage',identity,'en',fence);c.set('speed',identity,1.5);c.set('quality',identity,'720p-high');c.set('subtitleOffsetMs',identity,500,fence);
 c.begin({...identity});c.bindSource({...fence,sessionId:'next',sourceId:'next',entryId:'second'});
 assert.equal(c.get('forcedOnly',identity,false),true);assert.equal(c.get('speed',identity,1),1.5);assert.equal(c.get('quality',identity,'original'),'720p-high');assert.equal(c.get('subtitleTrackId',identity,null),null);assert.equal(c.get('audioTrackId',identity,'a2'),'a2');assert.equal(c.get('audioLanguage',identity,'en'),'ja');assert.equal(c.get('subtitleLanguage',identity,null),'en');assert.equal(c.get('subtitleOffsetMs',identity,0),0);
 let told=0;const stop=c.subscribe(()=>told++),before=c.getVersion();c.set('forcedOnly',identity,false);assert.equal(told,1);assert.notEqual(c.getVersion(),before);stop();
});
test('stop, replacement and every identity fence discard choices physically and stale callbacks cannot resurrect them',()=>{
 const c=new PlayerSessionChoices();c.begin(identity);c.bindSource(fence);c.set('quality',identity,'automatic');c.stop(identity);assert.equal(c.set('quality',identity,'720p-high'),false);assert.equal(c.get('quality',identity,'original'),'original');
 c.begin(identity);c.bindSource(fence);c.set('quality',identity,'720p-high');const next={...identity,run:2};c.begin(next);assert.equal(c.get('quality',next,'original'),'original');assert.equal(c.set('speed',identity,2),false);
 for(const key of Object.keys(scope)){const other={...next,scope:{...scope,[key]:key==='authority'?'hosted':'other'}} as typeof identity;c.begin(other);c.set('speed',other,2);assert.equal(c.get('speed',next,1),1);}
});
test('typed bounded choices reject malformed values without growing an arbitrary-key map',()=>{
 const c=new PlayerSessionChoices();c.begin(identity);c.bindSource(fence);
 for(const [name,value] of [['speed',NaN],['speed',99],['quality','2161'],['subtitleSize','huge'],['subtitleBackground','shadow'],['subtitleOffsetMs',Infinity],['audioTrackId','../../a'],['__proto__',true]] as const)assert.throws(()=>c.set(name as never,identity,value as never,fence));
 c.set('subtitleBackground',identity,'translucent');c.set('subtitleSize',identity,'large');assert.equal(c.get('subtitleSize',identity,'medium'),'large');
});

test('turning subtitles off carries across sources until the playback stops',()=>{const c=new PlayerSessionChoices();c.begin(identity);c.bindSource(fence);c.set('subtitleTrackId',identity,null,fence);c.set('subtitleLanguage',identity,null,fence);c.bindSource({...fence,sessionId:'next',sourceId:'next',entryId:'second'});assert.equal(c.get('subtitleTrackId',identity,'default'),null);c.stop(identity);assert.equal(c.get('subtitleTrackId',identity,'default'),'default');});

test('old source acknowledgements cannot overwrite audio, subtitles or timing in the same playback',()=>{const c=new PlayerSessionChoices();c.begin(identity);c.bindSource(fence);c.set('audioLanguage',identity,'ja',fence);const next={...fence,sourceId:'next',sessionId:'next'};c.bindSource(next);assert.equal(c.set('audioLanguage',identity,'fr',fence),false);assert.equal(c.get('audioLanguage',identity,'en'),'ja');assert.equal(c.set('subtitleOffsetMs',identity,500,fence),false);assert.equal(c.set('subtitleOffsetMs',identity,250,next),true);assert.equal(c.get('subtitleOffsetMs',identity,0),250);});

test('channel choices use the real playback run and source generation, rejecting queue collisions and retired closures',()=>{
 const c=new PlayerSessionChoices(),live={...channelFence,sessionId:fence.sessionId,channelId:fence.itemId};
 c.begin(identity);c.bindSource(live);c.set('audioLanguage',identity,'ja',live);c.set('audioTrackId',identity,'a1',live);c.set('speed',identity,1.5);c.set('quality',identity,'720p-high');
 assert.equal(c.set('audioLanguage',identity,'fr',fence),false);
 const next={...live,generation:2};c.bindSource(next);assert.equal(c.get('audioTrackId',identity,'a2'),'a2');assert.equal(c.get('audioLanguage',identity,'en'),'ja');
 assert.equal(c.set('audioTrackId',identity,'a1',live),false);assert.equal(c.set('subtitleOffsetMs',identity,500,live),false);assert.equal(c.set('audioTrackId',identity,'a2',next),true);
 const retune={...next,playback:{...identity,run:2}};c.begin(retune.playback);c.bindSource(retune);
 assert.equal(c.get('speed',retune.playback,1),1);assert.equal(c.get('quality',retune.playback,'original'),'original');assert.equal(c.set('quality',identity,'240p'),false);
 c.stop(retune.playback);assert.equal(c.bindSource(retune),false);assert.equal(c.set('audioTrackId',retune.playback,'a3',retune),false);
});
