import test from 'node:test';
import assert from 'node:assert/strict';
import {aacConfig} from '../src/platform/playback/audio/aac-config.ts';
const config=(fields:[number,number][])=>{const b=fields.flatMap(([v,n])=>Array.from({length:n},(_,i)=>(v>>(n-1-i))&1));return Uint8Array.from({length:Math.ceil(b.length/8)},(_,i)=>b.slice(i*8,i*8+8).reduce((a,v,j)=>a|(v<<(7-j)),0));};
test('AAC LC and explicit SBR/PS use the decoded clock and channel layout',()=>{
 assert.deepEqual(aacConfig(config([[2,5],[4,4],[2,4],[0,3]])),{objectType:2,sampleRate:44100,channels:2,framesPerPacket:1024});
 assert.deepEqual(aacConfig(config([[5,5],[7,4],[2,4],[4,4],[2,5]])),{objectType:5,sampleRate:44100,channels:2,framesPerPacket:2048});
 assert.deepEqual(aacConfig(config([[29,5],[7,4],[1,4],[4,4],[2,5]])),{objectType:29,sampleRate:44100,channels:2,framesPerPacket:2048});
 assert.deepEqual(aacConfig(config([[2,5],[15,4],[48000,24],[2,4],[0,3]])),{objectType:2,sampleRate:48000,channels:2,framesPerPacket:1024});
 assert.throws(()=>aacConfig(new Uint8Array([0x2b])));
});
test('implicit SBR and PS extensions preserve the output clock',()=>{
 assert.deepEqual(aacConfig(config([[2,5],[7,4],[1,4],[0,3],[0x2b7,11],[5,5],[1,1],[4,4],[0x548,11],[1,1]])),{objectType:2,sampleRate:44100,channels:2,framesPerPacket:2048});
});
