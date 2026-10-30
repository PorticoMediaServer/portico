import {build,preview} from 'vite';
import {spawn} from 'node:child_process';
import {mkdtemp,readFile,rm} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
import {join} from 'node:path';
import assert from 'node:assert/strict';
import {buildFloorWitness} from './build-floor-witness.mjs';

// Remote verification only, driven through native CDP with no browser package,
// credentials, retained profile or product route/assembly replacement.
const scratch=await mkdtemp(join(process.env.TMPDIR,'reader-snapshot-'));
let server,chrome,socket;
try {
  const root=fileURLToPath(new URL('./browser/',import.meta.url));
  const fixed=process.env.PORTICO_READER_MODE==='fixed';
  const floor=process.env.PORTICO_READER_MODE==='floor';
  const retainedBuild=process.env.PORTICO_READER_BUILD_DIR;
  assert(!retainedBuild||floor,'only the explicit floor artifact may be supplied prebuilt');
  const built=retainedBuild??join(scratch,'dist');
  const fixtures=floor?{}:fixed?{'/fixture.pdf':{bytes:await readFile(process.env.PORTICO_READER_PDF),mime:'application/pdf'},'/fixture.cbz':{bytes:await readFile(process.env.PORTICO_READER_CBZ),mime:'application/zip'}}
    :{'/publication.epub':{bytes:await readFile(process.env.PORTICO_READER_EPUB),mime:'application/epub+zip'}};
  if(floor){if(!retainedBuild)await buildFloorWitness(built,process.env.PORTICO_READER_PDF,process.env.PORTICO_READER_CBZ);}
  else await build({root,configFile:false,publicDir:false,build:{outDir:join(scratch,'dist'),emptyOutDir:true,rolldownOptions:{input:join(root,fixed?'fixed.html':'index.html')}}});
  server=await preview({root,configFile:false,build:{outDir:built},preview:{host:'127.0.0.1',port:0},plugins:[{name:'lawful-verification-fixture',configurePreviewServer(value){value.middlewares.use((req,res,next)=>{
    const fixture=fixtures[req.url];if(fixture){res.setHeader('Content-Type',fixture.mime);res.setHeader('Content-Length',fixture.bytes.length);res.end(fixture.bytes);}else next();
  });}}]});
  const httpPort=server.httpServer.address().port;
  const profile=join(scratch,'chrome');
  chrome=spawn(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH??'/usr/bin/chromium',['--headless','--no-sandbox','--disable-gpu','--disable-dev-shm-usage','--no-first-run','--remote-debugging-port=0','--user-data-dir='+profile,'about:blank'],{stdio:['ignore','ignore','pipe']});
  let diagnostic='';chrome.stderr.on('data',data=>diagnostic+=data);
  const browserPort=await new Promise((resolve,reject)=>{
    const deadline=setTimeout(()=>reject(Error('Chromium startup timeout: '+diagnostic.slice(-2000))),15000);
    chrome.once('exit',code=>{clearTimeout(deadline);reject(Error('Chromium exited '+code+': '+diagnostic.slice(-2000)));});
    const parse=()=>{const match=/DevTools listening on ws:\/\/127\.0\.0\.1:(\d+)\//.exec(diagnostic);if(match){clearTimeout(deadline);chrome.stderr.off('data',parse);resolve(Number(match[1]));}};chrome.stderr.on('data',parse);parse();
  });
  const pages=await fetch(`http://127.0.0.1:${browserPort}/json/list`).then(response=>response.json());
  socket=new WebSocket(pages.find(page=>page.type==='page').webSocketDebuggerUrl);
  await new Promise((resolve,reject)=>{socket.addEventListener('open',resolve,{once:true});socket.addEventListener('error',reject,{once:true});});
  let id=0;const pending=new Map(),events=new Map();
  socket.addEventListener('message',event=>{const message=JSON.parse(event.data);if(message.id){const promise=pending.get(message.id);pending.delete(message.id);message.error?promise?.reject(Error(JSON.stringify(message.error))):promise?.resolve(message.result);}else{for(const resolve of events.get(message.method)??[])resolve(message.params);events.delete(message.method);}});
  const request=(method,params={})=>new Promise((resolve,reject)=>{const key=++id;pending.set(key,{resolve,reject});socket.send(JSON.stringify({id:key,method,params}));});
  await request('Page.enable');await request('Runtime.enable');
  const loaded=new Promise(resolve=>events.set('Page.loadEventFired',[resolve]));
  await request('Page.navigate',{url:`http://127.0.0.1:${httpPort}/${fixed?'fixed.html':''}`});await loaded;
  const outcome=await request('Runtime.evaluate',{expression:'window.snapshotProof.then(result=>({result,cleanup:window.readerCleanup}))',awaitPromise:true,returnByValue:true,timeout:floor?120000:60000});
  assert.ok(!outcome.exceptionDetails,JSON.stringify(outcome));
  const value=outcome.result.value;assert.equal(value.result.status,'pass',JSON.stringify(value));assert.equal(value.cleanup.surfaceChildren,0);assert.equal(value.cleanup.overrides,0);
  console.log(JSON.stringify(value));
}finally{
  socket?.close();
  if(chrome&&chrome.exitCode===null){const stopped=new Promise(resolve=>chrome.once('exit',resolve));chrome.kill('SIGTERM');const kill=setTimeout(()=>chrome.kill('SIGKILL'),3000);await stopped;clearTimeout(kill);}
  await new Promise(resolve=>server?server.httpServer.close(resolve):resolve());
  await rm(scratch,{recursive:true,force:true});
}
