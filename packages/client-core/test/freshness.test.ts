import test from 'node:test';
import assert from 'node:assert/strict';
import {viewerScope} from '../src/presentation/content.ts';
import {parseFreshnessChange, subscribeFreshness} from '../src/freshness.ts';
import {EventsClient} from '../src/playback-v1/events.ts';

test('viewer scope survives token renewal and changes with profile', () => {
 const api={baseUrl:'http://server'};
 const viewer={authority:'local',accountId:'account',profileId:'one',serverId:'server'};
 const first=viewerScope({viewer},api);
 assert.equal(viewerScope({viewer:{...viewer}},api),first);
 assert.notEqual(viewerScope({viewer:{...viewer,profileId:'two'}},api),first);
});
test('freshness consumes counters without losing integer precision', () => {
 const change=parseFreshnessChange({id:'1',type:'progress.changed',resource:{kind:'library',id:'films'},data:{personal:'12',observation:'9007199254740993'}});
 assert.equal(change?.counters.observation,'9007199254740993');
 assert.equal(parseFreshnessChange({id:'2',type:'device.command'}),undefined);
});
test('features share the feed and resync is delivered even for a previously seen id', () => {
 const events=new EventsClient({http:{request:async()=>({status:200,headers:{},body:{}})}});
 let changes=0,resyncs=0;
 const stop=subscribeFreshness(events,()=>changes++);
 events.onResync(()=>resyncs++);
 events.deliver({id:'1',type:'library.changed',resource:{kind:'library',id:'films'}});
 events.deliver({id:'1',type:'stream.resync'});
 assert.equal(changes,1);assert.equal(resyncs,1);
 stop();events.deliver({id:'2',type:'settings.changed',resource:{kind:'settings',id:'server'}});
 assert.equal(changes,1);
});
