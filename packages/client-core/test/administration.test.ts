import test from 'node:test';
import assert from 'node:assert/strict';
import {AdministrationError,analysisSelectionComplete,backupProgressFraction,deletionAllowed,folderTemplateProblems,folderTemplateTokens,isRestoreRefusalCode,isStatePermissionsAlert,parseAnalysisMatrix,parseBackupsDocument,parseChannelMapPage,parseDVRDocument,parseDeletePreview,parseDeleteResult,parseEnvelope,parseFailure,parseFilesystemPage,parseLibraryDocument,parseLiveSourceDocument,parseRestoreStaged,parseStatePermissionsItem,parseStorageReport,parseTunerView,parseUpdateReport} from '../src/administration.ts';

const matrix={costClasses:[{id:'metadata-only',name:'Metadata only',description:'One read.',order:1},{id:'decode',name:'Decodes the video',description:'Expensive.',order:3}],operations:[{id:'probe',name:'Media probe',costClass:'metadata-only',description:'Reads facts.',mediaKinds:['movie'],defaultEnabled:true,requires:null,producesArtifacts:false,network:false},{id:'trickplay',name:'Trickplay tiles',costClass:'decode',description:'Decodes frames.',mediaKinds:['movie'],defaultEnabled:false,requires:['probe'],producesArtifacts:true,network:false}]};
const navigation={trickplayIntervalMs:10000,trickplayTileWidth:320,trickplayMaxTiles:400,chapterThumbnailMode:'generated',videoPreviewEnabled:false,videoPreviewMs:30000};
const newLibraryFields={scanDepth:'basic',groupCollections:true,localTags:'prefer',lyricsSource:'files',titleSource:'tags',episodeNumbering:'season',findExtras:true,sortIgnoreArticles:true,refreshMode:'replace_unlocked',acoustidEnabled:false,continueWatching:{weeks:16,maximumItems:40,includeSeasonPremieres:true,videoPlayedThreshold:95,videoCompletion:'earliest'}};
function libraryDocument(overrides:Record<string,unknown>={}){return {audience:{everyone:true,accounts:1},actions:['rename','scan','remove'],eligibleControls:['libraries.name'],libraryId:'lib',libraryName:'Films',libraryKind:'movie',revision:2,digest:'d',defaults:{...newLibraryFields,allowMediaDeletion:false,monitorChanges:true,providers:[],analysis:['probe','local_metadata','subtitle_discovery'],navigation},settings:{...newLibraryFields,allowMediaDeletion:true,monitorChanges:true,providers:[{mediaKind:'movie',provider:'tmdb',apiKey:'',apiKeySet:true,language:'en',region:'GB'}],analysis:['probe','trickplay'],navigation},analysisMatrix:matrix,availableProviders:[{id:'tmdb',name:'TMDB',mediaKinds:['movie'],supportsApiKey:true,supportsLocale:true,attributionNote:'Metadata provided by TMDB.'}],enumerations:{chapterThumbnailMode:['none','embedded','generated']},sources:[{id:'src',libraryId:'lib',kind:'local',name:'Films',path:'/mnt/media/Films',resolvedPath:'/mnt/media/Films',classification:'local',enabled:true,generation:1,health:'ready',followSymlinks:false,identityConfirmed:true,intervalMs:3600000,nextScanAt:'',lastCompleteAt:'',lastProgressAt:''}],...overrides};}

test('the envelope is checked and a response from another server is refused',()=>{
 const value=parseEnvelope({protocolVersion:'1.0',serverId:'srv-1',result:libraryDocument()},'srv-1',parseLibraryDocument);
 assert.equal(value.result.libraryName,'Films');
 assert(Object.isFrozen(value.result.sources));
 assert.throws(()=>parseEnvelope({protocolVersion:'1.0',serverId:'srv-2',result:libraryDocument()},'srv-1',parseLibraryDocument),(e:unknown)=>e instanceof AdministrationError&&e.code==='wrong_server');
});

