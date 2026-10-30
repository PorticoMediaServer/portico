import assert from 'node:assert/strict';
import test from 'node:test';
import {exactPresetValue,presetPatch,readSettingsBinding,settingsBinding,settingsBindingPatch} from '../src/presentation/settings-bindings.ts';
import {parseSettingsConfiguration,searchConfiguration} from '../src/settings-configuration.ts';

test('presets preserve unmatched Advanced values as Custom and reset the exact composite',()=>{
 const options=[{value:'original',sets:{a:'require',b:'never',c:'never'}},{value:'automatic',sets:{a:'prefer',b:'allow',c:'allow'}},{value:'custom'}];
 const read=(values:Record<string,unknown>)=>(key:string)=>values[key];
 assert.equal(exactPresetValue(options,read({a:'require',b:'allow',c:'never'})),'custom');
 assert.equal(exactPresetValue(options,read({a:'require',b:'never',c:'never'})),'original');
 assert.deepEqual(presetPatch(options,'automatic'),{a:'prefer',b:'allow',c:'allow'});
 assert.throws(()=>presetPatch(options,'custom'));
});
test('typed Mbps input is empty by default and converts only explicit numbers',()=>{
 const binding=settingsBinding('streaming.uploadSpeed')!;
 assert.deepEqual(settingsBindingPatch(binding,null),{form:'connectivity',values:{uploadCapacityKbps:0}});
 assert.deepEqual(settingsBindingPatch(binding,12.5),{form:'connectivity',values:{uploadCapacityKbps:12500}});
 assert.equal(readSettingsBinding(binding,{forms:{connectivity:{uploadCapacityKbps:0}}})?.current,null);
 assert.throws(()=>settingsBindingPatch(binding,Number.NaN));
});
test('configuration preserves current/effective/default and config-only source search',()=>{
 const doc=parseSettingsConfiguration({revision:3,registryRevision:'38b',path:'/config/portico.toml',fields:[{id:'updates.channel',key:'updates.channel',current:'beta',effective:'stable',default:'stable',source:'file',configOnly:true,applicationMode:'restart'}]});
 assert.equal(searchConfiguration(doc,'updates.channel beta')[0]?.source,'file');
 assert.deepEqual(readSettingsBinding(settingsBinding('updates.channel')!,{configuration:doc},'updates.channel'),{current:'beta',effective:'stable',default:'stable',source:'file',configOnly:true,applicationMode:'restart'});
 assert.throws(()=>parseSettingsConfiguration({...doc,fields:[...doc.fields,...doc.fields]}));
});
test('own address is independent of managed route and cannot smuggle credentials or a path',()=>{
 assert.deepEqual(settingsBindingPatch(settingsBinding('remote.ownCertificate')!,false),{form:'connectivity',values:{ownAddressEnabled:false}});
 assert.deepEqual(settingsBindingPatch(settingsBinding('remote.externalAddress')!,'https://media.example.com'),{form:'connectivity',values:{customCertificateDomain:'media.example.com'}});
 for(const bad of ['https://name:secret@example.com','https://example.com/a','http://example.com'])assert.throws(()=>settingsBindingPatch(settingsBinding('remote.externalAddress')!,bad));
});

test('retired hardware controls have no Settings binding',()=>{
 for(const retired of ['quality.playerEngine','audioSubtitles.burnIn','audioSubtitles.passthroughFormats','streaming.hardwareDevice'])assert.equal(settingsBinding(retired),undefined);
 // The revised selector writes its own opaque-id field, never the old method enum.
 assert.equal(settingsBinding('streaming.hardware')?.key,'hardwareSelection');
});

test('heavy window edits preserve the independent backup policy and exact day matrix',()=>{
 const backup={id:'nightly',enabled:true,cadence:'nightly',days:['monday'],startMinute:180,durationMinutes:240,timezone:'',tasks:['backup']};
 const heavy={id:'heavy',enabled:false,cadence:'nightly',days:['monday','tuesday','wednesday','thursday','friday','saturday','sunday'],startMinute:60,durationMinutes:300,timezone:'',tasks:['analysis','trickplay']};
 const sources={forms:{maintenance:{windows:[backup,heavy]}}};
 assert.equal(readSettingsBinding(settingsBinding('general.heavyWork')!,sources)?.current,'any');
 const patch=settingsBindingPatch(settingsBinding('general.heavyWork')!,'overnight',sources);
 assert.deepEqual((patch.values.windows as unknown[])[0],backup);
 assert.equal(readSettingsBinding(settingsBinding('general.heavyWork')!,{forms:{maintenance:patch.values}})?.current,'overnight');
 const custom=settingsBindingPatch(settingsBinding('general.heavyLength')!,'4',{forms:{maintenance:patch.values}});
 assert.equal(readSettingsBinding(settingsBinding('general.heavyWork')!,{forms:{maintenance:custom.values}})?.current,'custom');
});

