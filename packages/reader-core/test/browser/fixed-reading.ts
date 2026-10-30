import {createPdfAdapter,createComicAdapter,createReadingController,sourceFromBlob} from '../../src/index.ts';
import {parsePreferenceSnapshot,readingPreferencesPatch,type PreferenceSnapshot,type ReadingPreferencesChange} from '@portico/client-core';
import bootstrap from '../../../../server/testdata/contracts/get-bootstrap.json';
import workerUrl from 'pdfjs-dist/legacy/build/pdf.worker.mjs?url';
import {sha256} from '@noble/hashes/sha2.js';

// Controlled confirmations of the recorded server registry. No product route,
// account writer, HTTP save or online-archive fallback is supplied by this test.
function confirmed(before:PreferenceSnapshot,change:ReadingPreferencesChange):PreferenceSnapshot {
  const patch=readingPreferencesPatch(before,change),effective={...before.effective},effectiveSource={...before.effectiveSource};
  const document=before.documents.find(d=>d.scope==='profile-server')!,values={...document.values};
  for(const[key,value]of Object.entries(patch)) {
    if(value===null){delete values[key];effective[key]=before.registry.fields.find(f=>f.key===key)!.default;effectiveSource[key]='default';}
    else{values[key]=value;effective[key]=value;effectiveSource[key]='profile-server';}
  }
  return parsePreferenceSnapshot({...before,effective,effectiveSource,documents:before.documents.map(d=>d.scope==='profile-server'
    ?{...d,values,revision:d.revision+1,activeRevision:d.revision+1}:d)});
}
function assert(value:unknown,message:string):asserts value {if(!value)throw Error(message);}
async function fixture(path:string,id:string) {
  const response=await fetch(path,{credentials:'omit'});assert(response.ok,'lawful local fixture response');const blob=await response.blob();
  const revision=Array.from(sha256(new Uint8Array(await blob.arrayBuffer())),b=>b.toString(16).padStart(2,'0')).join('');
  return sourceFromBlob(blob,id,revision);
}

