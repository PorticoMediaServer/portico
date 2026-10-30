import assert from 'node:assert/strict';
import test from 'node:test';
import {parsePreferenceSnapshot,currentPreferenceSnapshot,preferenceValue,preferenceGroups,validatePreferencePatch,type PreferenceSnapshot} from '../src/preferences.ts';
import {resetSkippedParts,skippedParts} from '../src/tolerant.ts';

const digest='a'.repeat(64);
const fields=[
 {key:'playback.playedThresholdPercent',type:'integer',default:95,min:75,max:100,scopes:['profile-server'],group:'playback',labelKey:'preferences.playback.playedThresholdPercent'},
 {key:'playback.defaultSpeed',type:'number',default:1,allowedValues:[0.5,1,1.5,2],scopes:['profile-server'],group:'playback',labelKey:'preferences.playback.defaultSpeed'},
 {key:'playback.preferredAudioLanguages',type:'string-list',default:[],scopes:['profile-server'],group:'playback',labelKey:'preferences.playback.preferredAudioLanguages'},
 {key:'search.rememberHistory',type:'boolean',default:true,scopes:['profile-server'],group:'search',labelKey:'preferences.search.rememberHistory'},
 {key:'quality.cellular.mode',type:'string',default:'standard',allowedValues:['off','standard','original'],scopes:['profile-device-class'],group:'quality',labelKey:'preferences.quality.cellular.mode'},
 {key:'appearance.reduceMotion',type:'boolean',default:false,scopes:['profile-device-class'],group:'appearance',labelKey:'preferences.appearance.reduceMotion'},
];
const snapshot=(overrides:Record<string,unknown>={},revision=1)=>({
 deviceClass:'web',registryRevision:'p39.A1',registry:{revision:'p39.A1',fields},
 documents:[
  {scope:'profile-server',revision,digest,activeRevision:revision,values:{'search.rememberHistory':false}},
  {scope:'profile-device-class',revision:1,digest,activeRevision:1,values:{}},
 ],
 effective:{'playback.playedThresholdPercent':95,'playback.defaultSpeed':1,'playback.preferredAudioLanguages':['en'],'search.rememberHistory':false,'quality.cellular.mode':'off','appearance.reduceMotion':false},
 effectiveSource:{'playback.playedThresholdPercent':'default','playback.defaultSpeed':'default','playback.preferredAudioLanguages':'default','search.rememberHistory':'profile-server','quality.cellular.mode':'policy','appearance.reduceMotion':'default'},
 clampedFields:['quality.cellular.mode'],
 ...overrides});

test('a published registry parses into frozen fields and effective values',()=>{
 const parsed=parsePreferenceSnapshot(snapshot());
 assert.equal(parsed.registry.fields.length,fields.length);
 assert.equal(parsed.effective['search.rememberHistory'],false);
 assert.deepEqual([...parsed.effective['playback.preferredAudioLanguages'] as readonly string[]],['en']);
 assert.equal(parsed.documents.length,2);
 assert.equal(parsed.clampedFields[0],'quality.cellular.mode');
 assert.throws(()=>{(parsed.effective as Record<string,unknown>)['search.rememberHistory']=true;});
});

test('the parser refuses a shape the server would not publish',()=>{
 assert.throws(()=>parsePreferenceSnapshot(null));
 assert.throws(()=>parsePreferenceSnapshot(snapshot({deviceClass:'watch'})));
 // registry.revision must agree with the envelope's registryRevision
 assert.throws(()=>parsePreferenceSnapshot(snapshot({registryRevision:'other'})));
 // every registry field must appear in effective and effectiveSource
 assert.throws(()=>parsePreferenceSnapshot(snapshot({effective:{'search.rememberHistory':false}})));
 // a value of the wrong type for its declared field
 assert.throws(()=>parsePreferenceSnapshot(snapshot({effective:{...snapshot().effective,'playback.playedThresholdPercent':'95'}})));
 // an unknown key anywhere
 assert.throws(()=>parsePreferenceSnapshot(snapshot({effective:{...snapshot().effective,'playback.warpDrive':true}})));
 assert.throws(()=>parsePreferenceSnapshot(snapshot({clampedFields:['playback.warpDrive']})));
 // a document carrying a key that belongs to the other scope
 assert.throws(()=>parsePreferenceSnapshot(snapshot({documents:[{scope:'profile-server',revision:1,digest,activeRevision:1,values:{'appearance.reduceMotion':true}},{scope:'profile-device-class',revision:1,digest,activeRevision:1,values:{}}]})));
 // a duplicated scope, a bad digest, a mismatched activeRevision
 assert.throws(()=>parsePreferenceSnapshot(snapshot({documents:[snapshot().documents[0],snapshot().documents[0]]})));
 assert.throws(()=>parsePreferenceSnapshot(snapshot({documents:[{...snapshot().documents[0],digest:'invalid'},snapshot().documents[1]]})));
 assert.throws(()=>parsePreferenceSnapshot(snapshot({documents:[{...snapshot().documents[0],activeRevision:9},snapshot().documents[1]]})));
 // a document revision that is missing, zero or not a number, or a document that is missing
 assert.throws(()=>parsePreferenceSnapshot(snapshot({documents:[{...snapshot().documents[0],revision:0,activeRevision:0},snapshot().documents[1]]})));
 assert.throws(()=>parsePreferenceSnapshot(snapshot({documents:[{...snapshot().documents[0],revision:'1',activeRevision:'1'},snapshot().documents[1]]})));
 assert.throws(()=>parsePreferenceSnapshot(snapshot({documents:[snapshot().documents[0]]})));
 assert.throws(()=>parsePreferenceSnapshot(snapshot({documents:[snapshot().documents[0],{...snapshot().documents[1],scope:'account'}]})));
 // more fields than a registry carries, or none at all
 assert.throws(()=>parsePreferenceSnapshot(snapshot({registry:{revision:'p39.A1',fields:Array.from({length:501},()=>fields[0])}})));
 assert.throws(()=>parsePreferenceSnapshot(snapshot({registry:{revision:'p39.A1',fields:[]}})));
 assert.throws(()=>parsePreferenceSnapshot(snapshot({registry:{revision:'p39.A1',fields:{}}})));
});

