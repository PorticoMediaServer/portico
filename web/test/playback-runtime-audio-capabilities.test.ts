import test from 'node:test';
import assert from 'node:assert/strict';
import {probeAudioDecode} from '../src/platform/playback/audio/capabilities.ts';
import {capabilityDocumentProblem, webDeclaredProfile} from '../../packages/client-core/src/client-profile.ts';

test('audioDecode declares only what passed the fixtures on this engine and the browser confirms', async () => {
  const all = async () => true;
  const blink = await probeAudioDecode(all, 'blink');
  assert.deepEqual(blink.map(a => [a.codec, a.via]), [['pcm', 'pcm'], ['flac', 'webcodecs'], ['mp3', 'webcodecs'], ['aac', 'webcodecs'], ['opus', 'webcodecs']]);
  const webkit = await probeAudioDecode(all, 'webkit');
  assert.equal(webkit.find(a => a.codec === 'flac')!.via, 'js-flac', 'Safari’s WebCodecs FLAC failed the fixtures: the JavaScript decoder plays FLAC');
  assert.equal(webkit.find(a => a.codec === 'alac'), undefined, 'ALAC decodes nowhere here: the server converts it to FLAC');
  const gecko = await probeAudioDecode(all, 'gecko');
  assert.deepEqual(gecko.map(a => a.codec), ['pcm', 'flac'], 'an engine never measured declares only what is decoded here');
  const none = await probeAudioDecode(undefined, 'blink');
  assert.deepEqual(none.map(a => [a.codec, a.via]), [['pcm', 'pcm'], ['flac', 'js-flac']], 'no WebCodecs: FLAC and PCM still play');
  const limited = await probeAudioDecode(async c => c.sampleRate <= 48000 && c.numberOfChannels <= 2, 'blink');
  assert.equal(limited.find(a => a.codec === 'aac')!.maxSampleRate, 48000);
  assert.equal(limited.find(a => a.codec === 'aac')!.maxChannels, 2);
});

test('the capability document carries audioDecode and passes the admission rules', async () => {
  const audioDecode = (await probeAudioDecode(async () => true, 'blink')).map(a => ({...a, containers: [...a.containers]}));
  assert.equal(capabilityDocumentProblem({...webDeclaredProfile(), audioDecode}), null);
});

test('capability discovery waits for runtime alignment evidence before admitting Gecko codecs',async()=>{
 const {recordDecoderSkip,WEBCODECS_SKIP}=await import('../src/platform/playback/audio/track.ts');
 const prior={...WEBCODECS_SKIP.gecko};
 try {
  const check=Object.assign(async()=>true,{ready:Promise.resolve().then(()=>recordDecoderSkip('gecko','mp3',0))});
  const caps=await probeAudioDecode(check,'gecko');assert.ok(caps.some(c=>c.codec==='mp3'));
  recordDecoderSkip('gecko','opus',312);
  assert.equal(WEBCODECS_SKIP.gecko.opus,'preskip','each real Opus header supplies its own pre-skip');
 }finally{for(const key of Object.keys(WEBCODECS_SKIP.gecko))delete (WEBCODECS_SKIP.gecko as Record<string,unknown>)[key];Object.assign(WEBCODECS_SKIP.gecko,prior);}
});
