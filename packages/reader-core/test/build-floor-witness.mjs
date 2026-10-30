import {build} from 'vite';
import {readFile,writeFile,mkdir,copyFile,rename} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
import {resolve,join} from 'node:path';
import {createHash} from 'node:crypto';
import {ZipReader,Uint8ArrayReader} from '@zip.js/zip.js';
import assert from 'node:assert/strict';

export async function buildFloorWitness(output,pdfPath,comicPath) {
// Build output only: no checkout, credentials, routes or retained app state.
assert(output&&pdfPath&&comicPath,'output and lawful fixture paths required');
const out=resolve(output),root=fileURLToPath(new URL('./browser/',import.meta.url));
const pdf=await readFile(pdfPath),comic=await readFile(comicPath);
assert(pdf.length<16*1024*1024&&comic.length<8*1024*1024,'bounded checkpoint fixture files');
const zip=new ZipReader(new Uint8ArrayReader(comic),{useWebWorkers:false});
try{const entries=await zip.getEntries();assert(entries.some(entry=>!entry.directory&&entry.compressionMethod===8&&/\.jpe?g$/i.test(entry.filename)),'fixture must contain a genuinely DEFLATE-compressed real page');}finally{await zip.close();}
const worker=await readFile(fileURLToPath(import.meta.resolve('pdfjs-dist/legacy/build/pdf.worker.mjs')));
const fact=(bytes,mime)=>({bytes:bytes.length,sha256:createHash('sha256').update(bytes).digest('hex'),mime});
const manifest={pdfjsVersion:'6.4.299',pdf:fact(pdf,'application/pdf'),comic:fact(comic,'application/zip'),worker:fact(worker,'text/javascript')};
assert.equal(manifest.worker.sha256,'bb422f60804477de4a0b09091d8347a916a387755ed1cbad24ccaec1d09c78bf');
await build({root,base:'./',configFile:false,publicDir:false,build:{outDir:out,emptyOutDir:true,assetsInlineLimit:0,target:['ios15.1','safari15.1'],minify:false,
  rolldownOptions:{input:join(root,'floor.html')}}});
await rename(join(out,'floor.html'),join(out,'index.html'));
await mkdir(join(out,'fixtures'));await writeFile(join(out,'fixtures','manifest.json'),JSON.stringify(manifest));
await copyFile(pdfPath,join(out,'fixtures','publication.pdf'));await copyFile(comicPath,join(out,'fixtures','publication.cbz'));
const licences={};
for(const [name,path]of [['Portico','../../../LICENSE'],['PDF.js','../../../third_party/reader/pdfjs-LICENSE'],['zip.js','../../../third_party/reader/zipjs-LICENSE'],
  ['Foliate','../../../third_party/reader/foliate-LICENSE'],['DOMPurify','../../../third_party/reader/dompurify-LICENSE'],['Literata','../assets/OFL.txt']])
  licences[name]=await readFile(new URL(path,import.meta.url),'utf8');
licences['@noble/hashes']=await readFile(new URL('./LICENSE',import.meta.resolve('@noble/hashes/sha2.js')),'utf8');
await writeFile(join(out,'licences.json'),JSON.stringify(licences));
console.log(JSON.stringify({operation:'built-reader-floor-witness',output:out,target:'ios15.1/safari15.1',pdfjsVersion:manifest.pdfjsVersion,rawWorkerBytes:worker.length,fixtureBytes:pdf.length+comic.length,
  qualification:'Diagnostic asset graph for actual WKWebView execution; no native host or floor-runtime acceptance.'}));
}
if(process.argv[1]&&resolve(process.argv[1])===fileURLToPath(import.meta.url))
  await buildFloorWitness(process.argv[2],process.env.PORTICO_READER_PDF,process.env.PORTICO_READER_CBZ);