test('a validation refusal keeps the fields the server named',()=>{
 const failure=parseFailure({error:{code:'invalid_administration_input',message:'invalid values: settings.folderTemplate',retryable:false,fields:['settings.folderTemplate']}});
 assert.deepEqual([...failure.fields],['settings.folderTemplate']);
 assert.equal(parseFailure('nonsense').code,'request_failed');
});

test('a returned provider API key is refused rather than held',()=>{
 const leaking=libraryDocument({settings:{...newLibraryFields,allowMediaDeletion:true,monitorChanges:true,providers:[{mediaKind:'movie',provider:'tmdb',apiKey:'secret',apiKeySet:true,language:'en',region:'GB'}],analysis:['probe'],navigation}});
 assert.throws(()=>parseLibraryDocument(leaking),(e:unknown)=>e instanceof AdministrationError&&e.code==='provider_key_disclosed');
});

test('the analysis matrix leaves out an operation whose cost class it does not publish, and dependencies are reported',()=>{
 const parsed=parseAnalysisMatrix(matrix);
 assert.equal(parsed.operations[0].requires.length,0);
 assert.deepEqual([...analysisSelectionComplete(parsed,['trickplay'])],['probe']);
 assert.deepEqual([...analysisSelectionComplete(parsed,['probe','trickplay'])],[]);
 const partial=parseAnalysisMatrix({...matrix,futureField:1,costClasses:[...matrix.costClasses,{id:'quantum',name:'Quantum',description:'',order:9}],operations:[matrix.operations[0],{...matrix.operations[1],costClass:'network'},{...matrix.operations[1],id:'future',costClass:'quantum'},{id:'broken'},{...matrix.operations[0],name:'Again'}]});
 assert.deepEqual(partial.costClasses.map(entry=>entry.id),['metadata-only','decode']);
 assert.deepEqual(partial.operations.map(entry=>[entry.id,entry.name]),[['probe','Media probe']]);
 // Sizes still refuse the whole matrix, inside a row as much as outside it.
 assert.throws(()=>parseAnalysisMatrix({...matrix,operations:Array.from({length:129},()=>matrix.operations[0])}));
 assert.throws(()=>parseAnalysisMatrix({...matrix,operations:[{...matrix.operations[0],mediaKinds:new Array(33).fill('movie')}]}));
 assert.throws(()=>parseAnalysisMatrix({...matrix,operations:{}}));
});
test('a library document keeps its settings exact and leaves out a folder, a provider or a list of choices it cannot read',()=>{
 const doc=parseLibraryDocument(libraryDocument({futureField:true,availableProviders:[...libraryDocument().availableProviders,{id:'imdbx',name:'Future',mediaKinds:['movie'],supportsApiKey:false,supportsLocale:false,attributionNote:''}],sources:[...libraryDocument().sources,{id:'cloud',name:'Cloud',root:'s3://films',classification:'cloud',enabled:true},{id:'src',name:'Again',root:'/again',classification:'local',enabled:true}],enumerations:{chapterThumbnailMode:['none','embedded','generated'],broken:'none',numbers:[1,2]}}));
 assert.deepEqual(doc.availableProviders.map(provider=>provider.id),['tmdb']);
 assert.deepEqual(doc.sources.map(source=>[source.id,source.name]),[['src','Films']]);
 assert.deepEqual(Object.keys(doc.enumerations),['chapterThumbnailMode']);
 // What a save sends back is never guessed: an unknown stored value, a bad revision or an over-long list refuses the document.
 const settings=libraryDocument().settings;
 assert.throws(()=>parseLibraryDocument(libraryDocument({settings:{...settings,navigation:{...navigation,chapterThumbnailMode:'holographic'}}})));
 assert.throws(()=>parseLibraryDocument(libraryDocument({settings:{...settings,providers:[{...settings.providers[0],provider:'imdbx'}]}})));
 assert.throws(()=>parseLibraryDocument(libraryDocument({revision:0})));
 assert.throws(()=>parseLibraryDocument(libraryDocument({digest:''})));
 assert.throws(()=>parseLibraryDocument(libraryDocument({sources:Array.from({length:257},(_,i)=>({id:'s'+i,name:'',root:'/',classification:'local',enabled:true}))})));
 assert.throws(()=>parseLibraryDocument(libraryDocument({enumerations:{long:new Array(257).fill('x')}})));
});

