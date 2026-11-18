import test from 'node:test';
import assert from 'node:assert/strict';
import type {SourceAdvanced, SourceAdvancedSettings, SourceReadSettings} from '../src/remote-sources.ts';
import {parseSourceChoiceBaseline,sourceChoiceAccepted,sourceChoiceAdopt,sourceChoiceCommand,sourceChoiceComparison,sourceChoiceFields,sourceChoicePutBody,sourceChoiceResetDefinition,type SourceChoiceBaseline,type SourceChoiceDefinition,type SourceChoiceField} from '../src/remote-source-choice.ts';

const operation='1788580000000-12345678-1234-1234-1234-123456789012';
const reading={scanDepth:'basic',wholeFileReads:false,connections:4,requestsPerSecond:4,bytesPerSecond:0} as const;
const advanced:SourceAdvanced={scanIntervalSeconds:21600,watchForChanges:false,scanOnStart:false,fullRecheckDays:0,cacheBytes:0,cacheFloorBytes:0,seekPreviews:true,chapterImages:true,waveforms:true,loudness:true,checksums:true,introsMode:'inherit',doubleCheckFolders:false,dirCacheSeconds:0,readBufferBytes:0,connectTimeoutSeconds:0,idleTimeoutSeconds:0,retries:0,lowLevelRetries:0};
const definitions: readonly SourceChoiceDefinition[] = [
 {name: 'list', definitionRevision: 1, reading: {...reading, scanDepth: 'file_list_only', connections: 2, requestsPerSecond: 2}, advanced},
 {name: 'once', definitionRevision: 1, reading, advanced},
 {name: 'everything', definitionRevision: 1, reading: {...reading, scanDepth: 'complete', connections: 8, requestsPerSecond: 10}, advanced},
 {name: 'ask', definitionRevision: 1, reading: {...reading, connections: 2, requestsPerSecond: 2}, advanced},
];
// This fixture is the server's published direct-DAV set, not a client classifier.
const applicable=Object.freeze(sourceChoiceFields.filter(field=>!['dirCacheSeconds','readBufferBytes','connectTimeoutSeconds','idleTimeoutSeconds','retries','lowLevelRetries'].includes(field)));
const originalAdvanced={...advanced,dirCacheSeconds:123,retries:7};
const originalRead=(over:Partial<SourceReadSettings>={}):SourceReadSettings=>({sourceId:'dav',generation:'7',revision:2,saved:true,configurationGeneration:'11',values:{...reading,wholeFileReads:true,allowAdvancedMediaAnalysis:false},choiceDefinitions:definitions,choiceApplicableFields:applicable,...over});
const advancedRead=(over:Partial<SourceAdvancedSettings>={}):SourceAdvancedSettings=>({sourceId:'dav',generation:'7',revision:5,saved:true,cacheSupported:false,cacheApplied:false,cacheUsedBytes:0,scanIntervalObserved:0,values:{...originalAdvanced},...over});
function parent(readRevision=2,advancedRevision=6,generation='7',configurationGeneration='11'):SourceReadSettings{
 return originalRead({revision:readRevision,generation,configurationGeneration,choiceBaseline:{sourceId:'dav',generation,configurationGeneration,name:'once',definitionRevision:1,readRevision,advancedRevision,reading:{...reading},advanced:{...advanced},currentAdvanced:{...originalAdvanced},applicableFields:applicable,changedFields:[]}});
}
function command(){return sourceChoiceCommand(originalRead(),advancedRead(),definitions[1]!)!;}

test('read-only baseline comparison is coherent, empty is explicit, consent and legacy are not counted',()=>{
 const settings=parent();assert.deepEqual(sourceChoiceComparison(settings)?.changedFields,[]);
 assert.equal(sourceChoiceComparison(originalRead()),undefined,'absent is not zero');
 assert.equal(sourceChoiceComparison({...settings,revision:3}),undefined);
 assert.equal(sourceChoiceComparison({...settings,values:{...settings.values,connections:8}}),undefined,'server changed set must agree');
 const parsed=parseSourceChoiceBaseline(settings.choiceBaseline)!;assert.deepEqual(parsed.changedFields,[]);
 assert.throws(()=>parseSourceChoiceBaseline({...parsed,changedFields:['allowAdvancedMediaAnalysis']}));
 assert.throws(()=>parseSourceChoiceBaseline({...parsed,applicableFields:['connections','connections']}));
});