test('a field whose definition cannot be read is left out with its values, and the rest of the snapshot is kept',()=>{
 const unreadable:Record<string,unknown>[]=[
  {...fields[1],default:3.5},                                   // a default outside its own published domain
  {...fields[0],labelKey:'preferences.other'},                   // a label key that does not name its field
  {...fields[3],scopes:['profile-server','account']},            // a scope this client does not know
  {...fields[4],allowedValues:[]},                               // an option list with nothing in it
  {...fields[5],min:2,max:1},                                    // limits that contradict each other
  {...fields[2],group:''},                                       // no group to show it under
 ];
 for(const bad of unreadable){
  resetSkippedParts();
  const parsed=parsePreferenceSnapshot(snapshot({registry:{revision:'p39.A1',fields:fields.map(field=>field.key===bad.key?bad:field)},futureField:1}));
  assert.deepEqual(parsed.registry.fields.map(field=>field.key),fields.filter(field=>field.key!==bad.key).map(field=>field.key));
  assert.equal(bad.key as string in parsed.effective,false);
  assert.equal(bad.key as string in parsed.effectiveSource,false);
  assert.equal(parsed.clampedFields.includes(bad.key as string),false);
  for(const document of parsed.documents)assert.equal(bad.key as string in document.values,false);
  assert.throws(()=>preferenceValue(parsed,bad.key as string));
  assert.deepEqual(parsed.documents.map(document=>document.revision),[1,1]);
  assert.deepEqual(skippedParts(),[{document:'preferences',part:'registry.fields',count:1}]);
 }
 // Entries that are not fields at all, and a field under a key this client cannot address, go the same way.
 const odd=parsePreferenceSnapshot(snapshot({registry:{revision:'p39.A1',fields:[null,'field',{type:'boolean'},{...fields[3],key:'Search_History',labelKey:'preferences.Search_History'},...fields]},
  effective:{...snapshot().effective,'Search_History':true},effectiveSource:{...snapshot().effectiveSource,'Search_History':'default'}}));
 assert.equal(odd.registry.fields.length,fields.length);
 assert.equal('Search_History' in odd.effective,false);
});

test('a key published twice keeps its first field',()=>{
 const twice=parsePreferenceSnapshot(snapshot({registry:{revision:'p39.A1',fields:[...fields,{...fields[0],default:80}]}}));
 assert.equal(twice.registry.fields.length,fields.length);
 assert.equal(twice.registry.fields[0].default,95);
 // An unreadable first field is not replaced by a later one under the same key.
 const hidden=parsePreferenceSnapshot(snapshot({registry:{revision:'p39.A1',fields:[{...fields[0],type:'colour'},...fields]}}));
 assert.equal(hidden.registry.fields.some(field=>field.key===fields[0].key),false);
 assert.equal(fields[0].key in hidden.effective,false);
 // A readable first field keeps its values when a later repeat is unreadable.
 const kept=parsePreferenceSnapshot(snapshot({registry:{revision:'p39.A1',fields:[...fields,{...fields[0],type:'colour'}]}}));
 assert.equal(kept.effective[fields[0].key],95);
});

test('late preference reads cannot regress a newer applied revision in the same scope',()=>{
 const applied=parsePreferenceSnapshot(snapshot({},2));
 assert.equal(currentPreferenceSnapshot(applied,parsePreferenceSnapshot(snapshot({},1))),applied);
 assert.equal(currentPreferenceSnapshot(undefined,applied),applied);
 const newer=parsePreferenceSnapshot(snapshot({},3));
 assert.equal(currentPreferenceSnapshot(applied,newer),newer);
 // A snapshot for another device class is a different scope, not a regression.
 const other=parsePreferenceSnapshot(snapshot({deviceClass:'mobile'},1)) as PreferenceSnapshot;
 assert.equal(currentPreferenceSnapshot(applied,other),other);
});

