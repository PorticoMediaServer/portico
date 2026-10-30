import test from 'node:test';
import assert from 'node:assert/strict';
import {SessionsClient} from '../src/playback-v1/sessions.ts';
import type {V1Http} from '../src/playback-v1/http.ts';
import {routePath} from '../../contracts/src/routes.ts';

const resource = (observedPlayback?: unknown) => ({id:'session',revision:'1',kind:'vod',role:'local',state:'playing',
  presentation:{generation:1,mode:'direct',url:'/v1/media/current',startPositionMs:0,subtitles:[],decision:{}},
  ...(observedPlayback === undefined ? {} : {observedPlayback})});

function wire(raw: unknown) {
  const requests: string[] = [];
  const http: V1Http = {send: async request => { requests.push(request.path); return {status:200,headers:{},body:JSON.parse(JSON.stringify(raw))}; }};
  return {client:new SessionsClient(http),requests};
}

test('SessionsClient GET retains only actual sparse immutable renderer facts',async()=>{
  const raw = resource({video:{width:1920,height:1080},audio:{handling:'decoded',channels:2,sampleRate:44100},privateUrl:'https://private.invalid/?token=secret'});
  const {client,requests} = wire(raw);
  const session = await client.get('session');
  assert.deepEqual(requests,[routePath('get_playback_session',{id:'session'})]);
  assert.deepEqual(session.observedPlayback,{video:{width:1920,height:1080},audio:{handling:'decoded',channels:2,sampleRate:44100}});
  assert.equal(session.observedPlayback?.screen,undefined);
  assert.equal(session.observedPlayback?.audio?.bitDepth,undefined);
  assert.ok(Object.isFrozen(session.observedPlayback) && Object.isFrozen(session.observedPlayback?.video) && Object.isFrozen(session.observedPlayback?.audio));
  assert.ok(!JSON.stringify(session.observedPlayback).includes('secret'));
});

test('missing, empty and ended snapshots do not inherit or fabricate output',async()=>{
  assert.equal((await wire(resource()).client.get('session')).observedPlayback,undefined);
  assert.deepEqual((await wire(resource({})).client.get('session')).observedPlayback,{});
  const ended = {...resource({screen:{dynamicRange:'hdr10'}}),state:'ended'};
  assert.equal((await wire(ended).client.get('session')).observedPlayback,undefined);
  const {client} = wire({...resource(),presentation:{...resource().presentation,generation:2}});
  const replacement = await client.get('session');
  assert.equal(replacement.presentation.generation,2);
  assert.equal(replacement.observedPlayback,undefined);
});

test('invalid optional renderer blocks stay unknown while session authority stays readable',async()=>{
  for(const value of [null,[],{video:null},{video:{height:-1}},{screen:{refreshRateHz:2000}},{audio:{handling:'guessed-hdmi'}},{audio:{sampleRate:9999999}},{subtitles:{rendering:'unknown'}}]) {
    const session = await wire(resource(value)).client.get('session');
    assert.equal(session.id,'session');
    assert.equal(session.revision,'1');
    assert.equal(session.observedPlayback,undefined);
  }
});
