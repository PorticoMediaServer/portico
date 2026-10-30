import {test} from 'node:test';
import assert from 'node:assert/strict';
import {epubFontKey} from '../src/epub/font-obfuscation.ts';
const hex=(bytes:Uint8Array)=>Array.from(bytes,b=>b.toString(16).padStart(2,'0')).join('');

test('OCF font keys use the standard digest and identifier whitespace rule without WebCrypto',()=>{
  const descriptor=Object.getOwnPropertyDescriptor(globalThis,'crypto');
  try {
    Object.defineProperty(globalThis,'crypto',{configurable:true,value:{}});
    assert.equal(hex(epubFontKey('abc')),'a9993e364706816aba3e25717850c26c9cd0d89d');
    assert.equal(hex(epubFontKey(' a\tb\r\nc ')),'a9993e364706816aba3e25717850c26c9cd0d89d');
    assert.equal(hex(epubFontKey('ofl-font-fixture-7905')),'ffd945a020a69d7e3d1bcd09c79d5d8267ed3696');
  }finally{if(descriptor)Object.defineProperty(globalThis,'crypto',descriptor);else Reflect.deleteProperty(globalThis,'crypto');}
});
