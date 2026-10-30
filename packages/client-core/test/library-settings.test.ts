import {readLibrarySettingsBinding,patchLibrarySettingsBinding} from '../src/presentation/settings-bindings.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import type {LibraryAdminDocument,LibrarySettings} from '../src/administration.ts';
import {libraryScanDepth,setLibraryScanDepth,setLibrarySeekPreviews,setLibraryPreviewHours} from '../src/server-admin/library-policy.ts';
import {reorderLibraries,saveLibraryFolder} from '../src/server-admin/libraries.ts';

const settings:LibrarySettings={scanDepth:'basic',groupCollections:true,localTags:'prefer',lyricsSource:'files',titleSource:'tags',episodeNumbering:'season',findExtras:true,sortIgnoreArticles:true,refreshMode:'replace_unlocked',acoustidEnabled:false,continueWatching:{weeks:16,maximumItems:40,includeSeasonPremieres:true,videoPlayedThreshold:95,videoCompletion:'earliest'},allowMediaDeletion:false,monitorChanges:true,providers:[],analysis:['probe','local_metadata','subtitle_discovery'],navigation:{trickplayIntervalMs:10000,trickplayTileWidth:320,trickplayMaxTiles:1080,chapterThumbnailMode:'embedded',videoPreviewEnabled:false,videoPreviewMs:30000},segmentDetection:'server'};

test('scan presets are exact and preserve independent picture choices',()=>{
 const pictures={...settings,analysis:[...settings.analysis,'trickplay','chapter_images'],navigation:{...settings.navigation,chapterThumbnailMode:'generated' as const}};
 const complete=setLibraryScanDepth(pictures,'complete');
 assert.equal(libraryScanDepth(complete),'complete');
 assert.deepEqual(new Set(complete.analysis),new Set(['probe','local_metadata','subtitle_discovery','trickplay','chapter_images','waveform','loudness']));
 const files=setLibraryScanDepth(complete,'file_list_only');
 assert.equal(libraryScanDepth(files),'file_list_only');
 assert.deepEqual(new Set(files.analysis),new Set(['probe','trickplay','chapter_images']));
 assert.equal(libraryScanDepth({...settings,analysis:[...settings.analysis,'checksum']}),'custom');
 assert.equal(setLibrarySeekPreviews(settings,'smooth').navigation.trickplayIntervalMs,5000);
 assert.equal(setLibraryPreviewHours(setLibrarySeekPreviews(settings,'smooth'),6).navigation.trickplayMaxTiles,4096);
 assert.equal(setLibraryPreviewHours(settings,3).navigation.trickplayMaxTiles,1080);
});
test('stable library order uses the real route and rejects duplicate identities before transport',async()=>{
 let calls=0;
 const api={async request<T>(path:string,method?:string,body?:unknown):Promise<T>{calls++;assert.equal(path,'/v1/admin/libraries/order');assert.equal(method,'PUT');assert.deepEqual(body,{libraryIds:['second','first']});return body as T;}};
 await reorderLibraries(api,['second','first']);assert.equal(calls,1);
 await assert.rejects(reorderLibraries(api,['first','first']));assert.equal(calls,1);
});
test('folder writes preserve optional label and canonical interval milliseconds',async()=>{
 const api={async request<T>(path:string,method?:string,body?:unknown):Promise<T>{
  assert.equal(path,'/v1/admin/libraries/library/sources');assert.equal(method,'POST');
  assert.deepEqual(body,{name:'',path:'/media',classification:'local',followSymlinks:false,intervalMs:900000,expectedRevision:7,acceptReplacement:false});
  return {id:'source',libraryId:'library',kind:'local',name:'media',path:'/media',resolvedPath:'/media',classification:'local',enabled:true,followSymlinks:false,identityConfirmed:true,intervalMs:900000,generation:1,health:'ready',nextScanAt:'',lastCompleteAt:'',lastProgressAt:''} as T;
 }};
 const source=await saveLibraryFolder(api,'library',undefined,{name:'',path:'/media',classification:'local',followSymlinks:false,intervalSeconds:900,expectedRevision:7,acceptReplacement:false});
 assert.equal(source.intervalSeconds,900);assert.equal(source.name,'media');
});

