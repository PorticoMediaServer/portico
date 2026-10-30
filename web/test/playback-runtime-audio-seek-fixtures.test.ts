import test from 'node:test';
import assert from 'node:assert/strict';
import {execFileSync} from 'node:child_process';
import {mkdtempSync,readFileSync,rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {openFlac} from '../src/platform/playback/audio/flac.ts';
import {TrackStream} from '../src/platform/playback/audio/track.ts';
import {openMp3} from '../src/platform/playback/audio/mp3.ts';

const reader=(b:Uint8Array,reads:number[]=[])=>( {size:b.length,read:async(o:number,n:number)=>{reads.push(o);return b.slice(o,o+n);}} );
test('generated FLAC without an index returns exact deep samples using bounded bisection',async()=>{
 const dir=mkdtempSync(join(tmpdir(),'portico-flac-seek-'));
 try {
  const path=join(dir,'seek.flac');
  execFileSync('ffmpeg',['-v','error','-y','-f','lavfi','-i','sine=frequency=441:sample_rate=44100','-t','45','-c:a','flac',path]);
  const bytes=new Uint8Array(readFileSync(path));
  const d=await openFlac(reader(bytes));
  assert.equal(d.seekTable.length,0,'FFmpeg supplies no seek table: exercise bounded byte bisection');
  const plan={id:'seek',codec:'flac',container:'flac',sampleRate:44100,channels:1,durationFrames:45*44100,trim:{startFrames:0,endFrames:0}};
  const full=await TrackStream.open(plan,reader(bytes));assert.ok(full);
  const target=40*44100+123;
  let at=0, reference:Float32Array|undefined;
  for(;;){const b=await full.read(4096);if(!b)break;if(at+b.frames>target){reference=b.channels[0]!.slice(target-at,target-at+100);break;}at+=b.frames;}full.close();assert.ok(reference?.length);
  const reads:number[]=[];const sought=await TrackStream.open(plan,reader(bytes,reads),target);assert.ok(sought);
  const got=await sought.read(reference.length);sought.close();assert.deepEqual(got?.channels[0],reference);
  assert.ok(reads.length<40,'a deep seek uses bounded probes rather than a packet walk');
  assert.ok(reads.at(-1)!>bytes.length/2,'the decoder reads from a deep byte position');
 }finally{rmSync(dir,{recursive:true,force:true});}
});
test('unindexed deep MP3 seeks yield to the ordinary player before a prefix walk',async()=>{
 const path=new URL('../../fixtures/audio-gapless/single-mp3-lame.mp3',import.meta.url), bytes=new Uint8Array(readFileSync(path));
 const reads:number[]=[];
 const stream=await TrackStream.open({id:'long',codec:'mp3',container:'mp3',sampleRate:44100,channels:2,durationFrames:3600*44100,trim:{startFrames:1105,endFrames:0}},reader(bytes,reads),3500*44100);
 assert.equal(stream,undefined);assert.ok(reads.length<=2);
 const demux=await openMp3(reader(bytes));assert.equal(demux.config.codec,'mp3');
});
