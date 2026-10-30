import test from 'node:test';
import assert from 'node:assert/strict';
import {parseDvbPackets} from '../src/platform/playback/subtitles/dvb.ts';
import {parsePgs} from '../src/platform/playback/subtitles/pgs.ts';
import {bytes,dvbPage,dvbRegion,pgsPresentation,pgsPalette,pgsObject,pgsEnd,picture} from './helpers/subtitle-samples.ts';

const packet=(data:number[],timeMs=1000)=>({data:Uint8Array.from(data),timeMs,durationMs:null});

test('DVB bounds the sum of live region pixels before allocation',()=>{
 const data=[...dvbPage(1,2,1,[[1,0,0],[2,0,0]]),...dvbRegion(1,1,[8,8],8,0,1,[]),...dvbRegion(1,2,[8,8],8,0,1,[])];
 const track=parseDvbPackets([packet(data)],null,{maxStatePixels:64});
 assert.equal(track.cues[0]!.paint().length,1);assert.ok(track.skipped>0);
});

test('DVB charges retained region snapshots and stops before packing excessive work',()=>{
 const data=[...dvbPage(1,2,1,[[1,0,0]]),...dvbRegion(1,1,[8,8],8,0,1,[])];
 const packets=[packet(data),packet(dvbPage(1,2,0,[[1,0,0]]),2000)];
 for(const limit of [{maxRetainedBytes:1090},{maxWorkPixels:128}]){
  const track=parseDvbPackets(packets,null,limit);
  assert.equal(track.cues.length,1);assert.ok(track.skipped>0);
 }
});

test('DVB and PGS bound combined paint pixels and retain other pictures',()=>{
 const dvb=parseDvbPackets([packet([...dvbPage(1,2,1,[[1,0,0],[1,8,0]]),...dvbRegion(1,1,[8,8],8,0,1,[])])],null,{maxFramePixels:64});
 assert.equal(dvb.cues[0]!.paint().length,1);assert.ok(dvb.skipped>0);
 const art=picture('11111111','11111111','11111111','11111111','11111111','11111111','11111111','11111111');
 const pgs=parsePgs(bytes(pgsPresentation(1000,[16,8],'epoch',0,[{id:1,x:0,y:0},{id:1,x:8,y:0}]),pgsPalette(1000,0,[[1,235,128,128,255]]),pgsObject(1000,1,art),pgsEnd(1000)),{maxFramePixels:64});
 assert.equal(pgs.cues[0]!.paint().length,1);assert.ok(pgs.skipped>0);
});

test('a tiny DVB definition cannot allocate hundreds of full-size live regions',()=>{
 const regions=Array.from({length:256},(_,id)=>dvbRegion(1,id,[4096,2304],8,0,0,[])).flat();
 const track=parseDvbPackets([packet([...dvbPage(1,2,1,[[0,0,0]]),...regions])],null);
 assert.ok(track.skipped>=254,'combined default budget rejects excess definitions before pixel allocation');
});