test('capture uses one original pair, actual saved booleans and exact offered identity',()=>{
 const c=command();assert.ok(c);assert.equal(Object.isFrozen(c),true);assert.equal(Object.isFrozen(c.originalAdvanced),true);
 assert.equal(sourceChoiceCommand(originalRead(),advancedRead(),{...definitions[1]!}),undefined);
 assert.equal(sourceChoiceCommand(originalRead(),advancedRead({generation:'8'}),definitions[1]!),undefined);
 assert.equal(sourceChoiceCommand(originalRead({configurationGeneration:undefined}),advancedRead(),definitions[1]!),undefined);
 assert.equal(sourceChoiceCommand(originalRead({revision:0,saved:true}),advancedRead(),definitions[1]!),undefined);
 assert.equal(sourceChoiceCommand(originalRead(),advancedRead({revision:0,saved:true}),definitions[1]!),undefined);
 assert.equal(sourceChoiceCommand({...originalRead(),saved:undefined} as never,advancedRead(),definitions[1]!),undefined);
 assert.ok(sourceChoiceCommand(originalRead({revision:0,saved:false}),advancedRead({revision:0,saved:false}),definitions[1]!));
});

test('body is one frozen exact intent, compatibility preserved, consent entirely omitted',()=>{
 const c=command(),body=sourceChoicePutBody(c,operation);
 assert.deepEqual(Object.keys(body).sort(),['expectedGeneration','expectedRevision','operationId','startChoice','values']);
 assert.deepEqual(body.values,{...reading,wholeFileReads:true});assert.equal(Object.hasOwn(body.values,'allowAdvancedMediaAnalysis'),false);
 assert.deepEqual(body.startChoice,{name:'once',definitionRevision:1,expectedAdvancedRevision:5,expectedConfigurationGeneration:'11'});
 assert.equal(Object.isFrozen(body),true);assert.equal(Object.isFrozen(body.values),true);
 assert.throws(()=>sourceChoicePutBody(c,'fresh-invalid-key'));
});

test('parent accepts no-op read2 and actual Advanced6; child uses own generations and accepted pair',()=>{
 const c=command(),accepted=sourceChoiceAccepted(parent(),c)!;assert.ok(accepted);
 assert.equal(accepted.readRevision,2);assert.equal(accepted.advancedRevision,6);
 assert.ok(sourceChoiceAdopt(parent(2,6,'9','18'),c,accepted),'real child is not forced to historical generations');
 assert.equal(sourceChoiceAdopt(parent(2,7,'9','18'),c,accepted),undefined,'later equal values are not this operation');
 assert.equal(sourceChoiceAdopt(parent(3,6,'9','18'),c,accepted),undefined);
 assert.equal(sourceChoiceAdopt({...parent(),choiceBaseline:undefined},c,accepted),undefined);
 assert.equal(sourceChoiceAccepted(parent(2,5),c),undefined,'pre-write Advanced revision is not accepted');
});

test('parent requires exact baseline, definition, applicability and preserved independent fields',()=>{
 const c=command(),p=parent(),b=p.choiceBaseline!;
 for(const bad of [
  {...p,choiceBaseline:undefined},{...p,sourceId:'other'},{...p,generation:'8'},{...p,configurationGeneration:'12'},
  {...p,choiceBaseline:{...b,configurationGeneration:'12'}},{...p,choiceBaseline:{...b,definitionRevision:2}},
  {...p,choiceApplicableFields:sourceChoiceFields,choiceBaseline:{...b,applicableFields:sourceChoiceFields}},
  {...p,values:{...p.values,allowAdvancedMediaAnalysis:true}},{...p,values:{...p.values,wholeFileReads:false}},
  {...p,choiceBaseline:{...b,currentAdvanced:{...b.currentAdvanced,dirCacheSeconds:0}}},
  {...p,choiceBaseline:{...b,currentAdvanced:{...b.currentAdvanced,loudness:false},changedFields:['loudness'] as SourceChoiceField[]}},
 ])assert.equal(sourceChoiceAccepted(bad,c),undefined);
});

test('reset returns the recorded exact offered version; never substitutes a newer same-name definition',()=>{
 const p=parent();assert.equal(sourceChoiceResetDefinition(p),p.choiceDefinitions![1]);
 assert.equal(sourceChoiceResetDefinition({...p,choiceDefinitions:definitions.map(d=>d.name==='once'?{...d,definitionRevision:2}:d)}),undefined);
 assert.equal(sourceChoiceResetDefinition(originalRead()),undefined);
});

