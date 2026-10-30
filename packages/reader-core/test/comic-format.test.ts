import test from 'node:test';
import assert from 'node:assert/strict';
import {crc32 as nativeCRC,deflateRawSync} from 'node:zlib';
import {openComicArchive} from '../src/comic/archive.ts';
import {createComicAdapter} from '../src/comic/adapter.ts';
import {comicInfo,rasterInfo} from '../src/comic/image.ts';
import {archiveLimits,crc32,naturalCompare,readDirectory,readEntry} from '../src/comic/zip.ts';

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
test('stored and deflated CBZ read actual images in natural page order, never hidden macOS artifacts',async()=>{
  const bytes=zip([{name:'page10.png',bytes:png,deflate:true},{name:'page2.png',bytes:png},{name:'__MACOSX/._page.png',bytes:png},{name:'ComicInfo.xml',bytes:Buffer.from('<ComicInfo><Title>A &amp; B</Title><Writer>McCay</Writer><Manga>YesAndRightToLeft</Manga></ComicInfo>')}]);
  const input=source(bytes),archive=await openComicArchive(input,signal());assert.equal(archive.pageCount,2);assert.deepEqual(archive.metadata,{title:'A & B',author:'McCay',direction:'rtl'});
  const page=await archive.page(0,{width:1400,signal:signal()});assert.equal(page.name,'page2.png');assert.deepEqual(page.image,{mime:'image/png',width:1,height:1});assert.deepEqual(Buffer.from(page.bytes),png);
  assert.deepEqual(Buffer.from((await archive.page(1,{width:300,signal:signal()})).bytes),png);
  assert.ok(input.reads.every(r=>r.endExclusive-r.start<=archiveLimits.rangeBytes));
  await assert.rejects(archive.page(2,{width:300,signal:signal()}),{code:'page_invalid'});archive.dispose();await assert.rejects(archive.page(0,{width:300,signal:signal()}),{code:'closed'});
});

test('ComicInfo Manga Yes keeps default left-to-right reading and preserves archive metadata',async()=>{
  const bytes=zip([{name:'page2.png',bytes:png},{name:'page1.png',bytes:png},{name:'ComicInfo.xml',bytes:Buffer.from('<ComicInfo><Title>A &amp; B &lt;C&gt;</Title><Writer>McCay &amp; Son</Writer><Manga>Yes</Manga></ComicInfo>')}]);
  const input=source(bytes),archive=await openComicArchive(input,signal());
  try {
    assert.equal(archive.pageCount,2);assert.deepEqual(archive.metadata,{title:'A & B <C>',author:'McCay & Son',direction:'ltr'});
    const pages=[await archive.page(0,{width:300,signal:signal()}),await archive.page(1,{width:300,signal:signal()})];
    assert.deepEqual(pages.map(page=>page.name),['page1.png','page2.png']);assert.ok(pages.every(page=>Buffer.from(page.bytes).equals(png)));
    assert.ok(input.reads.every(range=>range.endExclusive-range.start<=archiveLimits.rangeBytes));
  }finally{archive.dispose();}
});
test('ComicInfo Manga direction is right-to-left only for YesAndRightToLeft',()=>{
  const info=(manga?:string)=>comicInfo(Buffer.from(`<ComicInfo><Title>T &amp; T</Title><Writer>W &lt;X&gt;</Writer>${manga?`<Manga>${manga}</Manga>`:''}</ComicInfo>`));
  assert.deepEqual(info('Yes'),{title:'T & T',author:'W <X>',direction:'ltr'});
  assert.deepEqual(info('YesAndRightToLeft'),{title:'T & T',author:'W <X>',direction:'rtl'});
  assert.deepEqual(info('No'),{title:'T & T',author:'W <X>',direction:'ltr'});
  assert.deepEqual(info('Unknown'),{title:'T & T',author:'W <X>',direction:'ltr'});
  assert.deepEqual(info(),{title:'T & T',author:'W <X>'});
});

test('offline deflated CBZ extraction does not require a native DecompressionStream',async()=>{
  const previous=Object.getOwnPropertyDescriptor(globalThis,'DecompressionStream');
  const bytes=zip([{name:'page.png',bytes:png,deflate:true}]);
  Object.defineProperty(globalThis,'DecompressionStream',{configurable:true,value:undefined});
  try {
    const archive=await openComicArchive(source(bytes),signal());
    try{assert.deepEqual((await archive.page(0,{width:300,signal:signal()})).bytes,new Uint8Array(png));}finally{archive.dispose();}
  }finally{if(previous)Object.defineProperty(globalThis,'DecompressionStream',previous);else Reflect.deleteProperty(globalThis,'DecompressionStream');}
});

