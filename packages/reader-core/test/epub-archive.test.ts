import assert from 'node:assert/strict';
import {test} from 'node:test';
import {ZipWriter, Uint8ArrayWriter, TextReader} from '@zip.js/zip.js';
import {openEpubArchive,safeArchivePath} from '../src/epub/archive.ts';
import {sourceFromBlob} from '../src/source.ts';
const container='<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="book.opf" media-type="application/oebps-package+xml"/></rootfiles></container>';
async function zip(entries:Record<string,string>,password?:string) {
  const writer=new ZipWriter(new Uint8ArrayWriter(),{useWebWorkers:false});
  await writer.add('mimetype',new TextReader('application/epub+zip'),{level:0});
  await writer.add('META-INF/container.xml',new TextReader(container));
  for(const [path,text]of Object.entries(entries))await writer.add(path,new TextReader(text),{password});
  return await writer.close();
}
test('EPUB ZIP range loader checks actual CRC/content and closes/cancels deterministically',async()=>{
  const bytes=await zip({'chapter.xhtml':'<html xmlns="http://www.w3.org/1999/xhtml"><body>Actual test content</body></html>'});
  const source=sourceFromBlob(new Blob([bytes]),'local-test','revision-1');
  const abort=new AbortController();const archive=await openEpubArchive(source,abort.signal);
  assert.match(new TextDecoder().decode(await archive.read('chapter.xhtml',abort.signal)),/Actual test content/);
  assert.deepEqual([...await archive.read('missing.xhtml',abort.signal)],[]);
  await archive.dispose();await assert.rejects(archive.read('chapter.xhtml',abort.signal));
});
test('authored EPUB preserves exact OPF and navigation resources for the package parser',async()=>{
  const opf=`<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="book-id">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="book-id">urn:uuid:5c1f6f0e-6f6a-4f3e-9d2a-2f4f6c0d9e21</dc:identifier>
    <dc:title>Authored OPF &amp; Navigation</dc:title>
    <dc:creator>Ada Lovelace</dc:creator>
    <dc:language>en</dc:language>
    <dc:language>fr</dc:language>
  </metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="chapter1" href="chapter1.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine page-progression-direction="rtl"><itemref idref="chapter1"/></spine>
</package>`;
  const nav=`<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
  <head><title>Contents</title></head>
  <body><nav epub:type="toc"><ol><li><a href="chapter1.xhtml">Chapter &amp; One</a></li></ol></nav></body>
</html>`;
  const bytes=await zip({'book.opf':opf,'nav.xhtml':nav,'chapter1.xhtml':'<html xmlns="http://www.w3.org/1999/xhtml"><body>Authored chapter</body></html>'});
  const archive=await openEpubArchive(sourceFromBlob(new Blob([bytes]),'authored-opf','revision-1'),new AbortController().signal);
  try {
    assert.deepEqual(archive.entries.filter(entry=>entry.path==='book.opf'||entry.path==='nav.xhtml').map(entry=>entry.path),['book.opf','nav.xhtml']);
    assert.equal(new TextDecoder().decode(await archive.read('book.opf',new AbortController().signal)),opf);
    assert.equal(new TextDecoder().decode(await archive.read('nav.xhtml',new AbortController().signal)),nav);
  }finally{await archive.dispose();}
});
test('EPUB rejects traversal/aliases, encrypted entries, truncated ZIP and declared bombs',async()=>{
  for(const path of ['../outside','/absolute','a\\b','a//b','a/./b','a/%2e%2e/b','a?token=x'])assert.throws(()=>safeArchivePath(path));
  const hostile=await zip({'../outside':'unsafe'});
  await assert.rejects(openEpubArchive(sourceFromBlob(new Blob([hostile]),'test','1'),new AbortController().signal));
  const encrypted=await zip({'secret.xhtml':'secret'},'test-only-password');
  await assert.rejects(openEpubArchive(sourceFromBlob(new Blob([encrypted]),'test','1'),new AbortController().signal),{code:'protected'});
  const bytes=await zip({'chapter.xhtml':'text'});
  await assert.rejects(openEpubArchive(sourceFromBlob(new Blob([bytes.subarray(0,bytes.length-2)]),'test','1'),new AbortController().signal));
  const bomb=await zip({'bomb.xhtml':'a'.repeat(1024*1024)});
  await assert.rejects(openEpubArchive(sourceFromBlob(new Blob([bomb]),'test','1'),new AbortController().signal),{code:'limit_exceeded'});
});
test('queued EPUB extraction cancellation rejects before provider IO without retiring the archive',async()=>{
  const bytes=await zip({'chapter.xhtml':'<html xmlns="http://www.w3.org/1999/xhtml"><body>Actual queued content</body></html>'});
  const base=sourceFromBlob(new Blob([bytes]),'local-test','revision-1');let gate:Promise<void>|undefined,releaseGate!:()=>void,started!:()=>void,reads=0;
  const entered=new Promise<void>(done=>started=done);
  const source={...base,async read(range:import('../src/ports.ts').ReaderRange,signal:AbortSignal){if(gate){reads++;started();await gate;signal.throwIfAborted();}return base.read!(range,signal);}};
  const lifetime=new AbortController(),archive=await openEpubArchive(source,lifetime.signal);
  const hold=new Promise<void>(done=>releaseGate=done);gate=hold;
  const a=archive.read('chapter.xhtml',new AbortController().signal);await entered;
  const bAbort=new AbortController(),signal=bAbort.signal,add=signal.addEventListener.bind(signal),remove=signal.removeEventListener.bind(signal);let added=0,removed=0;
  signal.addEventListener=(...args)=>{added++;return add(...args);};signal.removeEventListener=(...args)=>{removed++;return remove(...args);};
  const b=archive.read('chapter.xhtml',signal),readsBefore=reads;bAbort.abort();
  try {
    const outcome=await Promise.race([b.then(()=> 'resolved',error=>error),new Promise(resolve=>setTimeout(()=>resolve('timeout'),100))]);
    assert.equal(outcome instanceof Error?(outcome as {code?:string}).code:outcome,'aborted');
    assert.equal(reads,readsBefore);assert.equal(added,1);assert.equal(removed,1);
    releaseGate();assert.match(new TextDecoder().decode(await a),/Actual queued content/);
    assert.match(new TextDecoder().decode(await archive.read('chapter.xhtml',new AbortController().signal)),/Actual queued content/);
  }finally{releaseGate();await a.catch(()=>{});await archive.dispose();}
});
test('cancelling one extraction aborts its actual range request without retiring the archive',async()=>{
  const bytes=await zip({'chapter.xhtml':'<html xmlns="http://www.w3.org/1999/xhtml"><body>Actual test content</body></html>'});
  const base=sourceFromBlob(new Blob([bytes]),'local-test','revision-1');let hold=false,entered!:()=>void;
  const started=new Promise<void>(resolve=>{entered=resolve;});
  const source={...base,async read(range:import('../src/ports.ts').ReaderRange,signal:AbortSignal){
    if(!hold)return base.read!(range,signal);entered();
    return new Promise<Uint8Array>((_,reject)=>{const stop=()=>reject(new DOMException('Closed','AbortError'));if(signal.aborted)stop();else signal.addEventListener('abort',stop,{once:true});});
  }};
  const lifetime=new AbortController(),archive=await openEpubArchive(source,lifetime.signal),caller=new AbortController();
  hold=true;const read=archive.read('chapter.xhtml',caller.signal),rejected=assert.rejects(read);
  await started;caller.abort();await rejected;assert.equal(lifetime.signal.aborted,false);
  hold=false;assert.match(new TextDecoder().decode(await archive.read('chapter.xhtml',lifetime.signal)),/Actual test content/);
  await archive.dispose();
});
