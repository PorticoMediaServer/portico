import {createEpubAdapter,createReadingController,loadLiterataFont,sourceFromBlob} from '../../src/index.ts';
import {parsePreferenceSnapshot,readingPreferences,readingPreferencesPatch,type PreferenceSnapshot,type ReadingPreferencesChange} from '@portico/client-core';
import bootstrap from '../../../../server/testdata/contracts/get-bootstrap.json';
import manropeUrl from '../../../../web/src/kit/fonts/manrope-var-latin.woff2?url';
import {sha256} from '@noble/hashes/sha2.js';

// Verification fixture only: these are controlled confirmed snapshots of the
// actual recorded registry, not HTTP saves or a product Settings store.
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

async function run() {
  const checks:string[]=[],errors:string[]=[],surface=document.querySelector<HTMLElement>('#surface')!;
  let snapshot=parsePreferenceSnapshot(bootstrap.response.preferences);
  const book=await fetch('/publication.epub',{credentials:'omit'}).then(response=>{assert(response.ok,'lawful fixture response');return response.blob();});
  const revision=Array.from(sha256(new Uint8Array(await book.arrayBuffer())),byte=>byte.toString(16).padStart(2,'0')).join('');
  const [literata,manrope]=await Promise.all([loadLiterataFont(),fetch(manropeUrl,{credentials:'omit'}).then(response=>response.blob())]);
  const reader=createReadingController({adapters:[createEpubAdapter({fonts:{literata,manrope}})],preferenceSnapshot:snapshot});
  const options={source:sourceFromBlob(book,'lawful-gatsby-fixture',revision),format:'epub' as const,container:surface,viewport:{width:1180,height:820}};
  type Paginator=HTMLElement&{getContents():{doc:Document}[]};
  const observed=async()=>{
    await new Promise<void>(resolve=>requestAnimationFrame(()=>requestAnimationFrame(()=>resolve())));
    const paginator=document.querySelector<Paginator>('foliate-paginator')!;
    const doc=paginator.getContents()[0]?.doc;assert(doc?.body,'actual EPUB document');await doc.fonts.ready;
    const style=doc.defaultView!.getComputedStyle(doc.body),selection=doc.defaultView!.getComputedStyle(doc.body,'::selection');
    return {family:style.fontFamily,size:style.fontSize,line:style.lineHeight,alignment:style.textAlign,hyphens:style.hyphens,
      background:doc.defaultView!.getComputedStyle(doc.documentElement).backgroundColor,highlight:selection.backgroundColor,
      flow:paginator.getAttribute('flow'),columns:paginator.getAttribute('max-column-count'),width:paginator.getAttribute('max-inline-size'),
      literataLoaded:doc.fonts.check('20px Literata'),manropeLoaded:doc.fonts.check('20px Manrope'),text:doc.body.textContent??''};
  };
  const update=async(change:ReadingPreferencesChange)=>{snapshot=confirmed(snapshot,change);await reader.updatePreferenceSnapshot(snapshot);return observed();};
  try {
    await reader.open(options);assert(reader.getSnapshot().status==='ready','real EPUB opened through canonical snapshot factory');
    const results=[];for await(const hit of reader.search('green light',{signal:new AbortController().signal,limit:10}))results.push(hit);
    assert(results.length>0,'actual Gatsby text/CFI search');await reader.navigate({kind:'location',location:results[0].location});
    const initial=await observed();assert(initial.text.length>100,'actual chapter text');assert(initial.literataLoaded,'official Literata loaded');checks.push('actual lawful Gatsby text/CFI and pinned Literata through canonical Reading15 factory');
    let before=initial,after=await update({theme:'sepia'});assert(after.background!==before.background,'theme effect');checks.push('theme changes actual book background');
    before=after;after=await update({font:'manrope'});assert(after.family!==before.family&&after.manropeLoaded,'font effect');checks.push('font changes actual loaded book FontFace');
    before=after;after=await update({textSize:7});assert(after.size!==before.size,'text size effect');checks.push('text size changes actual computed font size');
    before=after;after=await update({lineSpacing:'loose'});assert(after.line!==before.line,'line spacing effect');checks.push('line spacing changes actual computed line height');
    before=after;after=await update({margins:'wide'});assert(after.width!==before.width,'margin effect');checks.push('margins change actual paginator content width');
    after=await update({alignment:'left'});assert(after.alignment==='left','alignment effect');checks.push('alignment changes actual chapter computed style');
    after=await update({hyphenation:false});assert(after.hyphens==='none','hyphenation effect');checks.push('hyphenation disables actual chapter CSS hyphens');
    before=after;after=await update({highlightColour:'pink'});assert(after.highlight!==before.highlight,'highlight effect');checks.push('highlight colour changes actual selection computed style');
    after=await update({twoPages:false});assert(after.columns==='1','two pages effect');checks.push('two-pages choice changes actual paginator columns');
    after=await update({layout:'scroll'});assert(after.flow==='scrolled','scroll effect');checks.push('layout changes actual renderer to scrolled flow');
    await update({tapToTurn:false});assert(reader.getSnapshot().preferences.tapToTurn===false,'tap preference propagation');checks.push('tap-to-turn reaches canonical snapshot; actual chrome turn gating remains web-owned');
    await reader.setOverride({theme:'black',textSize:6});snapshot=confirmed(snapshot,{theme:'light',textSize:2,font:'literata'});await reader.updatePreferenceSnapshot(snapshot);
    after=await observed();assert(reader.getSnapshot().preferences.theme==='black'&&reader.getSnapshot().preferences.textSize===6,'book override precedence');assert(after.family.includes('Literata'),'unoverridden live font');checks.push('actual book-local theme/size overrides retain precedence while unoverridden live font applies');
    await reader.close();assert(surface.children.length===0,'close clears DOM');assert(reader.getSnapshot().overriddenKeys.length===0,'close clears overrides');
    await reader.open(options);assert(reader.getSnapshot().preferences.theme===readingPreferences(snapshot).theme&&reader.getSnapshot().preferences.textSize===2,'new book reads latest confirmed snapshot');checks.push('close resets overrides and next open reads latest confirmed Settings snapshot');
    return {status:'pass',checks,hits:results.length,sourceRevision:revision,environment:{origin:location.origin,isSecureContext,hasSubtle:!!globalThis.crypto?.subtle},qualification:'Actual EPUB effects with controlled confirmed snapshots from real recorded registry. No HTTP save, public Read authorization, full chrome/frame or hardware acceptance.'};
  }catch(error){errors.push(String(error));throw error;}
  finally{await reader.close();(window as unknown as {readerCleanup:unknown}).readerCleanup={surfaceChildren:surface.children.length,overrides:reader.getSnapshot().overriddenKeys.length,errors};}
}
(window as unknown as {snapshotProof:Promise<unknown>}).snapshotProof=run();