test('bundled DEFLATE fallback retains CRC, expansion, exact-size and cancellation fences',async()=>{
  const previous=Object.getOwnPropertyDescriptor(globalThis,'DecompressionStream');
  // Some hosts expose the constructor but cannot accept deflate-raw.
  Object.defineProperty(globalThis,'DecompressionStream',{configurable:true,value:class {constructor(){throw new TypeError('format unavailable');}}});
  try {
    const input=source(zip([{name:'page.png',bytes:png,deflate:true}])),[entry]=await readDirectory(input,signal());
    assert.deepEqual(await readEntry(input,entry,signal()),new Uint8Array(png));assert.ok(input.reads.every(range=>range.endExclusive-range.start<=archiveLimits.rangeBytes));
    const lying=source(zip([{name:'page.png',bytes:Buffer.alloc(10000),deflate:true}])),[large]=await readDirectory(lying,signal());
    await assert.rejects(readEntry(lying,large,signal(),{...archiveLimits,pageBytes:100}),{code:'archive_limit'});
    await assert.rejects(readEntry(input,{...entry,crc:0},signal()),{code:'archive_invalid'});
    const corrupted=zip([{name:'page.png',bytes:png,deflate:true}]),central=corrupted.length-22-46-8;
    corrupted.writeUInt32LE(0,14);corrupted.writeUInt32LE(0,central+16);
    const bad=source(corrupted),[badEntry]=await readDirectory(bad,signal());await assert.rejects(readEntry(bad,badEntry,signal()),{code:'archive_invalid'});
    const tooSmall=zip([{name:'page.png',bytes:png,deflate:true}]);tooSmall.writeUInt32LE(1,22);tooSmall.writeUInt32LE(1,tooSmall.length-22-46-8+24);
    const lyingSize=source(tooSmall),[smallEntry]=await readDirectory(lyingSize,signal());await assert.rejects(readEntry(lyingSize,smallEntry,signal()),{code:'archive_limit'});
    const abort=new AbortController();abort.abort();await assert.rejects(readEntry(input,entry,abort.signal),{name:'AbortError'});
  }finally{if(previous)Object.defineProperty(globalThis,'DecompressionStream',previous);else Reflect.deleteProperty(globalThis,'DecompressionStream');}
});
test('central directory CRC, local name, truncation and overlapping data fail closed',async()=>{
  const good=zip([{name:'p.png',bytes:png}]);
  const corrupt=Buffer.from(good);corrupt[35]^=1;let s=source(corrupt);const [entry]=await readDirectory(s,signal());await assert.rejects(readEntry(s,entry,signal()),{code:'archive_invalid'});
  const renamed=Buffer.from(good);renamed[30]=120;s=source(renamed);await assert.rejects(readEntry(s,(await readDirectory(s,signal()))[0],signal()),{code:'archive_invalid'});
  await assert.rejects(readDirectory(source(good.subarray(0,-1)),signal()),{code:'archive_invalid'});
  await assert.rejects(readDirectory(source(Buffer.concat([good,Buffer.from([0])])),signal()),{code:'archive_invalid'});
  const inexact=source(good),read=inexact.read;inexact.read=async(range,sig)=>Buffer.concat([await read(range,sig),Buffer.from([0])]);
  await assert.rejects(readDirectory(inexact,signal()),{code:'archive_invalid'});
  const overlap=Buffer.from(good);overlap.writeUInt16LE(65000,28);s=source(overlap);await assert.rejects(readEntry(s,(await readDirectory(s,signal()))[0],signal()),{code:'archive_invalid'});
});
test('path traversal, duplicate normalized names, encryption and ZIP64 are refused',async()=>{
  for(const name of ['../a.png','/a.png','C:/a.png','a\\b.png'])await assert.rejects(readDirectory(source(zip([{name,bytes:png}])),signal()));
  await assert.rejects(readDirectory(source(zip([{name:'a.png',bytes:png},{name:'A.PNG',bytes:png}])),signal()));
  const encrypted=zip([{name:'a.png',bytes:png}]);encrypted.writeUInt16LE(1,encrypted.length-22-46-5+8);await assert.rejects(readDirectory(source(encrypted),signal()));
  const zip64=zip([{name:'a.png',bytes:png}]);zip64.writeUInt16LE(65535,zip64.length-22+10);await assert.rejects(readDirectory(source(zip64),signal()));
});
test('declared expansion limits and lying DEFLATE sizes are bounded before allocation',async()=>{
  const data=zip([{name:'a.png',bytes:Buffer.alloc(10000),deflate:true}]);await assert.rejects(readDirectory(source(data),signal(),{...archiveLimits,ratio:2}),{code:'archive_limit'});
  const lie=Buffer.from(data),central=lie.length-22-46-5;lie.writeUInt32LE(1,22);lie.writeUInt32LE(1,central+24);const s=source(lie),[entry]=await readDirectory(s,signal());await assert.rejects(readEntry(s,entry,signal()),{code:'archive_limit'});
});
test('directory and selected entry cancellation reaches the range provider; disposal cancels pending work',async()=>{
  const abort=new AbortController();abort.abort();await assert.rejects(openComicArchive(source(zip([{name:'a.png',bytes:png}])),abort.signal),{name:'AbortError'});
  const s=source(zip([{name:'a.png',bytes:png}])),archive=await openComicArchive(s,signal());
  let started!:()=>void;const ready=new Promise<void>(r=>{started=r;});s.read=async(_range,sig)=>{started();return new Promise((_r,reject)=>sig.addEventListener('abort',()=>reject(sig.reason),{once:true}));};
  const pending=archive.page(0,{width:300,signal:signal()});await ready;archive.dispose();await assert.rejects(pending,{name:'AbortError'});
});
test('CRC matches independent native implementation and natural order handles long numbers',()=>{
  assert.equal(crc32(png),nativeCRC(png));assert.deepEqual(['p10','p2','p01','p1'].sort(naturalCompare),['p01','p1','p2','p10']);assert.ok(naturalCompare('p99999999999999999999','p100000000000000000000')<0);
});
test('image sniffing and XML metadata reject active markup, bad signatures and huge rasters',()=>{
  assert.throws(()=>rasterInfo(Buffer.from('<svg onload="alert(1)"/>')),{code:'image_invalid'});
  const huge=Buffer.from(png);huge.writeUInt32BE(100000,16);assert.throws(()=>rasterInfo(huge),{code:'image_limit'});
  assert.throws(()=>comicInfo(Buffer.from('<!DOCTYPE ComicInfo [<!ENTITY x SYSTEM "file:///secret">]><ComicInfo/>')),{code:'archive_invalid'});
});
test('source retirement between directory and metadata cannot publish stale archive identity',async()=>{
  const input=source(zip([{name:'page.png',bytes:png},{name:'ComicInfo.xml',bytes:Buffer.from('<ComicInfo><Title>Old</Title></ComicInfo>')}])),read=input.read;let calls=0;
  input.read=(range,sig)=>{if(++calls!==1)return read(range,sig);
    return new Promise((resolve,reject)=>read(range,sig).then(bytes=>{resolve(bytes);queueMicrotask(()=>{input.revision='changed';});},reject));
  };
  await assert.rejects(openComicArchive(input,signal()),{code:'archive_invalid'});
});
test('independent comic archive readers retain separate retirement lifecycles',async()=>{
  const input=source(zip([{name:'page.png',bytes:png}])),a=await openComicArchive(input,signal()),b=await openComicArchive(input,signal());
  a.dispose();a.dispose();await assert.rejects(a.page(0,{width:300,signal:signal()}),{code:'closed'});
  assert.deepEqual(Buffer.from((await b.page(0,{width:300,signal:signal()})).bytes),png);
  b.dispose();await assert.rejects(b.page(0,{width:300,signal:signal()}),{code:'closed'});
});
test('changed source revision and an absent binary reader cannot splice CBZ pages',async()=>{
  const input=source(zip([{name:'a.png',bytes:png}])),archive=await openComicArchive(input,signal());input.revision='changed';await assert.rejects(archive.page(0,{width:300,signal:signal()}),{code:'archive_invalid'});archive.dispose();
  await assert.rejects(openComicArchive({...input,read:undefined},signal()),{code:'archive_invalid'});
});
test('online comics use only the authorised page provider, fence identity and validate preview bounds before I/O',async()=>{
  const source={id:'online-comic',revision:'v1',size:10000};let reads=0,disposed=0;
  const adapter=createComicAdapter({pages:async()=>({id:source.id,revision:source.revision,pageCount:1,metadata:{},async page(index,{signal:taskSignal}){taskSignal.throwIfAborted();reads++;return {index,name:'page.png',bytes:png,image:rasterInfo(png)};},dispose(){disposed++;}})});
  const document=await adapter.open({source,signal:signal()});
  await assert.rejects(document.page!(-1,{width:300,signal:signal()}),{code:'page_invalid'});assert.equal(reads,0);
  const preview=await document.page!(0,{width:300,signal:signal()});assert.equal(preview.mime,'image/png');assert.equal(preview.width,1);assert.notEqual(preview.bytes,png);assert.equal(reads,1);
  source.size++;await assert.rejects(document.page!(0,{width:300,signal:signal()}),{code:'archive_invalid'});assert.equal(reads,1);
  await document.dispose();await document.dispose();assert.equal(disposed,1);
  await assert.rejects(createComicAdapter().open({source,signal:signal()}),/comic_provider_required/);
});

// CBR format selection never enables a local RAR decoder or a ZIP fallback, UNRUN.
test('CBR adapter requires an explicit raster provider even when localArchive is requested',async()=>{
  const input=source(new Uint8Array([0x52,0x61,0x72,0x21]));
  await assert.rejects(createComicAdapter({format:'cbr',localArchive:true}).open({source:input,signal:signal()}),/comic_provider_required/);
  assert.deepEqual(input.reads,[]);
  const adapter=createComicAdapter({format:'cbr',pages:async()=>({id:input.id,revision:input.revision,pageCount:1,metadata:{direction:'rtl'},
    async page(index){return {index,name:'page.png',bytes:png,image:rasterInfo(png)};},dispose(){}})});
  const document=await adapter.open({source:input,signal:signal()});
  try{assert.equal(adapter.format,'cbr');assert.equal(document.format,'cbr');assert.equal((await document.page!(0,{width:320,signal:signal()})).mime,'image/png');assert.deepEqual(input.reads,[]);}
  finally{await document.dispose();}
});