test('pure pair capture reads getter-backed source witnesses once',()=>{
 const read=originalRead(),adv=advancedRead();let readGets=0,advGets=0;
 Object.defineProperty(read,'generation',{get(){assert.equal(readGets++,0);return '7';}});
 Object.defineProperty(adv,'generation',{get(){assert.equal(advGets++,0);return '7';}});
 const c=sourceChoiceCommand(read,adv,definitions[1]!);assert.ok(c);assert.equal(readGets,1);assert.equal(advGets,1);
});

const currentAdvanced=Object.freeze({...advanced,loudness:false});
const baseline:SourceChoiceBaseline=Object.freeze({sourceId:'dav',generation:'7',configurationGeneration:'11',name:'once',definitionRevision:1,
 readRevision:2,advancedRevision:3,reading:Object.freeze({...reading}),advanced:Object.freeze({...advanced}),currentAdvanced,
 applicableFields:applicable,changedFields:Object.freeze(['loudness'] as SourceChoiceField[])});
const settings=(over:Record<string,unknown>={})=>({sourceId:'dav',generation:'7',revision:2,configurationGeneration:'11',values:{...reading},choiceDefinitions:definitions,choiceApplicableFields:applicable,choiceBaseline:baseline,...over});

test('a coherent baseline compares the actual read facts and same-snapshot Advanced over the applicable set',()=>{
 const result=sourceChoiceComparison(settings());
 assert.deepEqual(result,{name:'once',definitionRevision:1,applicableFields:applicable,changedFields:['loudness']});
 assert.equal(Object.isFrozen(result),true);
 assert.deepEqual(sourceChoiceComparison(settings({values:{...reading,connections:6}})),undefined,'a changed read value without an exact server diff is withheld');
 assert.deepEqual(sourceChoiceComparison(settings({choiceBaseline:{...baseline,changedFields:[]}})),undefined,'a missing changed row is never a synthetic zero');
 assert.deepEqual(sourceChoiceComparison(settings({choiceBaseline:{...baseline,changedFields:['loudness','connections']}})),undefined,'an extra changed row is refused');
});

test('every original witness must match the same snapshot',()=>{
 for(const over of [
  {sourceId:'other'},{generation:'8'},{configurationGeneration:'12'},{revision:3},
  {choiceBaseline:{...baseline,name:'everything'}},{choiceBaseline:{...baseline,definitionRevision:2}},
  {choiceBaseline:{...baseline,applicableFields:applicable.slice(0,-1)}},
  {choiceDefinitions:definitions.map(value=>value.name==='once'?{...value,advanced:{...value.advanced,loudness:false}}:value)},
 ])assert.equal(sourceChoiceComparison(settings(over as never)),undefined,JSON.stringify(over));
 assert.equal(sourceChoiceComparison({...settings(),configurationGeneration:undefined}),undefined);
 assert.equal(sourceChoiceComparison({...settings(),choiceBaseline:undefined}),undefined);
});

test('an unchanged applied baseline parses zero changes, and a flags-only drift parses its exact row',()=>{
 const zero=Object.freeze([...[]] as SourceChoiceField[]);
 const unchanged=Object.freeze({...baseline,currentAdvanced:Object.freeze({...advanced}),changedFields:zero});
 const parsed=sourceChoiceComparison(settings({choiceBaseline:unchanged}));
 assert.deepEqual(parsed,{name:'once',definitionRevision:1,applicableFields:applicable,changedFields:[]},'a freshly applied choice is a real zero, not a withheld comparison');
 const flags=Object.freeze({...baseline,currentAdvanced:Object.freeze({...advanced,checksums:false}),changedFields:Object.freeze(['checksums'] as SourceChoiceField[])});
 assert.deepEqual(sourceChoiceComparison(settings({choiceBaseline:flags})),{name:'once',definitionRevision:1,applicableFields:applicable,changedFields:['checksums']});
 assert.equal(sourceChoiceComparison(settings({choiceBaseline:{...flags,changedFields:[]}})),undefined,'actual drift with an empty server set stays withheld');
});

test('the retired whole-file and consent flags never add a changed row',()=>{
 const unchanged=Object.freeze({...baseline,currentAdvanced:Object.freeze({...advanced}),changedFields:Object.freeze([] as SourceChoiceField[])});
 const actual={...reading,wholeFileReads:true,allowAdvancedMediaAnalysis:true};
 const comparison=sourceChoiceComparison({...settings({choiceBaseline:unchanged}),values:actual});
 assert.deepEqual(comparison,{name:'once',definitionRevision:1,applicableFields:applicable,changedFields:[]},'legacy whole-file and independent consent are not choice rows');
});