test('effective reads and generated groups come from the published registry alone',()=>{
 const parsed=parsePreferenceSnapshot(snapshot());
 assert.equal(preferenceValue<number>(parsed,'playback.playedThresholdPercent'),95);
 assert.throws(()=>preferenceValue(parsed,'playback.warpDrive'));
 assert.deepEqual(preferenceGroups(parsed.registry).map(g=>g.group),['playback','search','quality','appearance']);
});

test('a patch is checked against the registry before it is sent',()=>{
 const registry=parsePreferenceSnapshot(snapshot()).registry;
 assert.deepEqual(validatePreferencePatch(registry,'profile-server',{'search.rememberHistory':false,'playback.playedThresholdPercent':null}),{'search.rememberHistory':false,'playback.playedThresholdPercent':null});
 assert.throws(()=>validatePreferencePatch(registry,'profile-server',{'playback.warpDrive':true}));
 assert.throws(()=>validatePreferencePatch(registry,'profile-server',{'appearance.reduceMotion':true}));
 assert.throws(()=>validatePreferencePatch(registry,'profile-server',{'playback.defaultSpeed':3.5}));
 assert.throws(()=>validatePreferencePatch(registry,'profile-server',{'playback.playedThresholdPercent':74}));
 assert.throws(()=>validatePreferencePatch(registry,'profile-server',{'playback.playedThresholdPercent':101}));
 assert.throws(()=>validatePreferencePatch(registry,'profile-server',{'search.rememberHistory':'yes' as never}));
});

test('the server’s semantic types and labelled options parse; a type this client doesn’t know is left out, not fatal',()=>{
 const extra=[
  {key:'playback.preferredSubtitleLanguages',type:'languageList',default:['en'],scopes:['profile-server'],group:'playback',labelKey:'preferences.playback.preferredSubtitleLanguages'},
  {key:'region.locale',type:'locale',default:'auto',scopes:['profile-server'],group:'region',labelKey:'preferences.region.locale'},
  {key:'region.timeZone',type:'timeZone',default:'auto',scopes:['profile-server'],group:'region',labelKey:'preferences.region.timeZone'},
  {key:'quality.wifi.maxVideoBitrateMbps',type:'integer',default:0,allowedValues:[{value:0,label:'Original'},{value:20,label:'20 Mbps',maxVideoHeight:2160}],scopes:['profile-device-class'],group:'quality',labelKey:'preferences.quality.wifi.maxVideoBitrateMbps'},
  {key:'future.thing',type:'colour',default:'#fff',scopes:['profile-server'],group:'appearance',labelKey:'preferences.future.thing'},
 ];
 const raw=snapshot({registry:{revision:'p39.A1',fields:[...fields,...extra]},
  effective:{...snapshot().effective,'playback.preferredSubtitleLanguages':['en','ja'],'region.locale':'auto','region.timeZone':'America/Halifax','quality.wifi.maxVideoBitrateMbps':20,'future.thing':'#000'},
  effectiveSource:{...snapshot().effectiveSource,'playback.preferredSubtitleLanguages':'default','region.locale':'default','region.timeZone':'profile-server','quality.wifi.maxVideoBitrateMbps':'default','future.thing':'default'}});
 const parsed=parsePreferenceSnapshot(raw);
 assert.deepEqual(preferenceValue(parsed,'playback.preferredSubtitleLanguages'),['en','ja']);
 // An older server may still publish the retired time zone field: it is left out like any unknown type.
 assert.equal(parsed.registry.fields.some(f=>f.key==='region.timeZone'),false,'the retired time zone field is left out');
 assert.equal('region.timeZone' in parsed.effective,false);
 assert.equal(parsed.registry.fields.some(f=>f.key==='region.locale'),true);
 assert.deepEqual(parsed.registry.fields.find(f=>f.key==='quality.wifi.maxVideoBitrateMbps')!.allowedValues,[0,20]);
 assert.equal(parsed.registry.fields.some(f=>f.key==='future.thing'),false,'an unknown type is left out');
 assert.equal('future.thing' in parsed.effective,false);
 const outside=parsePreferenceSnapshot(snapshot({registry:{revision:'p39.A1',fields:[...fields,{...extra[3],default:5}]},effective:{...snapshot().effective,'quality.wifi.maxVideoBitrateMbps':0},effectiveSource:{...snapshot().effectiveSource,'quality.wifi.maxVideoBitrateMbps':'default'}}));
 assert.equal(outside.registry.fields.some(f=>f.key==='quality.wifi.maxVideoBitrateMbps'),false,'a field whose default is outside its labelled options is left out');
 assert.equal(outside.registry.fields.length,fields.length);
});
