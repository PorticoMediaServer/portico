import test from 'node:test';
import assert from 'node:assert/strict';
import {execFileSync} from 'node:child_process';
import {mkdtempSync,readFileSync,readdirSync,rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {openDemuxer} from '../src/platform/playback/audio/demux.ts';
import {decoderProbes} from '../src/platform/playback/audio/decoder-probes.ts';
const reader=(data:Uint8Array)=>({size:data.length,read:async(o:number,n:number)=>data.slice(o,o+n)});

test('generated edit-list AAC, no-LAME MP3, multi-packet padding and Vorbis retain their measured frames',async()=>{
 const dir=mkdtempSync(join(tmpdir(),'portico-codec-fixtures-'));
 try{
  execFileSync('python3',[fileURLToPath(new URL('../../fixtures/audio-gapless/generate-portable.py',import.meta.url)),'--out',dir]);
  for(const file of readdirSync(dir).filter(f=>f.endsWith('.json'))){
   const s=JSON.parse(readFileSync(join(dir,file),'utf8')),data=new Uint8Array(readFileSync(join(dir,s.file)));
   const demux=await openDemuxer(s.container,reader(data));let frames=0,packets=0;
   for await(const packet of demux.packets()){packets++;frames+=packet.frames??0;}
   assert.ok(packets>0,s.file);assert.equal(demux.config.sampleRate,s.sampleRate,s.file);
   if(s.codec!=='vorbis')assert.equal(frames,s.rawFrames,s.file);
   assert.equal(s.rawFrames-s.trim.startFrames-s.trim.endFrames,s.durationFrames,s.file);
   if(file==='mp3-long-padding.json')assert.ok(s.trim.endFrames>1152);
   if(file==='aac-editlist.json'){assert.equal(s.trim.startFrames,1024);assert.ok(s.trim.endFrames>0);assert.equal(Buffer.from(data).indexOf('iTunSMPB'),-1);}
  }
 }finally{rmSync(dir,{recursive:true,force:true});}
});

test('runtime browser probes match a qualified full raw decode at the stored alignment anchor',()=>{
 for(const [codec,probe] of Object.entries(decoderProbes)){
  const bytes=Buffer.from(probe.data,'base64');
  const pcm=execFileSync('ffmpeg',['-v','error','-flags2','+skip_manual','-i','pipe:0','-ac','2','-f','f32le','pipe:1'],{input:bytes});
  assert.equal(pcm.length/8,probe.rawFrames,codec);
  for(let i=0;i<probe.reference.length;i++)assert.ok(Math.abs(pcm.readFloatLE((probe.anchor+i)*8)-probe.reference[i]!)<1e-6,`${codec}: sample ${i}`);
 }
});