function bindingDocument():LibraryAdminDocument{return {libraryId:'library',libraryName:'Library',libraryKind:'movie',revision:7,digest:'current',eligibleControls:['libraries.cwWeeks','libraries.cwWatched','libraries.scrubSpacing','libraries.scanDepth','libraries.titlesFrom','libraries.language'],audience:{everyone:true,accounts:0},actions:['scan','remove','rename'],settings,defaults:{...settings,continueWatching:{...settings.continueWatching,weeks:8}},analysisMatrix:{costClasses:[],operations:[]},availableProviders:[],enumerations:{},sources:[]};}
test('library binding reads producer defaults, converts actual units and gates absent controls',()=>{
 const doc=bindingDocument();
 assert.equal(readLibrarySettingsBinding('libraries.cwWeeks',doc)?.current,16);
 assert.equal(readLibrarySettingsBinding('libraries.cwWeeks',doc)?.default,8);
 assert.equal(readLibrarySettingsBinding('libraries.scrubSpacing',doc)?.current,10);
 assert.equal(patchLibrarySettingsBinding('libraries.scrubSpacing',20,doc).navigation.trickplayIntervalMs,20000);
 assert.equal(patchLibrarySettingsBinding('libraries.cwWatched','80',doc).continueWatching.videoPlayedThreshold,80);
 assert.equal(readLibrarySettingsBinding('libraries.episodeThumbs',doc),undefined);
 assert.throws(()=>patchLibrarySettingsBinding('libraries.episodeThumbs','off',doc));
 assert.throws(()=>patchLibrarySettingsBinding('libraries.scanDepth','custom',doc));
 assert.equal(doc.settings.continueWatching.videoPlayedThreshold,95);
});
test('library provider controls use canonical show and book media kinds',()=>{
 for(const [libraryKind,mediaKind] of [['tv','show'],['anime','show'],['audiobook','book']] as const){
  const provider=libraryKind==='audiobook'?'musicbrainz':'tmdb';
  const doc={...bindingDocument(),libraryKind,settings:{...settings,providers:[{mediaKind,provider,apiKeySet:false,language:'en',region:'US'}]},availableProviders:[{id:provider,name:provider,mediaKinds:[mediaKind],supportsApiKey:false,supportsLocale:true,attributionNote:''}]} as LibraryAdminDocument;
  assert.equal(readLibrarySettingsBinding('libraries.titlesFrom',doc)?.current,provider);
  const changed=patchLibrarySettingsBinding('libraries.language','fr',doc);
  assert.equal(changed.providers[0].language,'fr');
  assert.equal(changed.providers[0].mediaKind,mediaKind);
  assert.equal(patchLibrarySettingsBinding('libraries.titlesFrom',provider,doc).providers[0].provider,provider);
 }
});

test('MOB-111: a folder can be attached without its automatic scan; omission keeps the server default',async()=>{
 const bodies:unknown[]=[];
 const api={async request<T>(_path:string,_method?:string,body?:unknown):Promise<T>{bodies.push(body);
  return {id:'source',libraryId:'library',kind:'local',name:'media',path:'/media',resolvedPath:'/media',classification:'local',enabled:true,followSymlinks:false,identityConfirmed:true,intervalMs:900000,generation:1,health:'ready',nextScanAt:'',lastCompleteAt:'',lastProgressAt:''} as T;}};
 const input={name:'',path:'/media',classification:'local' as const,followSymlinks:false,intervalSeconds:900,expectedRevision:7,acceptReplacement:false};
 await saveLibraryFolder(api,'library',undefined,{...input,scanAfter:false});
 await saveLibraryFolder(api,'library',undefined,input);
 assert.equal((bodies[0] as {scanAfter?:boolean}).scanAfter,false);
 assert.equal('scanAfter' in (bodies[1] as object),false);
 await assert.rejects(saveLibraryFolder(api,'library',undefined,{...input,scanAfter:'no' as never}));
});