test('partial matrix preset resets its exact members and preserves other Advanced members',()=>{
 const options=[{value:'on',sets:{matrix:{first:'runs',second:'runs'}}}];
 const current={first:'skips',second:'runs',third:'skips'};
 assert.deepEqual(presetPatch(options,'on',()=>current),{matrix:{first:'runs',second:'runs',third:'skips'}});
 assert.equal(exactPresetValue(options,()=>({...current,first:'runs'})),'on');
});

test('away quality uses canonical exact video bitrates, retaining arbitrary values as Custom',()=>{
 const b=settingsBinding('streaming.awayQuality')!;
 assert.equal(readSettingsBinding(b,{forms:{connectivity:{remoteBitrateLimitKbps:8000}}})?.current,'1080p-low');
 assert.equal(readSettingsBinding(b,{forms:{connectivity:{remoteBitrateLimitKbps:8123}}})?.current,'custom');
 assert.deepEqual(settingsBindingPatch(b,'4k'),{form:'connectivity',values:{remoteBitrateLimitKbps:40000}});
 assert.throws(()=>settingsBindingPatch(b,'automatic'));
});

test('missing-title preset writes both policy and exact days without rounding a custom grace period',()=>{
 const b=settingsBinding('storage.missingRule')!;
 assert.deepEqual(settingsBindingPatch(b,'7d'),{form:'runtime',values:{'library.missingTitles':'after_days','library.missingTitlesDays':7}});
 assert.equal(readSettingsBinding(b,{forms:{runtime:{'library.missingTitles':'after_days','library.missingTitlesDays':17}}})?.current,'custom');
});


test('folder units and create kinds remain canonical and preserve inventory revision',async()=>{
 const {libraryKindForChoice,readLibrarySourceSettingsBinding,patchLibrarySourceSettingsBinding}=await import('../src/presentation/settings-bindings.ts');
 for(const [choice,kind]of [['movies','movie'],['audiobooks','audiobook'],['books','book'],['comics','comic']])assert.equal(libraryKindForChoice(choice),kind);
 const draft={name:'',path:'/media',classification:'local' as const,followSymlinks:false,intervalSeconds:3600,expectedRevision:11,acceptReplacement:false};
 const patched=patchLibrarySourceSettingsBinding('libraries.rescan','15m',draft);assert.equal(patched.intervalSeconds,900);assert.equal(patched.expectedRevision,11);assert.equal(patched.acceptReplacement,false);
 const source={...draft,id:'s',libraryId:'l',kind:'local',resolvedPath:'/media',generation:2,health:'ready',enabled:true,identityConfirmed:true,nextScanAt:'',lastCompleteAt:'',lastProgressAt:''};
 assert.equal(readLibrarySourceSettingsBinding('libraries.rescan',source)?.current,'1h');
 assert.equal(readLibrarySourceSettingsBinding('libraries.rescan',{...source,intervalSeconds:1234})?.current,'custom');
 assert.throws(()=>patchLibrarySourceSettingsBinding('libraries.rescan','custom',draft));
});


test('chapter labels toggle writes the real runtime boolean',()=>{
 const binding=settingsBinding('segments.chapters')!;
 assert.equal(binding.key,'segments.useChapterNames');
 const sources={forms:{runtime:{'segments.useChapterNames':true}}};
 assert.equal(readSettingsBinding(binding,sources)?.current,true);
 const patch=settingsBindingPatch(binding,false,sources);
 assert.deepEqual(patch,{form:'runtime',values:{'segments.useChapterNames':false}});
 assert.equal(readSettingsBinding(binding,{forms:{runtime:patch.values}})?.current,false);
});

test('conversion preference and its disk folder have independent runtime bindings',()=>{
 const location=settingsBinding('streaming.transcodeLocation')!,folder=settingsBinding('streaming.transcodeFolder')!;
 assert.deepEqual(location,{form:'runtime',key:'transcodeLocation'});
 assert.equal(folder.key,'temporaryDirectory');
 const sources={forms:{runtime:{transcodeLocation:'memory',temporaryDirectory:'/transcode'}}};
 assert.equal(readSettingsBinding(location,sources)?.current,'memory');
 assert.equal(readSettingsBinding(folder,sources)?.current,'/transcode');
 assert.deepEqual(settingsBindingPatch(location,'disk'),{form:'runtime',values:{transcodeLocation:'disk'}});
 assert.deepEqual(settingsBindingPatch(location,'memory'),{form:'runtime',values:{transcodeLocation:'memory'}});
 assert.deepEqual(settingsBindingPatch(folder,'/disk'),{form:'runtime',values:{temporaryDirectory:'/disk'}});
});
