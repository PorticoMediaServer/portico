import {test} from 'node:test';
import assert from 'node:assert/strict';
import {createComicAdapter, rasterInfo, type ComicPageProvider, type RasterInfo} from '@portico/reader-core';

// Original encoded fixtures were independently decoded with Pillow. Both
// WebP encodings matter: the size fields occupy different bit layouts.
const rasters = [
  {
    "name": "pixel-lossless.webp",
    "mime": "image/webp",
    "base64": "UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA=="
  },
  {
    "name": "pixel-lossy.webp",
    "mime": "image/webp",
    "base64": "UklGRiQAAABXRUJQVlA4IBgAAAAwAQCdASoBAAEAAUAmJaQAA3AA/vz0AAA="
  },
  {
    "name": "pixel.gif",
    "mime": "image/gif",
    "base64": "R0lGODlhAQABAIEAAAAAAAAAAAAAAAAAACH5BAEAAAAALAAAAAABAAEAAAgEAAEEBAA7"
  },
  {
    "name": "pixel.jpg",
    "mime": "image/jpeg",
    "base64": "/9j/4AAQSkZJRgABAQAAAQABAAD/2wBDAAgGBgcGBQgHBwcJCQgKDBQNDAsLDBkSEw8UHRofHh0aHBwgJC4nICIsIxwcKDcpLDAxNDQ0Hyc5PTgyPC4zNDL/2wBDAQkJCQwLDBgNDRgyIRwhMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjL/wAARCAABAAEDASIAAhEBAxEB/8QAHwAAAQUBAQEBAQEAAAAAAAAAAAECAwQFBgcICQoL/8QAtRAAAgEDAwIEAwUFBAQAAAF9AQIDAAQRBRIhMUEGE1FhByJxFDKBkaEII0KxwRVS0fAkM2JyggkKFhcYGRolJicoKSo0NTY3ODk6Q0RFRkdISUpTVFVWV1hZWmNkZWZnaGlqc3R1dnd4eXqDhIWGh4iJipKTlJWWl5iZmqKjpKWmp6ipqrKztLW2t7i5usLDxMXGx8jJytLT1NXW19jZ2uHi4+Tl5ufo6erx8vP09fb3+Pn6/8QAHwEAAwEBAQEBAQEBAQAAAAAAAAECAwQFBgcICQoL/8QAtREAAgECBAQDBAcFBAQAAQJ3AAECAxEEBSExBhJBUQdhcRMiMoEIFEKRobHBCSMzUvAVYnLRChYkNOEl8RcYGRomJygpKjU2Nzg5OkNERUZHSElKU1RVVldYWVpjZGVmZ2hpanN0dXZ3eHl6goOEhYaHiImKkpOUlZaXmJmaoqOkpaanqKmqsrO0tba3uLm6wsPExcbHyMnK0tPU1dbX2Nna4uPk5ebn6Onq8vP09fb3+Pn6/9oADAMBAAIRAxEAPwD3+iiigD//2Q=="
  },
  {
    "name": "pixel.png",
    "mime": "image/png",
    "base64": "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGNgYGBgAAAABQABpfZFQAAAAABJRU5ErkJggg=="
  }
];

test('public package raster inspector recognizes independently decoded raster encodings',()=>{
  for(const fixture of rasters){
    const info:RasterInfo=rasterInfo(Buffer.from(fixture.base64,'base64'));
    assert.deepEqual(info,{mime:fixture.mime,width:1,height:1},fixture.name);
    assert.ok(Object.isFrozen(info),fixture.name);
  }
});

test('public inspector composes returned variant dimensions with fenced comic previews',async()=>{
  // A true 48x64 resample of an independently decoded 768x1024 original.
  // The provider must report the encoded variant, not the original dimensions.
  const encoded=Buffer.from('iVBORw0KGgoAAAANSUhEUgAAADAAAABACAIAAADTQmMRAAAAwUlEQVR4nO3SMRKCMBSE4bePEIgpoMHLehSv4jEcLEEqsFALFGUsLMAzPIvNpPjLneTDbn9o+iuA4NNtGdvhZtWqKiKu6W/HdlAg5tk4zXVn1skyCID+rnmLiAvexdwrsMnTkFn2+kJVGcepABC8q4r4eM1q1Oug83Cvu8vyl8/352TXNERDNERDNERDNERDNERDNERDNERDNERDNERDNERDNERDNERDNERDNERDNERDNGRuSBWJqgKJwraXQSp/dr7Lvcx9p6kcTAAAAABJRU5ErkJggg==','base64');
  const source={id:'public-raster-variant',revision:'opaque-fixture-revision',size:5034};
  const signal=new AbortController().signal;
  let calls=0,disposed=0,metadata:'observed'|'original'|'wrong-mime'='observed';
  const provider:ComicPageProvider={id:source.id,revision:source.revision,pageCount:1,metadata:{},
    async page(index,{signal:requestSignal}){
      requestSignal.throwIfAborted();calls++;
      const observed:RasterInfo=rasterInfo(encoded);
      const image:RasterInfo=metadata==='original'?{...observed,width:768,height:1024}:
        metadata==='wrong-mime'?{...observed,mime:'image/jpeg'}:observed;
      return {index,name:'resized.png',bytes:encoded,image};
    },dispose(){disposed++;}
  };
  const document=await createComicAdapter({pages:async()=>provider}).open({source,signal});
  try{
    const preview=await document.page!(0,{width:64,signal});
    assert.deepEqual({mime:preview.mime,width:preview.width,height:preview.height},{mime:'image/png',width:48,height:64});
    assert.deepEqual(Buffer.from(preview.bytes),encoded);
    assert.notEqual(preview.bytes,encoded);
    preview.bytes[0]=0;assert.equal(encoded[0],137);
    for(const bad of ['original','wrong-mime'] as const){
      metadata=bad;
      await assert.rejects(document.page!(0,{width:64,signal}),{code:'image_invalid'});
    }
    metadata='observed';const before=calls;
    for(const [index,width] of [[-1,64],[1,64],[0,31],[0,2049]]){
      await assert.rejects(document.page!(index,{width,signal}),{code:'page_invalid'});
    }
    const aborted=new AbortController();aborted.abort();
    await assert.rejects(document.page!(0,{width:64,signal:aborted.signal}));
    source.revision='changed';
    await assert.rejects(document.page!(0,{width:64,signal}),{code:'archive_invalid'});
    assert.equal(calls,before,'bounds, abort and changed revision must fail before provider I/O');
  }finally{await document.dispose();await document.dispose();}
  assert.equal(disposed,1);
  await assert.rejects(document.page!(0,{width:64,signal}),{code:'closed'});
});
