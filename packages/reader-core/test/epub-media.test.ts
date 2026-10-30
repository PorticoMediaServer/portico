import {test} from 'node:test';
import assert from 'node:assert/strict';
import {validateEpubRaster} from '../src/epub/media.ts';

test('EPUB raster budget applies before decode even when a PNG is disguised as a font',()=>{
  const png=new Uint8Array(24);png.set([137,80,78,71,13,10,26,10]);png.set([73,72,68,82],12);
  const dimensions=new DataView(png.buffer);dimensions.setUint32(16,100000);dimensions.setUint32(20,100000);
  assert.throws(()=>validateEpubRaster(png,'font.bin','font/ttf'),{code:'limit_exceeded'});
  dimensions.setUint32(16,1);dimensions.setUint32(20,1);assert.equal(validateEpubRaster(png,'cover.jpg','image/jpeg')?.mime,'image/png');
  assert.throws(()=>validateEpubRaster(new Uint8Array([1,2,3]),'cover.png','image/png'),{code:'invalid_book'});
  assert.doesNotThrow(()=>validateEpubRaster(new Uint8Array([0,1,0,0]),'publisher.ttf','font/ttf'));
});
