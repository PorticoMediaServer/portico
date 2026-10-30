import {test} from 'node:test';
import assert from 'node:assert/strict';
import {createComicAdapter, rasterInfo} from '../src/index.ts';
import type {ComicPageProvider, RasterInfo} from '../src/index.ts';

test('public raster inspection composes a bounded page provider without a browser decoder',async()=>{
  // A real transparent one-pixel PNG, supplied as already-bounded bytes.
  const bytes=Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAAC0lEQVR42mNgAAIAAAUAAarVyFEAAAAASUVORK5CYII=','base64');
  const source={id:'public-page-fixture',revision:'revision-1',size:bytes.length};
  let disposed=0;
  const provider:ComicPageProvider={id:source.id,revision:source.revision,pageCount:1,metadata:{},
    async page(index,{signal}){
      signal.throwIfAborted();
      const image:RasterInfo=rasterInfo(bytes);
      assert.ok(Object.isFrozen(image));
      return {index,name:'page.png',bytes,image};
    },dispose(){disposed++;}
  };
  const signal=new AbortController().signal;
  const document=await createComicAdapter({pages:async()=>provider}).open({source,signal});
  try {
    // This path must work in Node without ImageBitmap or another decoder:
    // the adapter independently checks the bytes before returning a preview.
    const preview=await document.page!(0,{width:32,signal});
    assert.deepEqual({mime:preview.mime,width:preview.width,height:preview.height},{mime:'image/png',width:1,height:1});
    assert.deepEqual(Buffer.from(preview.bytes),bytes);
    assert.notEqual(preview.bytes,bytes);
    const oversized=Buffer.from(bytes);oversized.writeUInt32BE(100000,16);
    assert.throws(()=>rasterInfo(oversized),{code:'image_limit'});
    assert.throws(()=>rasterInfo(new TextEncoder().encode('<svg/>')),{code:'image_invalid'});
  }finally{await document.dispose();}
  assert.equal(disposed,1);
});