test('stored library choices and producer defaults keep closed enums independently',()=>{
 const doc=libraryDocument();
 for(const key of ['scanDepth','localTags','lyricsSource','titleSource','episodeNumbering','refreshMode']){
  assert.throws(()=>parseLibraryDocument({...doc,settings:{...doc.settings,[key]:'unknown'}}));
  assert.throws(()=>parseLibraryDocument({...doc,defaults:{...doc.defaults,[key]:'unknown'}}));
 }
 const parsed=parseLibraryDocument(doc);
 assert.equal(parsed.defaults.continueWatching.videoPlayedThreshold,95);
 assert.equal(parsed.settings.allowMediaDeletion,true);
 assert.equal(parsed.defaults.allowMediaDeletion,false);
 assert.throws(()=>parseLibraryDocument({...doc,defaults:undefined}));
});

test('a delete preview confirms by title for one target and by count for several',()=>{
 const target={itemId:'item-1',title:'The Lighthouse',kind:'movie',libraryId:'lib',libraryName:'Films',allowMediaDeletion:true,titles:1,bytes:10,files:[{assetId:'a',path:'/media/one.mkv',bytes:10,present:true,shared:false}],dependents:[{kind:'collection',count:1,description:'collections that list this item'}]};
 const single=parseDeletePreview({revision:7,confirmation:'The Lighthouse',confirmationKind:'title',totalBytes:10,totalFiles:1,totalBytesText:'10 B',blocked:[],targets:[target]});
 assert.equal(single.confirmation,'The Lighthouse');
 assert(deletionAllowed(single));
 const bulk=parseDeletePreview({revision:7,confirmation:'2',confirmationKind:'count',totalBytes:20,totalFiles:2,totalBytesText:'20 B',blocked:['other'],targets:[target,{...target,itemId:'item-2'}]});
 assert.equal(deletionAllowed(bulk),false);
 // A count that does not match the targets would prompt for the wrong text.
 assert.throws(()=>parseDeletePreview({revision:7,confirmation:'3',confirmationKind:'count',totalBytes:20,totalFiles:2,totalBytesText:'20 B',blocked:[],targets:[target,{...target,itemId:'item-2'}]}));
 assert.throws(()=>parseDeletePreview({revision:7,confirmation:'The Lighthouse',confirmationKind:'title',totalBytes:20,totalFiles:2,totalBytesText:'20 B',blocked:[],targets:[target,{...target,itemId:'item-2'}]}));
});

test('a delete result counts what was removed from its receipts',()=>{
 const receipt={itemId:'item-1',title:'The Lighthouse',removed:true,filesDeleted:2,filesKept:0,bytes:10,code:''};
 const result=parseDeleteResult({revision:8,removed:1,failed:0,bytesDeleted:10,filesRetained:false,receipts:[receipt]});
 assert.equal(result.receipts[0].filesDeleted,2);
 const disagreeing=parseDeleteResult({revision:8,removed:2,failed:3,bytesDeleted:10,filesRetained:false,receipts:[receipt],futureField:1});
 assert.deepEqual([disagreeing.removed,disagreeing.failed],[1,0]);
 // Each receipt is an outcome the owner must see: an unreadable one, or an unreadable revision, still refuses the result.
 assert.throws(()=>parseDeleteResult({revision:8,removed:1,failed:0,bytesDeleted:10,filesRetained:false,receipts:[receipt,{itemId:'item-2'}]}));
 assert.throws(()=>parseDeleteResult({revision:'8',removed:1,failed:0,bytesDeleted:10,filesRetained:false,receipts:[receipt]}));
});

