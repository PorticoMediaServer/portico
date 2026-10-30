import assert from 'node:assert/strict';
import {test} from 'node:test';
import {readFile} from 'node:fs/promises';
import {loadLiterataFont,literataFontUrl} from '../src/fonts.ts';

test('trusted Literata loader omits credentials, verifies pinned bytes and cancels an oversized body',async()=>{
  const original=globalThis.fetch;
  try {
    const bytes=await readFile(new URL('../assets/Literata[opsz,wght].ttf',import.meta.url));
    globalThis.fetch=async(url,options)=>{
      assert.equal(url,literataFontUrl);assert.equal(options?.credentials,'omit');
      return new Response(new ReadableStream({start(controller){
        controller.enqueue(bytes.subarray(0,1));controller.enqueue(bytes.subarray(1));controller.close();
      }}));
    };
    const blob=await loadLiterataFont();assert.equal(blob.size,955132);assert.equal(blob.type,'font/ttf');
    const corrupted=bytes.slice();corrupted[500]^=1;
    globalThis.fetch=async()=>new Response(corrupted);
    await assert.rejects(loadLiterataFont(),{code:'invalid_source'});
    let cancelled=false;
    globalThis.fetch=async()=>new Response(new ReadableStream({start(controller){controller.enqueue(new Uint8Array(955133));},cancel(){cancelled=true;}}));
    await assert.rejects(loadLiterataFont(),{code:'invalid_source'});assert.ok(cancelled);
    const abort=new AbortController();abort.abort();
    await assert.rejects(loadLiterataFont({signal:abort.signal}),{code:'aborted'});
  } finally {globalThis.fetch=original;}
});
test('Literata verifies the pinned font on self-host HTTP without WebCrypto subtle',async()=>{
  const originalFetch=globalThis.fetch,cryptoDescriptor=Object.getOwnPropertyDescriptor(globalThis,'crypto');
  try {
    Object.defineProperty(globalThis,'crypto',{configurable:true,value:{}});
    const bytes=await readFile(new URL('../assets/Literata[opsz,wght].ttf',import.meta.url));
    globalThis.fetch=async()=>new Response(bytes);
    assert.equal((await loadLiterataFont()).size,955132);
    const corrupted=bytes.slice();corrupted[500]^=1;
    globalThis.fetch=async()=>new Response(corrupted);
    await assert.rejects(loadLiterataFont(),{code:'invalid_source'});
  } finally {
    globalThis.fetch=originalFetch;
    if(cryptoDescriptor)Object.defineProperty(globalThis,'crypto',cryptoDescriptor);else Reflect.deleteProperty(globalThis,'crypto');
  }
});
