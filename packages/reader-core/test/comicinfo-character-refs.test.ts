import test from 'node:test';
import assert from 'node:assert/strict';
import {crc32 as nativeCRC,deflateRawSync} from 'node:zlib';
import {openComicArchive} from '../src/comic/archive.ts';
import {comicInfo} from '../src/comic/image.ts';
import {archiveLimits} from '../src/comic/zip.ts';

// A real PNG file (one transparent pixel); ZIP fixtures are independently encoded.
const png=Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAAC0lEQVR42mNgAAIAAAUAAarVyFEAAAAASUVORK5CYII=','base64');
const signal=()=>new AbortController().signal;
function zip(files:{name:string;bytes:Uint8Array;deflate?:boolean}[]){
  const local:Buffer[]=[],central:Buffer[]=[];let offset=0;
  for(const file of files){
    const name=Buffer.from(file.name),bytes=Buffer.from(file.bytes),compressed=file.deflate?deflateRawSync(bytes):bytes;
    const header=Buffer.alloc(30);header.writeUInt32LE(0x04034b50);header.writeUInt16LE(20,4);header.writeUInt16LE(0x800,6);header.writeUInt16LE(file.deflate?8:0,8);header.writeUInt32LE(nativeCRC(bytes),14);header.writeUInt32LE(compressed.length,18);header.writeUInt32LE(bytes.length,22);header.writeUInt16LE(name.length,26);
    const dir=Buffer.alloc(46);dir.writeUInt32LE(0x02014b50);dir.writeUInt16LE(20,4);dir.writeUInt16LE(20,6);header.copy(dir,8,6,26);dir.writeUInt16LE(name.length,28);dir.writeUInt32LE(offset,42);
    local.push(header,name,compressed);central.push(dir,name);offset+=header.length+name.length+compressed.length;
  }
  const dir=Buffer.concat(central),end=Buffer.alloc(22);end.writeUInt32LE(0x06054b50);end.writeUInt16LE(files.length,8);end.writeUInt16LE(files.length,10);end.writeUInt32LE(dir.length,12);end.writeUInt32LE(offset,16);
  return Buffer.concat([...local,dir,end]);
}
function source(bytes:Uint8Array){const reads:{start:number;endExclusive:number}[]=[];return {id:'lawful-test',revision:'revision-1',size:bytes.length,reads,async read(range:{start:number;endExclusive:number},sig:AbortSignal){sig.throwIfAborted();reads.push(range);return bytes.slice(range.start,range.endExclusive);}};}
const info=(body:string)=>comicInfo(Buffer.from(`<ComicInfo>${body}</ComicInfo>`));

test('ComicInfo decodes XML character references once, preserves invalid references and five names',()=>{
  assert.deepEqual(info('<Title>A &#38; B &#x26; C</Title><Writer>Jos&#233; &#x1F642;</Writer><Manga>YesAndRightToLef&#116;</Manga>'),{title:'A & B & C',author:'José 🙂',direction:'rtl'});
  assert.deepEqual(info('<Title>&amp;#65;|&#38;lt;|&lt;</Title>'),{title:'&#65;|&lt;|<'});
  const preserved='&#0;|&#xD800;|&#x110000;|&#X41;|&#65';
  assert.equal(info(`<Title>${preserved}</Title>`).title,preserved);
  assert.deepEqual(info('<Title>&amp;|&lt;|&gt;|&quot;|&apos;</Title><Manga>Yes</Manga>'),{title:'&|<|>|"|\'',direction:'ltr'});
});

test('authored CBZ decodes ComicInfo character references in real archive metadata',async()=>{
  const bytes=zip([{name:'page2.png',bytes:png},{name:'page1.png',bytes:png},{name:'ComicInfo.xml',bytes:Buffer.from('<ComicInfo><Title>A &#38; B &#x26; C</Title><Writer>Jos&#233; &#x1F642;</Writer><Manga>YesAndRightToLef&#116;</Manga></ComicInfo>')}]);
  const input=source(bytes),archive=await openComicArchive(input,signal());
  try {
    assert.equal(archive.pageCount,2);assert.deepEqual(archive.metadata,{title:'A & B & C',author:'José 🙂',direction:'rtl'});
    const pages=[await archive.page(0,{width:300,signal:signal()}),await archive.page(1,{width:300,signal:signal()})];
    assert.deepEqual(pages.map(page=>page.name),['page1.png','page2.png']);assert.ok(pages.every(page=>Buffer.from(page.bytes).equals(png)));
    assert.ok(input.reads.every(range=>range.endExclusive-range.start<=archiveLimits.rangeBytes));
  }finally{archive.dispose();}
});