test('the folder picker parses into frozen projections',()=>{
 const page=parseFilesystemPage({path:'/mnt/media',parent:'/mnt',separator:'/',platform:'linux',writable:true,truncated:false,roots:[{path:'/',name:'/',kind:'root',description:'The root filesystem'}],entries:[{name:'Films',path:'/mnt/media/Films',kind:'directory',readable:true,symlink:false}],nextCursor:'next'});
 assert.equal(page.entries[0].bytes,0);
 assert.equal(page.nextCursor,'next');
  // An entry of a kind this app does not know is listed as unknown, and one it cannot read is left out alone.
  const mixed=parseFilesystemPage({...page,futureField:1,roots:[...page.roots,{path:'',name:'Nowhere',kind:'root',description:''}],entries:[{name:'x',path:'/x',kind:'socket',readable:true,symlink:false,futureField:1},{name:'y',path:'/y',kind:'file',readable:'yes',symlink:false},{name:'Films',path:'/mnt/media/Films',kind:'directory',readable:true,symlink:false}]});
  assert.deepEqual(mixed.entries.map(entry=>[entry.name,entry.kind]),[['x','unknown'],['Films','directory']]);
  assert.equal(mixed.roots.length,1);
  assert.throws(()=>parseFilesystemPage({...page,nextCursor:7}));
  assert.throws(()=>parseFilesystemPage({...page,entries:Array.from({length:201},()=>page.entries[0])}));
});

test('CD-43: a capped 64-root page parses as truncated; a 65th root still refuses',()=>{
  const root=(n:number)=>({path:`/mnt/d${n}`,name:`d${n}`,kind:'media',description:`Dataset ${n}`});
  const base={path:'/',parent:'',separator:'/',platform:'linux',writable:true,entries:[],nextCursor:''};
  const page=parseFilesystemPage({...base,truncated:true,roots:Array.from({length:64},(_,n)=>root(n))});
  assert.equal(page.roots.length,64);
  assert.equal(page.truncated,true);
  assert.throws(()=>parseFilesystemPage({...base,truncated:true,roots:Array.from({length:65},(_,n)=>root(n))}));
});

const liveSourceFixture={sourceId:'src',sourceName:'Rooftop aerial',revision:3,digest:'d',tunerCount:2,effectiveLogoImport:{enabled:true,overwriteExisting:false},availableKinds:['playlist','xmltv-guide','hdhomerun'],settings:{enabledGroups:[],enabledChannels:[],kind:'playlist',streamBufferMs:12000,retryWindowMs:null,userAgent:'',guideSourceId:'src-guide',filters:{categories:{mode:'include',values:['News']},countries:{mode:'include',values:[]},keywords:{mode:'exclude',values:['shopping']}},logoImport:{enabled:true,overwriteExisting:false},channelNumbering:{mode:'sequential',startAt:100,step:1}},effective:{importLimits:{bytes:536870912,channels:50000,programmes:5000000},streamBufferMs:12000,retryWindowMs:60000,userAgent:'Portico/1.0',guideDays:7,logoImport:true,discoveryEnabled:true}};
test('a guide-only live source may not carry stream tuning or a guide attachment',()=>{
 const base=liveSourceFixture;
 const parsed=parseLiveSourceDocument(base);
 assert.equal(parsed.settings.streamBufferMs,12000);
 assert.equal(parsed.settings.retryWindowMs,null);
 assert.throws(()=>parseLiveSourceDocument({...base,settings:{...base.settings,kind:'xmltv-guide'}}));
 const guide=parseLiveSourceDocument({...base,settings:{...base.settings,kind:'xmltv-guide',streamBufferMs:null,guideSourceId:''}});
 assert.equal(guide.settings.kind,'xmltv-guide');
});

test('OWN-018: a source may follow the server logo switch; the effective logo import is the server\'s answer',()=>{
 const base=JSON.parse(JSON.stringify(liveSourceFixture));
 const follow=parseLiveSourceDocument({...base,settings:{...base.settings,logoImport:{enabled:false,overwriteExisting:false,followServer:true}},effectiveLogoImport:{enabled:true,overwriteExisting:false}});
 assert.equal(follow.settings.logoImport.followServer,true);
 assert.deepEqual(follow.effectiveLogoImport,{enabled:true,overwriteExisting:false});
 assert.equal('followServer' in parseLiveSourceDocument(base).settings.logoImport,false,'absent stays absent');
 assert.throws(()=>parseLiveSourceDocument({...base,settings:{...base.settings,logoImport:{enabled:true,overwriteExisting:false,followServer:'yes'}}}));
 assert.throws(()=>parseLiveSourceDocument({...base,effectiveLogoImport:undefined}));
});

