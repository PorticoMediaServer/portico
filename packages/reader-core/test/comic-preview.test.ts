import {test} from 'node:test';
import assert from 'node:assert/strict';
import {comicPreview} from '../src/comic/preview.ts';

test('comic preview uses bounded detached DOM canvas without OffscreenCanvas and clears it on every exit',async()=>{
  const oldCanvas=Object.getOwnPropertyDescriptor(globalThis,'OffscreenCanvas'),oldDocument=Object.getOwnPropertyDescriptor(globalThis,'document');
  let canvas:{width:number;height:number;getContext():unknown;toBlob(done:(blob:Blob|null)=>void):void},drawn=0,finish!:(blob:Blob|null)=>void;
  Object.defineProperty(globalThis,'OffscreenCanvas',{configurable:true,value:undefined});
  Object.defineProperty(globalThis,'document',{configurable:true,value:{createElement(name:string){assert.equal(name,'canvas');canvas={width:0,height:0,getContext(){return {drawImage(){drawn++;}};},toBlob(done){finish=done;}};return canvas;}}});
  try {
    const bitmap={width:768,height:1024} as ImageBitmap,signal=new AbortController().signal;
    const reading=comicPreview(bitmap,300,signal);assert.equal(canvas!.width,300);assert.equal(canvas!.height,400);finish(new Blob(['encoded PNG fixture']));
    const preview=await reading;assert.equal(preview.width,300);assert.equal(preview.height,400);assert.equal(preview.mime,'image/png');assert.equal(canvas!.width,0);assert.equal(canvas!.height,0);
    const abort=new AbortController(),cancelled=comicPreview(bitmap,300,abort.signal);abort.abort();finish(new Blob(['late bytes']));await assert.rejects(cancelled,{name:'AbortError'});assert.equal(canvas!.width,0);
    const failed=comicPreview(bitmap,300,signal);finish(null);await assert.rejects(failed,{code:'image_invalid'});assert.equal(canvas!.height,0);assert.equal(drawn,3);
  }finally{
    if(oldCanvas)Object.defineProperty(globalThis,'OffscreenCanvas',oldCanvas);else Reflect.deleteProperty(globalThis,'OffscreenCanvas');
    if(oldDocument)Object.defineProperty(globalThis,'document',oldDocument);else Reflect.deleteProperty(globalThis,'document');
  }
});