async function run() {
  const checks:string[]=[],surface=document.querySelector<HTMLElement>('#surface')!,viewport={width:1180,height:820};
  let snapshot=parsePreferenceSnapshot(bootstrap.response.preferences),workers=0,terminated=0;
  const reader=createReadingController({preferenceSnapshot:snapshot,adapters:[createPdfAdapter({workerUrl,createWorker(url){
    workers++;const worker=new Worker(url,{type:'module'}),terminate=worker.terminate.bind(worker);worker.terminate=()=>{terminated++;terminate();};return worker;
  }}),createComicAdapter({localArchive:true})]});
  const pages=()=>Array.from(surface.querySelectorAll<HTMLElement>('[data-reader-page]'),element=>Number(element.dataset.readerPage));
  const dimensions=()=>{const page=surface.querySelector<HTMLElement>('[data-reader-page]')!;assert(page,'actual rendered page');return page.getBoundingClientRect();};
  const update=async(change:ReadingPreferencesChange)=>{snapshot=confirmed(snapshot,change);await reader.updatePreferenceSnapshot(snapshot);};
  try {
    const pdf=await fixture('/fixture.pdf','lawful-gpo-pdf');
    await reader.open({source:pdf,format:'pdf',container:surface,viewport});assert(reader.getSnapshot().status==='ready','real GPO PDF ready');
    assert(reader.getSnapshot().metadata?.pageCount===85,'actual PDF parser count');checks.push('canonical snapshot factory renders the real85-page GPO PDF with a bundled native worker');
    await update({twoPages:false,pdfView:'width'});const width=dimensions();assert(reader.getZoom()?.fit==='width'&&Math.abs(width.width-viewport.width)<2,'actual PDF fit width');
    await update({pdfView:'page'});const page=dimensions();assert(reader.getZoom()?.fit==='page'&&page.height<=viewport.height+1&&page.width<width.width,'actual PDF fit page');checks.push('pdfView changes actual width/page render geometry');
    await update({twoPages:true,pdfView:'spread'});await reader.navigate({kind:'location',location:{format:'pdf',page:1,progression:1/84}});
    assert(pages().length===2,'actual PDF spread');await update({twoPages:false});assert(pages().length===1,'actual PDF single-page setting');checks.push('twoPages changes actual PDF page surfaces');
    await reader.setOverride({pdfView:'page'});await update({pdfView:'width'});assert(reader.getZoom()?.fit==='page','local PDF view retains precedence');
    await reader.close();await reader.open({source:pdf,format:'pdf',container:surface,viewport});assert(reader.getZoom()?.fit==='width','PDF local view resets on close');checks.push('PDF view overrides stay book-local while confirmed Settings remain live for next open');
    await reader.close();assert(workers===terminated,'every PDF worker retired');

    const cbz=await fixture('/fixture.cbz','lawful-local-nemo-cbz');await update({twoPages:true,firstPageAlone:true,comicDirection:'ltr',comicFit:'auto'});
    await reader.open({source:cbz,format:'cbz',container:surface,viewport});assert(reader.getSnapshot().status==='ready','real local CBZ ready');
    assert((reader.getSnapshot().metadata?.pageCount??0)>=3&&pages().length===1,'actual first page alone');
    await update({firstPageAlone:false});assert(pages().length===2,'actual first-page spread');checks.push('firstPageAlone changes actual CBZ cover grouping');
    await update({twoPages:false});assert(pages().length===1,'actual CBZ single page');await update({twoPages:true});assert(pages().length===2,'actual CBZ spread');checks.push('twoPages changes actual CBZ page surfaces');
    const left=pages();await update({comicDirection:'rtl'});assert(pages().join(',')===[...left].reverse().join(','),'actual RTL visual order');
    await update({comicDirection:'vertical'});const elements=Array.from(surface.querySelectorAll<HTMLElement>('[data-reader-page]'));
    assert(elements.length>=2&&elements.length<=4&&elements[1].getBoundingClientRect().top>elements[0].getBoundingClientRect().top,'actual vertical pages');checks.push('comicDirection changes real RTL order and bounded vertical stacking');
    await update({comicDirection:'ltr',twoPages:false,comicFit:'width'});const comicWidth=dimensions();assert(Math.abs(comicWidth.width-viewport.width)<2,'actual comic width fit');
    await update({comicFit:'height'});const comicHeight=dimensions();assert(Math.abs(comicHeight.height-viewport.height)<2&&comicHeight.width<comicWidth.width,'actual comic height fit');checks.push('comicFit changes actual raster render dimensions');
    await reader.setOverride({comicDirection:'rtl'});await update({comicDirection:'ltr',comicFit:'width',twoPages:true,firstPageAlone:false});assert(pages()[0]>pages()[1],'local direction retained');assert(reader.getZoom()?.fit==='width','unoverridden fit applies live');
    await reader.close();await reader.open({source:cbz,format:'cbz',container:surface,viewport});assert(pages()[0]<pages()[1]&&reader.getSnapshot().overriddenKeys.length===0,'local comic direction resets on close');checks.push('comic local overrides retain precedence, unoverridden Settings apply, and close resets overrides');
    // foldable6/tablet-read3: a real book crease splits the pages into physical panes,
    // a zoomed page stays inside its own pane, and pan reaches its corners.
    await reader.close();await update({twoPages:true,firstPageAlone:false,comicDirection:'ltr',comicFit:'auto'});
    const bookViewport={width:890,height:626,crease:{left:445,right:445}};
    await reader.open({source:cbz,format:'cbz',container:surface,viewport:bookViewport});
    let clips=Array.from(surface.querySelectorAll<HTMLElement>('[data-reader-page]'));assert(clips.length===2,'crease shows two pane surfaces');
    let [leftPane,rightPane]=clips,lb=leftPane.getBoundingClientRect(),rb=rightPane.getBoundingClientRect();
    assert(Math.abs(lb.left)<1&&Math.abs(lb.width-445)<1,'left page pane starts at zero and ends at the crease');
    assert(Math.abs(rb.left-445)<1&&Math.abs(rb.width-445)<1,'right page pane starts after the crease');
    assert(getComputedStyle(leftPane).overflow==='auto','pane is its own scroll boundary');
    await reader.setZoom({kind:'scale',value:2});
    // Zoom replaces the DOM: re-query so every assertion uses the live panes.
    clips=Array.from(surface.querySelectorAll<HTMLElement>('[data-reader-page]'));[leftPane,rightPane]=clips;
    const pageElement=leftPane.firstElementChild?.firstElementChild as HTMLElement|undefined;
    assert(!!pageElement&&pageElement.getBoundingClientRect().width>leftPane.clientWidth,'zoomed page is wider than its pane');
    assert(leftPane.scrollWidth>leftPane.clientWidth,'the pane scrolls the oversized page');
    const fixed=leftPane.getBoundingClientRect();
    await reader.pan({x:leftPane.scrollWidth,y:leftPane.scrollHeight});
    assert(leftPane.scrollLeft>0&&leftPane.scrollTop>0,'pan reaches the page corners inside the pane');
    assert(Math.abs(leftPane.getBoundingClientRect().left-fixed.left)<1&&Math.abs(leftPane.getBoundingClientRect().height-fixed.height)<1,'the pane stays fixed at the crease while its content pans');
    // Dragging the other visible page scrolls that pane, not the logical current page.
    clips=Array.from(surface.querySelectorAll<HTMLElement>('[data-reader-page]'));[leftPane,rightPane]=clips;
    const leftBefore=leftPane.scrollLeft,rightBox=rightPane.getBoundingClientRect();
    rightPane.dispatchEvent(new PointerEvent('pointerdown',{bubbles:true,pointerId:7,pointerType:'touch',clientX:rightBox.left+80,clientY:rightBox.top+80}));
    rightPane.dispatchEvent(new PointerEvent('pointermove',{bubbles:true,pointerId:7,pointerType:'touch',clientX:rightBox.left+40,clientY:rightBox.top+40}));
    rightPane.dispatchEvent(new PointerEvent('pointerup',{bubbles:true,pointerId:7,pointerType:'touch',clientX:rightBox.left+40,clientY:rightBox.top+40}));
    assert(rightPane.scrollLeft>0&&leftPane.scrollLeft===leftBefore,'dragging the second page moves only its own pane');
    await update({comicDirection:'rtl'});const visual=pages();assert(visual[0]>visual[1],'RTL keeps the later page on the physical left');
    checks.push('a book crease splits physical panes, zoom is panned inside its pane, and RTL keeps one reversal');
    await reader.close();
    return {status:'pass',checks,pdfRevision:pdf.revision,comicRevision:cbz.revision,workers,terminated,qualification:'Real lawful local/offline files through canonical confirmed-snapshot factory; no public Read authorization or full-screen/frame/hardware acceptance.'};
  }finally{await reader.close();(window as unknown as {readerCleanup:unknown}).readerCleanup={surfaceChildren:surface.children.length,overrides:reader.getSnapshot().overriddenKeys.length,workers,terminated};}
}
(window as unknown as {snapshotProof:Promise<unknown>}).snapshotProof=run();