test('a channel map lists two channels served on one number so the clash can be fixed, and leaves out a channel it cannot read',()=>{
 const entry={channelId:'bbc-two',name:'BBC Two',sourceNumber:'2',number:'102',group:'Terrestrial',guideChannelId:'bbctwo.uk',hidden:false,position:1,overridden:true};
 const page=parseChannelMapPage({sourceId:'src',generation:'gen-1',items:[entry,{...entry,channelId:'bbc-four',number:'104',position:2}],nextCursor:''});
 assert.equal(page.items.length,2);
 assert.equal(parseChannelMapPage({sourceId:'src',generation:'gen-1',items:[entry,{...entry,channelId:'bbc-four',position:2}],nextCursor:''}).items.length,2);
 assert.deepEqual(parseChannelMapPage({sourceId:'src',generation:'gen-1',items:[entry,{...entry,channelId:'bbc-four',position:-1},{...entry,name:'Again'},'nonsense'],nextCursor:'',futureField:1}).items.map(item=>item.name),['BBC Two']);
 // The source, the generation a save sends back, the cursor and the page size stay exact.
 for(const patch of [{sourceId:''},{generation:7},{nextCursor:7},{items:Array.from({length:201},()=>entry)},{items:{}}])assert.throws(()=>parseChannelMapPage({sourceId:'src',generation:'gen-1',items:[entry],nextCursor:'',...patch}),JSON.stringify(patch));
 // A hidden channel does not claim its number.
 assert.equal(parseChannelMapPage({sourceId:'src',generation:'gen-1',items:[entry,{...entry,channelId:'bbc-four',position:2,hidden:true}],nextCursor:''}).items.length,2);
});

test('the DVR document refuses deleting the original with no conversion, and templates are checked before a write',()=>{
 const settings={prePaddingMs:60000,postPaddingMs:180000,keepPolicy:{mode:'keep-count',keepCount:5,keepDays:0,keepUntilWatched:true},conversion:{mode:'remux',ladderId:'',deleteOriginal:false},folderTemplate:'{series}/Season {season:02}/{series} - S{season:02}E{episode:02} - {title}',movieFolderTemplate:'{title} ({year})/{title} ({year})',recordingProfile:'original',sidecars:{nfo:true,poster:true,fanart:false,thumbnail:true},tunerPreference:'least-busy',liveTunerConflict:'keep-watching'};
 const tokens=[{token:'{series}',description:'Series title.',example:'Nova',supportsPadding:false},{token:'{season}',description:'Season number.',example:'3',supportsPadding:true},{token:'{episode}',description:'Episode number.',example:'7',supportsPadding:true},{token:'{title}',description:'Title.',example:'The Planets',supportsPadding:false},{token:'{year}',description:'Year.',example:'2024',supportsPadding:false}];
 const document=parseDVRDocument({revision:1,digest:'d',settings,folderTokens:tokens,enumerations:{keepMode:['keep-all','keep-count','keep-days']}});
 assert.equal(document.settings.keepPolicy.keepCount,5);
 assert.throws(()=>parseDVRDocument({revision:1,digest:'d',settings:{...settings,conversion:{mode:'none',ladderId:'',deleteOriginal:true}},folderTokens:tokens,enumerations:{}}));
 assert.deepEqual([...folderTemplateTokens('{series}/{title} {season:02}')],['{season}','{series}','{title}']);
 assert.deepEqual([...folderTemplateProblems(settings.folderTemplate,document.folderTokens)],[]);
 assert.deepEqual([...folderTemplateProblems('{series}/{unknown}',document.folderTokens)],['Unknown token {unknown}.','A template must include {title}, {episode} or {date} so recordings do not collide.']);
 assert(folderTemplateProblems('../{title}',document.folderTokens).some(p=>/escape/.test(p)));
 assert(folderTemplateProblems('{title:02}',document.folderTokens).some(p=>/cannot be padded/.test(p)));
});

