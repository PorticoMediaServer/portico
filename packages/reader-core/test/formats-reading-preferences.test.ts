import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {parsePreferenceSnapshot, readingPreferences, readingPreferencesPatch,
  type ReadingPreferences} from '@portico/client-core';
import {ReaderController, createComicAdapter, createPdfAdapter, rasterInfo,
  type ComicPageProvider} from '@portico/reader-core';

// The committed server registry supplies every default and domain. This is
// typed preference wiring, not a live preference transport or renderer proof.
const raw=JSON.parse(readFileSync(new URL('../../../server/testdata/contracts/get-bootstrap.json',import.meta.url),'utf8')).response.preferences;
const account=parsePreferenceSnapshot(raw);
const initial:ReadingPreferences=readingPreferences(account);

test('canonical Reading15 feeds public PDF and CBZ factories without a second preference model',()=>{
  const controller=new ReaderController<ReadingPreferences>({
    adapters:[createPdfAdapter<ReadingPreferences>(),createComicAdapter<ReadingPreferences>()],preferences:initial
  });
  assert.equal(Object.keys(initial).length,15);
  assert.ok(Object.isFrozen(initial));
  assert.deepEqual(controller.getSnapshot().preferences,initial);
  assert.deepEqual(readingPreferencesPatch(account,{pdfView:null,comicFit:'width',twoPages:false}),{
    'reading.pdfView':null,'comics.fit':'width','reading.twoPages':false
  });
  assert.deepEqual(readingPreferences(account),initial,'a Settings patch must not mutate its input snapshot');
});

test('canonical Reading15 keeps book overrides temporary while incoming facade values stay live before mount',async()=>{
  const png=Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAAC0lEQVR42mNgAAIAAAUAAarVyFEAAAAASUVORK5CYII=','base64');
  const source={id:'canonical-reading-fixture',revision:'opaque-fixture-revision',size:png.length};
  let started!:()=>void,release!:()=>void,disposed=0;
  const entered=new Promise<void>(resolve=>{started=resolve;});
  const pending=new Promise<void>(resolve=>{release=resolve;});
  let sourceSignal:AbortSignal|undefined;
  const provider:ComicPageProvider={id:source.id,revision:source.revision,pageCount:1,metadata:{},
    async page(index,{signal}){signal.throwIfAborted();return {index,name:'page.png',bytes:png,image:rasterInfo(png)};},
    dispose(){disposed++;}
  };
  const controller=new ReaderController<ReadingPreferences>({
    adapters:[createPdfAdapter<ReadingPreferences>(),createComicAdapter<ReadingPreferences>({pages:async(_source,signal)=>{
      sourceSignal=signal;started();await pending;return provider;
    }})],preferences:initial
  });
  // Hold the actual adapter at its provider boundary: no DOM/rendering, public
  // source endpoint, authenticated session or persisted Settings is simulated.
  const opening=controller.open({source,format:'cbz',container:{} as HTMLElement,viewport:{width:1180,height:820}});
  await entered;
  const incoming:ReadingPreferences=readingPreferences({...account,effective:{...account.effective,
    'reading.theme':'sepia','reading.pdfView':'width','reading.twoPages':true,
    'comics.fit':'height','comics.direction':'rtl','comics.firstPageAlone':false
  }});
  try{
    await controller.setOverride({twoPages:false,comicDirection:'vertical'});
    await controller.updatePreferences(incoming);
    const current=controller.getSnapshot().preferences;
    assert.equal(current.twoPages,false);
    assert.equal(current.comicDirection,'vertical');
    assert.equal(current.comicFit,'height');
    assert.equal(current.pdfView,'width');
    assert.equal(current.firstPageAlone,false);
    assert.equal(current.theme,'sepia');
    assert.deepEqual([...controller.getSnapshot().overriddenKeys].sort(),['comicDirection','twoPages']);
    await controller.clearOverrides(['comicDirection']);
    assert.equal(controller.getSnapshot().preferences.comicDirection,'rtl');
    assert.deepEqual(readingPreferencesPatch(account,{comicDirection:null}),{'comics.direction':null});
    assert.deepEqual(readingPreferences(account),initial);
  }finally{
    await controller.close();release();await opening;
  }
  assert.equal(sourceSignal?.aborted,true);
  assert.equal(disposed,1);
  assert.deepEqual(controller.getSnapshot().overriddenKeys,[]);
  assert.deepEqual(controller.getSnapshot().preferences,incoming);
  assert.deepEqual(new ReaderController<ReadingPreferences>({adapters:[createPdfAdapter<ReadingPreferences>(),createComicAdapter<ReadingPreferences>()],preferences:initial}).getSnapshot().preferences,initial);
});