test('the tuner view keeps the server\'s in-use count and leaves out a recording or a source it cannot read',()=>{
 const assignment={kind:'recording',accountId:'account',profileId:'profile',recordingId:'rec-1',channelId:'bbc-two',title:'Nova',state:'recording',startsAt:'2026-09-16T20:00:00Z',endsAt:'2026-09-16T21:00:00Z',allocationId:'alloc-1'};
 const view=parseTunerView({observedAt:'2026-09-16T20:11:04Z',upcomingWindowHours:48,sources:[{sourceId:'src',sourceName:'Rooftop aerial',tunerCount:2,inUse:1,conflicts:1,active:[assignment],upcoming:[{...assignment,recordingId:'rec-2',state:'scheduled',allocationId:''}]}]});
 assert.equal(view.sources[0].conflicts,1);
 const shared=parseTunerView({observedAt:'',upcomingWindowHours:48,sources:[{sourceId:'src',sourceName:'x',tunerCount:1,inUse:1,conflicts:0,active:[assignment,{...assignment,kind:'live',profileId:'other',recordingId:'session-2',allocationId:'alloc-2'}],upcoming:[]}]});
 assert.equal(shared.sources[0].active.length,2);
 assert.equal(shared.sources[0].inUse,1);
 const expanded=parseTunerView({observedAt:'',upcomingWindowHours:48,sources:[{sourceId:'src',sourceName:'x',tunerCount:1,inUse:1,conflicts:0,active:Array.from({length:201},(_,i)=>({...assignment,recordingId:'reader-'+i})),upcoming:Array.from({length:201},(_,i)=>({...assignment,recordingId:'recording-'+i,state:'scheduled',allocationId:''}))}]});
 assert.equal(expanded.sources[0].active.length,201);
 assert.equal(expanded.sources[0].upcoming.length,201);
 const partial=parseTunerView({observedAt:'',upcomingWindowHours:48,sources:[{sourceId:'src',sourceName:'x',tunerCount:2,inUse:2,conflicts:0,active:[assignment,{recordingId:''}],upcoming:[]},{sourceId:'broken'},{sourceId:'src',sourceName:'again',tunerCount:1,inUse:0,conflicts:0,active:[],upcoming:[]}]});
 assert.deepEqual(partial.sources.map(source=>[source.sourceName,source.inUse,source.active.length]),[['x',2,1]]);
 assert.throws(()=>parseTunerView({observedAt:'',upcomingWindowHours:48,sources:[{sourceId:'src',sourceName:'x',tunerCount:2,inUse:0,conflicts:0,active:[],upcoming:Array.from({length:100001},()=>assignment)}]}));
 assert.throws(()=>parseTunerView({observedAt:'',upcomingWindowHours:48,sources:Array.from({length:65},(_,i)=>({sourceId:'s'+i,sourceName:'x',tunerCount:2,inUse:0,conflicts:0,active:[],upcoming:[]}))}));
});

test('storage and updates parse, and a server claiming auto-install is refused',()=>{
 const report=parseStorageReport({stateDirectory:'/var/lib/portico',totalBytes:100,totalBytesText:'100 B',observedAt:'2026-09-16T20:11:04Z',truncated:false,categories:[]});
 const current={version:'1.2.0',buildId:'b1',sourceDigest:'d',builtAt:''};
 const update=parseUpdateReport({current,channel:'stable',feedConfigured:true,latest:{...current,version:'1.3.0',notes:'Faster scans'},updateAvailable:true,checkedAt:'2026-09-16T20:11:04Z',status:'update-available',message:'',autoInstall:false});
 assert.equal(update.latest?.version,'1.3.0');
 // A status this app does not know reads as an update when one is described, and otherwise as unavailable.
 assert.equal(parseUpdateReport({current,channel:'stable',feedConfigured:true,latest:{...current,version:'1.3.0'},updateAvailable:true,checkedAt:'',status:'staged-rollout',message:'',autoInstall:false}).status,'update-available');
 assert.equal(parseUpdateReport({current,channel:'stable',feedConfigured:true,latest:null,updateAvailable:false,checkedAt:'',status:'staged-rollout',message:'',autoInstall:false,futureField:1}).status,'unavailable');
 assert.deepEqual([...report.categories],[]);
 const category={id:'previews',name:'Previews',description:'Scrubbing previews.',directories:['previews'],cleanable:true,regenerable:true,retentionKey:'previews',bytes:100,bytesText:'100 B',fileCount:2,paths:['/var/lib/portico/previews'],present:true,oldestDays:0,entries:1};
 assert.deepEqual(parseStorageReport({stateDirectory:'/var/lib/portico',totalBytes:100,totalBytesText:'100 B',observedAt:'',truncated:false,categories:[category,{...category,id:'broken',bytes:-1},{...category,name:'Again'}]}).categories.map(entry=>entry.name),['Previews']);
 assert.throws(()=>parseStorageReport({stateDirectory:'/var/lib/portico',totalBytes:100,totalBytesText:'100 B',observedAt:'',truncated:false,categories:[{...category,paths:new Array(17).fill('/x')}]}));
 assert.throws(()=>parseUpdateReport({current,channel:'stable',feedConfigured:true,latest:null,updateAvailable:true,checkedAt:'',status:'update-available',message:'',autoInstall:false}));
  assert.throws(()=>parseUpdateReport({current,channel:'stable',feedConfigured:false,latest:null,updateAvailable:false,checkedAt:'',status:'no-feed',message:'',autoInstall:true}),(e:unknown)=>e instanceof AdministrationError&&e.code==='unexpected_auto_install');
});

test('the Plex-model backups document parses; an unknown kind or an unreadable backup skips the item, malformed optionals drop',()=>{
  const backup={id:'2026-09-24T013000Z',createdAt:'2026-09-24T01:30:00Z',kind:'manual',schemaVersion:41,serverVersion:'1.2.0',bytes:120,path:'/var/lib/portico/backups/2026-09-24T013000Z'};
  const doc=parseBackupsDocument({backups:[backup,{...backup,id:'other',kind:'encrypted-chunks'},],running:{jobId:'job-1',phase:'copying',bytesDone:60,bytesTotal:120},lastRestore:{at:'2026-09-24T02:00:00Z',outcome:'rolled_back',reason:'integrity check failed'}});
  assert.equal(doc.backups.length,1);
  assert.equal(doc.backups[0].kind,'manual');
  assert(Object.isFrozen(doc.backups));
  assert.equal(doc.running?.phase,'copying');
  assert.equal(doc.lastRestore?.outcome,'rolled_back');
  assert.equal(backupProgressFraction(doc.running!),(60/120));
  assert.equal(backupProgressFraction({jobId:'j',phase:'p',bytesDone:5,bytesTotal:0}),undefined);
  // A malformed running block or an unknown restore outcome degrades to absent, never fatal.
  const degraded=parseBackupsDocument({backups:[backup],running:{jobId:'j'},lastRestore:{at:'t',outcome:'migrated'}});
  assert.equal(degraded.running,undefined);
  assert.equal(degraded.lastRestore,undefined);
  assert.equal(parseBackupsDocument({backups:[]}).backups.length,0);
  assert.throws(()=>parseBackupsDocument({}),{code:'invalid_administration_response'});
  assert.deepEqual(parseBackupsDocument({backups:[{...backup,bytes:-1},backup,{...backup,serverVersion:'again'},null]}).backups.map(entry=>entry.serverVersion),['1.2.0']);
  assert.throws(()=>parseBackupsDocument({backups:Array.from({length:1001},()=>backup)}),{code:'invalid_administration_response'});
  assert.throws(()=>parseBackupsDocument({backups:{}}),{code:'invalid_administration_response'});
});

test('restore progress survives tolerant backup row parsing',()=>{
 const backup={id:'backup-1',createdAt:'2026-10-13',kind:'manual',schemaVersion:1,serverVersion:'1',bytes:20,path:'/backups/one'};
 const result={staged:true,restartRequired:true,validation:{schemaVersion:1,integrity:'ok'}};
 const doc=parseBackupsDocument({backups:[backup,{id:'broken'},{...backup,path:'/duplicate'}],restore:{jobId:'restore-1',phase:'staged',bytesDone:20,bytesTotal:20,result},lastRestore:{at:'2026-10-13',outcome:'restored',jobId:'restore-0',schemaVersion:1}});
 assert.equal(doc.backups.length,1);
 assert.equal(doc.backups[0].path,'/backups/one');
 assert.equal(doc.restore?.jobId,'restore-1');
 assert.equal(doc.restore?.result?.validation.integrity,'ok');
 assert.equal(doc.lastRestore?.jobId,'restore-0');
});

test('the restore receipt is strict and the refusal codes are recognised',()=>{
  const staged=parseRestoreStaged({staged:true,restartRequired:true,validation:{schemaVersion:41,integrity:'ok'}});
  assert.equal(staged.validation.schemaVersion,41);
  for(const raw of [null,{},{staged:true},{staged:true,restartRequired:true},{staged:true,restartRequired:true,validation:{schemaVersion:41,integrity:'failed'}},{staged:true,restartRequired:false,validation:{schemaVersion:41,integrity:'ok'}}])assert.throws(()=>parseRestoreStaged(raw),{code:'invalid_administration_response'});
  for(const code of ['restore_source_invalid','restore_pre_release','restore_integrity_failed','restore_not_portico','insufficient_disk'])assert(isRestoreRefusalCode(code));
  assert.equal(isRestoreRefusalCode('request_failed'),false);
});

test('the state-permissions item parses and only live items match',()=>{
  const item=parseStatePermissionsItem({code:'state-permissions',id:'alert-1',revision:2,severity:'warning',status:'open',firstAt:10,lastAt:20,occurrences:3});
  assert.equal(item.id,'alert-1');
  assert(isStatePermissionsAlert({code:'state-permissions',status:'open'}));
  assert(isStatePermissionsAlert({code:'state-permissions',status:'acknowledged'}));
  assert.equal(isStatePermissionsAlert({code:'state-permissions',status:'resolved'}),false);
  assert.equal(isStatePermissionsAlert({code:'low_storage',status:'open'}),false);
  assert.throws(()=>parseStatePermissionsItem({...item,code:'low_storage'}),{code:'invalid_administration_response'});
});

test('current DVR defaults accept unavailable templates as empty and retain the supported settings',()=>{
 const settings={prePaddingMs:60000,postPaddingMs:180000,keepPolicy:{mode:'keep-all',keepCount:0,keepDays:0,keepUntilWatched:false},conversion:{mode:'none',ladderId:'',deleteOriginal:false},folderTemplate:'',movieFolderTemplate:'',recordingProfile:'original',sidecars:{nfo:false,poster:false,fanart:false,thumbnail:false},tunerPreference:'first-available',liveTunerConflict:'stop-watching'};
 const wire={revision:1,digest:'current',settings,folderTokens:[],enumerations:{keepMode:['keep-all','keep-count','keep-days'],conversionMode:['none'],recordingProfile:['original'],tunerPreference:['first-available']}};
 const document=parseDVRDocument(wire);
 assert.deepEqual(document.settings,settings);
 assert.deepEqual(document.folderTokens,[]);
 for(const bad of [undefined,null,42,'x'.repeat(401)])assert.throws(()=>parseDVRDocument({...wire,settings:{...settings,folderTemplate:bad}}),{code:'invalid_administration_response'});
});

// APL-SYS-12 (parser probe, 24 Sep): the runner e2e server's GET /v1/admin/updates answers this
// bare report (a registry route, no administration envelope); the web reads it directly.
test('the updates report as the server sends it parses', () => {
  const report = parseUpdateReport({current: {version: '0.1.0-dev', buildId: 'development', sourceDigest: 'unsealed', builtAt: '', channel: 'stable'}, channel: 'stable', feedConfigured: false, state: 'unconfigured', updateAvailable: false, checkedAt: '', status: 'no-feed', message: 'No release feed is configured, so this server only reports the build it is running.', autoInstall: false});
  assert.equal(report.status, 'no-feed');
  assert.equal(report.current.version, '0.1.0-dev');
});
