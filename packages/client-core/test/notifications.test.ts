import {wireFixture} from './wire-fixture.ts';
import {test} from 'node:test';
import assert from 'node:assert/strict';
import {
 parseNotification as read_parseNotification,parseNotificationInbox as read_parseNotificationInbox,parseNotificationDelta as read_parseNotificationDelta,parseNotificationBatchResult,
 parseNotificationSummary as read_parseNotificationSummary,parseNotificationBroadcastResult,notificationActionSupported,parseNotificationVocabulary,notificationCommands,
 mergeNotifications,parseFeedbackCapabilities,parseFeedbackSubmission as read_parseFeedbackSubmission,parseFeedbackList as read_parseFeedbackList,parseFeedbackReport as read_parseFeedbackReport,
 type Notification,
} from '../src/notifications.ts';
import {resetSkippedParts,skippedParts} from '../src/tolerant.ts';

const parseNotification=(value:unknown,...args:any[]) => (read_parseNotification as any)(wireFixture(value,{},['createdAt', 'updatedAt', 'expiresAt', 'readAt', 'archivedAt', 'observedAt', 'freshUntil', 'at', 'nextAt']),...args);
const parseNotificationInbox=(value:unknown,...args:any[]) => (read_parseNotificationInbox as any)(wireFixture(value,{},['createdAt', 'updatedAt', 'expiresAt', 'readAt', 'archivedAt', 'observedAt', 'freshUntil', 'at', 'nextAt']),...args);
const parseNotificationDelta=(value:unknown,...args:any[]) => (read_parseNotificationDelta as any)(wireFixture(value,{},['createdAt', 'updatedAt', 'expiresAt', 'readAt', 'archivedAt', 'observedAt', 'freshUntil', 'at', 'nextAt']),...args);
const parseNotificationSummary=(value:unknown,...args:any[]) => (read_parseNotificationSummary as any)(wireFixture(value,{},['createdAt', 'updatedAt', 'expiresAt', 'readAt', 'archivedAt', 'observedAt', 'freshUntil', 'at', 'nextAt']),...args);
const parseFeedbackSubmission=(value:unknown,...args:any[]) => (read_parseFeedbackSubmission as any)(wireFixture(value,{},['createdAt', 'updatedAt', 'expiresAt', 'readAt', 'archivedAt', 'observedAt', 'freshUntil', 'at', 'nextAt']),...args);
const parseFeedbackList=(value:unknown,...args:any[]) => (read_parseFeedbackList as any)(wireFixture(value,{},['createdAt', 'updatedAt', 'expiresAt', 'readAt', 'archivedAt', 'observedAt', 'freshUntil', 'at', 'nextAt']),...args);
const parseFeedbackReport=(value:unknown,...args:any[]) => (read_parseFeedbackReport as any)(wireFixture(value,{},['createdAt', 'updatedAt', 'expiresAt', 'readAt', 'archivedAt', 'observedAt', 'freshUntil', 'at', 'nextAt']),...args);

const notification=(over:Record<string,unknown>={})=>({
 id:'n-1',audience:'profile',severity:'info',source:'downloads',category:'download.finished',
 title:'Download ready',body:'A title finished downloading.',
 arguments:{title:'A title',downloadId:'dl-1'},
 actions:[{kind:'command',label:'Open',command:'open-download',arguments:{downloadId:'dl-1'}}],
 dedupeKey:'download:dl-1',revision:7,createdAt:1,updatedAt:1,expiresAt:99,
 readAt:null,archivedAt:null,read:false,archived:false,cursor:'3',...over});

const inbox=(items:unknown[])=>({
 revision:7,audience:'all',audiences:['profile','account-admin'],state:'all',
 counts:{unread:1,read:0,archived:0,total:1},items,nextCursor:'',observedAt:2,retentionDays:180});

test('a notification parses with its action and its dedupe identity',()=>{
 const parsed=parseNotification(notification());
 assert.equal(parsed.dedupeKey,'download:dl-1');
 assert.equal(parsed.actions[0].command,'open-download');
 assert.equal(parsed.readAt,null);
});

test('an action outside the shape is left out rather than half-read',()=>{
 // A navigate carrying a command, or a command carrying a target, is not a
 // shape the server produces; accepting it would let one be mistaken for the other.
 // The notification is still worth reading without it.
 const open={kind:'command',label:'Open',command:'open-download'};
 for(const action of [{kind:'navigate',label:'Go',command:'run-scan'},{kind:'command',label:'Go',command:'run-scan',target:{view:'home'}},{kind:'explode',label:'Go'},{kind:'command',label:'x'.repeat(121),command:'run-scan'},null]){
  const parsed=parseNotification(notification({actions:[action,open]}));
  assert.deepEqual(parsed.actions.map(a=>a.label),['Open']);
  assert.equal(parsed.title,'Download ready');
 }
 // More actions than a notification carries is a size limit, not a newer server.
 assert.throws(()=>parseNotification(notification({actions:[open,open,open,open]})));
 assert.throws(()=>parseNotification(notification({revision:-1})));
 assert.throws(()=>parseNotification(notification({id:''})));
 assert.throws(()=>parseNotification(notification({title:'x'.repeat(301)})));
});

test('a severity this app does not know reads as the plainest one',()=>{
 resetSkippedParts();
 assert.equal(parseNotification(notification({severity:'catastrophic'})).severity,'info');
 assert.deepEqual(skippedParts(),[{document:'notification',part:'severity',count:1}]);
 assert.equal(parseNotification(notification({severity:'critical'})).severity,'critical');
 // A severity that is not a short word at all is a malformed notification.
 assert.throws(()=>parseNotification(notification({severity:7})));
 assert.throws(()=>parseNotification(notification({severity:'x'.repeat(41)})));
 assert.throws(()=>parseNotification(notification({severity:undefined})));
});

test('one notification that cannot be read leaves the rest of the inbox in place',()=>{
 resetSkippedParts();
 const parsed=parseNotificationInbox(inbox([
  notification({id:'n-1'}),
  notification({id:'n-2',audience:'everyone'}),
  notification({id:'n-3',revision:-1}),
  'not a notification',
  notification({id:'n-5',severity:'catastrophic',futureField:{nested:true}}),
 ]));
 assert.deepEqual(parsed.items.map(n=>n.id),['n-1','n-5']);
 assert.equal(parsed.items[1].severity,'info');
 assert.equal(parsed.revision,7);
 assert.deepEqual(parsed.counts,{unread:1,read:0,archived:0,total:1});
 assert.deepEqual(skippedParts().find(part=>part.document==='notification-inbox'),{document:'notification-inbox',part:'items',count:3});
 // A field this app has never heard of does not matter anywhere in the document.
 assert.equal(parseNotificationInbox({...inbox([notification()]),futureField:1}).items.length,1);
 const delta=parseNotificationDelta({revision:9,since:7,resync:false,counts:{unread:1,read:0,archived:0,total:1},
  items:[notification({id:'n-1',audience:'everyone'}),notification({id:'n-2'})]});
 assert.deepEqual(delta.items.map(n=>n.id),['n-2']);
});

test('the inbox revision, cursor, counts and page size are still exact',()=>{
 assert.throws(()=>parseNotificationInbox({...inbox([notification()]),revision:-1}));
 assert.throws(()=>parseNotificationInbox({...inbox([notification()]),revision:'7'}));
 assert.throws(()=>parseNotificationInbox({...inbox([notification()]),nextCursor:'x'.repeat(65)}));
 assert.throws(()=>parseNotificationInbox({...inbox([notification()]),nextCursor:undefined}));
 assert.throws(()=>parseNotificationInbox({...inbox([notification()]),counts:{unread:-1,read:0,archived:0,total:0}}));
 assert.throws(()=>parseNotificationInbox({...inbox([notification()]),items:{}}));
 assert.throws(()=>parseNotificationInbox([]));
 assert.throws(()=>parseNotificationDelta({revision:9,since:7,resync:false,counts:{unread:0,read:0,archived:0,total:0},items:Array.from({length:101},()=>notification())}));
 assert.throws(()=>parseNotificationDelta({revision:9,since:-1,resync:false,counts:{unread:0,read:0,archived:0,total:0},items:[]}));
});

test('an unknown command is kept but reported as unsupported',()=>{
 // The server may be newer than this client. The action survives parsing so the
 // UI can show it disabled; it must not be silently dropped or guessed at.
 const parsed=parseNotification(notification({actions:[{kind:'command',label:'Do',command:'teleport'}]}));
 assert.equal(parsed.actions.length,1);
 assert.equal(notificationActionSupported(parsed.actions[0],['home']),false);
 const known=parseNotification(notification()).actions[0];
 assert.equal(notificationActionSupported(known,['home']),true);
 const navigate=parseNotification(notification({actions:[{kind:'navigate',label:'Go',target:{view:'atlantis'}}]})).actions[0];
 assert.equal(notificationActionSupported(navigate,['home','library']),false);
 assert.equal(notificationActionSupported(navigate,['atlantis']),true);
});

test('the inbox document carries its counts, revision and retention together',()=>{
 const parsed=parseNotificationInbox(inbox([notification()]));
 assert.equal(parsed.revision,7);
 assert.equal(parsed.counts.unread,1);
 assert.equal(parsed.retentionDays,180);
 assert.equal(parsed.items.length,1);
 assert.throws(()=>parseNotificationInbox(inbox(Array.from({length:101},()=>notification()))));
 assert.throws(()=>parseNotificationInbox({...inbox([]),counts:{unread:1}}));
});

test('a delta merges by identity so a restated condition does not double up',()=>{
 const held=[parseNotification(notification())];
 const delta=parseNotificationDelta({revision:9,since:7,resync:false,
  counts:{unread:1,read:0,archived:0,total:1},
  items:[notification({revision:9,title:'Download failed',severity:'warning'})]});
 const merged=mergeNotifications(held,delta);
 assert.equal(merged.length,1);
 assert.equal(merged[0].title,'Download failed');
 assert.equal(merged[0].severity,'warning');
 const added=mergeNotifications(merged,parseNotificationDelta({revision:10,since:9,resync:false,
  counts:{unread:2,read:0,archived:0,total:2},items:[notification({id:'n-2',createdAt:5})]}));
 assert.deepEqual(added.map((n:Notification)=>n.id),['n-2','n-1']);
});

test('a resync delta leaves the held list alone so a partial list is never merged',()=>{
 const held=[parseNotification(notification())];
 const delta=parseNotificationDelta({revision:99,since:7,resync:true,
  counts:{unread:0,read:0,archived:0,total:0},items:[]});
 assert.equal(delta.resync,true);
 assert.deepEqual(mergeNotifications(held,delta).map((n:Notification)=>n.id),['n-1']);
});

test('a batch result reports an outcome for every identifier',()=>{
 const parsed=parseNotificationBatchResult({revision:8,audience:'all',applied:1,
  counts:{unread:0,read:1,archived:0,total:1},
  receipts:[{action:'read',id:'n-1',outcome:'applied'},{action:'read-all',outcome:'unchanged'}]});
 assert.equal(parsed.receipts[0].outcome,'applied');
 assert.equal(parsed.receipts[1].id,undefined);
 // An outcome this app does not know says nothing it can act on: that receipt alone is left out.
 const newer=parseNotificationBatchResult({revision:8,audience:'all',applied:1,futureField:true,
  counts:{unread:0,read:0,archived:0,total:0},receipts:[{action:'read',id:'n-1',outcome:'maybe'},{action:'read',id:'n-2',outcome:'applied'},7]});
 assert.deepEqual(newer.receipts,[{action:'read',id:'n-2',outcome:'applied'}]);
 assert.equal(newer.revision,8);
 assert.throws(()=>parseNotificationBatchResult({revision:-8,audience:'all',applied:0,
  counts:{unread:0,read:0,archived:0,total:0},receipts:[]}));
 assert.throws(()=>parseNotificationBatchResult({revision:8,audience:'all',applied:0,
  counts:{unread:0,read:0,archived:0,total:0},receipts:Array.from({length:257},()=>({action:'read',outcome:'applied'}))}));
});

test('the summary and the broadcast report parse to their published shapes',()=>{
 assert.equal(parseNotificationSummary({revision:4,audience:'profile',observedAt:1,
  counts:{unread:2,read:1,archived:0,total:3}}).counts.unread,2);
 const broadcast=parseNotificationBroadcastResult({audience:'profile',delivered:3,created:2,updated:1,
  revision:11,dedupeKey:'maintenance-window',ids:['a','b']});
 assert.equal(broadcast.delivered,3);
 assert.throws(()=>parseNotificationBroadcastResult({audience:'profile',delivered:-1,created:0,updated:0,
  revision:1,dedupeKey:'k',ids:[]}));
});

// The exact document GET /v1/notifications/capabilities publishes today.
const publishedVocabulary={
 audiences:['profile','account-admin','all'],
 severities:['info','warning','critical'],
 states:['unread','read','archived','all'],
 sources:['feedback','downloads','dvr','security','scan','networking','storage','broadcast'],
 commands:['retry-job','open-download','dismiss-conflict','review-device','open-feedback','run-scan'],
 views:['home','library','item','show','season','episode','person','collection','downloads','recordings',
  'recording','live','search','feedback','notifications','settings','settings-storage','settings-network',
  'settings-security','settings-jobs','admin-jobs','admin-feedback','admin-devices'],
 maxLimit:100,heartbeatMs:20000,maxWaitMs:60000};

test('the published vocabularies parse frozen, with only known commands supported',()=>{
 const parsed=parseNotificationVocabulary(publishedVocabulary);
 assert.deepEqual(parsed.audiences,publishedVocabulary.audiences);
 assert.deepEqual(parsed.sources,publishedVocabulary.sources);
 assert.deepEqual(parsed.views,publishedVocabulary.views);
 assert.equal(parsed.maxLimit,100);
 // The listeners these described are gone; the values are kept exact and grant nothing.
 assert.equal(parsed.heartbeatMs,20000);
 assert.equal(parsed.maxWaitMs,60000);
 assert.deepEqual(parsed.supportedCommands,[...notificationCommands]);
 assert.ok(Object.isFrozen(parsed));
 for(const list of [parsed.audiences,parsed.severities,parsed.states,parsed.sources,parsed.commands,parsed.views,parsed.supportedCommands])
  assert.ok(Object.isFrozen(list));
 // A field a newer server adds is read rather than refused, exactly like the other documents.
 assert.equal(parseNotificationVocabulary({...publishedVocabulary,futureField:1}).maxLimit,100);
});

test('a command this build does not know stays published but is never supported',()=>{
 const parsed=parseNotificationVocabulary(publishedVocabulary);
 assert.deepEqual(parsed.commands,publishedVocabulary.commands);
 assert.deepEqual(parseNotificationVocabulary({...publishedVocabulary,commands:['run-scan','teleport-server']}).supportedCommands,['run-scan']);
 assert.equal(parseNotificationVocabulary({...publishedVocabulary,commands:['run-scan','teleport-server']}).commands.length,2);
 // Publication order follows the server, support order follows this build's allowlist.
 assert.deepEqual(parseNotificationVocabulary({...publishedVocabulary,commands:['run-scan','retry-job']}).supportedCommands,['retry-job','run-scan']);
 assert.deepEqual(parseNotificationVocabulary({...publishedVocabulary,commands:[]}).supportedCommands,[]);
});

test('a vocabulary outside its closed shape is refused, never half-read',()=>{
 assert.throws(()=>parseNotificationVocabulary([]));
 const missing=({...publishedVocabulary} as Record<string,unknown>); delete missing.views;
 assert.throws(()=>parseNotificationVocabulary(missing));
 assert.throws(()=>parseNotificationVocabulary({...publishedVocabulary,commands:{}}));
 assert.throws(()=>parseNotificationVocabulary({...publishedVocabulary,states:null}));
 assert.throws(()=>parseNotificationVocabulary({...publishedVocabulary,maxLimit:0}));
 assert.throws(()=>parseNotificationVocabulary({...publishedVocabulary,maxLimit:'100'}));
 assert.throws(()=>parseNotificationVocabulary({...publishedVocabulary,heartbeatMs:-1}));
 assert.throws(()=>parseNotificationVocabulary({...publishedVocabulary,maxWaitMs:1.5}));
 // A list longer than one vocabulary can carry is a size limit, not a newer server.
 assert.throws(()=>parseNotificationVocabulary({...publishedVocabulary,commands:Array.from({length:33},(_,i)=>`command-${i}`)}));
 assert.throws(()=>parseNotificationVocabulary({...publishedVocabulary,views:Array.from({length:65},(_,i)=>`view-${i}`)}));
 assert.throws(()=>parseNotificationVocabulary({...publishedVocabulary,audiences:['profile','account-admin','all','everyone','nobody']}));
 // A token longer than the longest one an action may carry is malformed.
 assert.throws(()=>parseNotificationVocabulary({...publishedVocabulary,commands:['x'.repeat(81)]}));
 assert.throws(()=>parseNotificationVocabulary({...publishedVocabulary,views:['']}));
 assert.throws(()=>parseNotificationVocabulary({...publishedVocabulary,commands:[7]}));
});

test('a vocabulary token is non-empty, well-formed text with no control characters',()=>{
 for(const token of ['run\u0000scan','run\u0007scan','\u001f','run\u007fscan','\u009f','\ud800','\udc00','run\ud800scan',''])
  assert.throws(()=>parseNotificationVocabulary({...publishedVocabulary,commands:[token]}));
 // Well-formed text outside ASCII stays exactly as published; no pattern narrowing.
 const published=parseNotificationVocabulary({...publishedVocabulary,commands:['run😀scan','команда']});
 assert.deepEqual(published.commands,['run😀scan','команда']);
 assert.deepEqual(published.supportedCommands,[]);
});

test('a repeated vocabulary token is one permission, and the parsed document cannot be edited',()=>{
 const parsed=parseNotificationVocabulary({...publishedVocabulary,
  audiences:['profile','profile','account-admin'],commands:['run-scan','run-scan'],views:['home','home']});
 assert.deepEqual(parsed.audiences,['profile','account-admin']);
 assert.deepEqual(parsed.commands,['run-scan']);
 assert.deepEqual(parsed.views,['home']);
 assert.throws(()=>{(parsed.commands as unknown as string[]).push('run-scan');});
 assert.throws(()=>{(parsed as unknown as {maxLimit:number}).maxLimit=1;});
});

const capabilities={
 revision:'e1.0',canSubmit:true,
 kinds:[{id:'playback',label:'Playback',categories:[
  {id:'buffering',label:'Buffering',description:'It keeps pausing.',wantsPlaybackSession:true,wantsItem:true}]}],
 maxMessageLength:2000,minMessageLength:8,diagnosticsSupported:true,diagnosticsOptional:true,
 duplicateWindowHours:24,retentionDays:180,statuses:['open','in-progress','resolved','closed'],
 diagnosticsDecisions:['attached','unavailable','declined','not-requested'],
 perProfileHourlyLimit:10,reporterName:'Sam',reporterAuthority:'local'};

test('feedback capabilities say what may be sent and why it may not',()=>{
 const parsed=parseFeedbackCapabilities(capabilities);
 assert.equal(parsed.kinds[0].categories[0].wantsPlaybackSession,true);
 const blocked=parseFeedbackCapabilities({...capabilities,canSubmit:false,
  submitBlockedReason:"This profile's access has been revoked on this server."});
 assert.equal(blocked.canSubmit,false);
 assert.match(blocked.submitBlockedReason??'',/revoked/);
 // A kind with no categories cannot be rendered as a two-level chooser.
 assert.throws(()=>parseFeedbackCapabilities({...capabilities,kinds:[{id:'x',label:'X',categories:[]}]}));
});

test('a feedback kind or category that cannot be read is not offered, and the rest still are',()=>{
 const category=capabilities.kinds[0].categories[0];
 const parsed=parseFeedbackCapabilities({...capabilities,futureField:1,kinds:[
  {id:'',label:'No id',categories:[category]},
  {id:'playback',label:'Playback',futureField:1,categories:[category,{...category,id:'stutter',wantsItem:'sometimes'},{...category,id:'audio'}]},
  {id:'empty',label:'Only unreadable categories',categories:[{id:'x'}]},
  'not a kind',
 ]});
 assert.deepEqual(parsed.kinds.map(kind=>kind.id),['playback']);
 assert.deepEqual(parsed.kinds[0].categories.map(c=>c.id),['buffering','audio']);
 assert.equal(parsed.maxMessageLength,2000);
 // No readable kind leaves nothing to choose; the limits a person is held to stay exact.
 assert.throws(()=>parseFeedbackCapabilities({...capabilities,kinds:[{id:'',label:'X',categories:[category]}]}));
 assert.throws(()=>parseFeedbackCapabilities({...capabilities,kinds:Array.from({length:21},(_,i)=>({id:'k'+i,label:'K',categories:[category]}))}));
 assert.throws(()=>parseFeedbackCapabilities({...capabilities,canSubmit:'yes'}));
 assert.throws(()=>parseFeedbackCapabilities({...capabilities,maxMessageLength:-1}));
 assert.throws(()=>parseFeedbackCapabilities({...capabilities,perProfileHourlyLimit:undefined}));
});

const report={
 id:'r-1',cursor:'4',revision:1,status:'open',kind:'playback',category:'buffering',
 message:'It pauses every minute.',itemId:'item-1',createdAt:1,updatedAt:1,expiresAt:9,
 reporter:{name:'Sam',authority:'local',role:'member',self:true},
 diagnostics:{decision:'attached',reference:'playback-session:s-1',detail:'Linked.'},
 duplicates:0,thread:[{sequence:1,at:1,revision:1,status:'open',reply:'',actorClass:'viewer'}]};

test('a duplicate submission returns the existing report rather than a new one',()=>{
 const created=parseFeedbackSubmission({report,duplicate:false,created:true});
 assert.equal(created.created,true);
 const duplicate=parseFeedbackSubmission({report:{...report,duplicates:1},duplicate:true,created:false,duplicateOf:report.id});
 assert.equal(duplicate.duplicate,true);
 assert.equal(duplicate.duplicateOf,report.id);
 assert.equal(duplicate.created,false);
 assert.equal(duplicate.report.id,created.report.id);
 assert.equal(duplicate.report.duplicates,1);
});

test('every diagnostics decision parses, and an invented one claims nothing was attached',()=>{
 for(const decision of ['attached','unavailable','declined','not-requested']){
  const parsed=parseFeedbackSubmission({report:{...report,diagnostics:{decision}},duplicate:false,created:true});
  assert.equal(parsed.report.diagnostics.decision,decision);
 }
 const parsed=parseFeedbackSubmission({report:{...report,diagnostics:{decision:'maybe',detail:'Linked.'}},duplicate:false,created:true});
 assert.equal(parsed.report.diagnostics.decision,'unavailable');
 assert.equal(parsed.report.diagnostics.detail,'Linked.');
 assert.throws(()=>parseFeedbackSubmission({report:{...report,diagnostics:{decision:7}},duplicate:false,created:true}));
 assert.throws(()=>parseFeedbackSubmission({report:{...report,diagnostics:{decision:'attached',reference:'x'.repeat(241)}},duplicate:false,created:true}));
});

test('a sent report is acknowledged whatever status a newer server gives it',()=>{
 const sent=parseFeedbackSubmission({report:{...report,status:'triaged',futureField:1},duplicate:false,created:true,futureField:1});
 assert.equal(sent.created,true);
 assert.equal(sent.report.id,'r-1');
 assert.equal(sent.report.status,'open');
 // The report's own identity and revision are not guessed at.
 assert.throws(()=>parseFeedbackSubmission({report:{...report,id:''},duplicate:false,created:true}));
 assert.throws(()=>parseFeedbackSubmission({report:{...report,revision:-1},duplicate:false,created:true}));
 assert.throws(()=>parseFeedbackSubmission({report:{...report,status:7},duplicate:false,created:true}));
 assert.throws(()=>parseFeedbackSubmission({report,duplicate:'no',created:true}));
});

test('a report this app cannot read is left out of the triage list, and so is one thread entry',()=>{
 const event={sequence:2,at:2,revision:2,status:'resolved',reply:'Fixed.',actorClass:'owner'};
 const parsed=parseFeedbackList({items:[
  {...report,id:'r-1',thread:[report.thread[0],{...event,sequence:-1},event]},
  {...report,id:'r-2',status:'triaged'},
  {...report,id:'r-3',duplicates:-1},
  {...report,id:'r-4'},
 ],nextCursor:'',total:4,observedAt:2,statusCounts:{open:4},filter:{}});
 assert.deepEqual(parsed.items.map(item=>item.id),['r-1','r-4']);
 assert.deepEqual(parsed.items[0].thread.map(entry=>entry.sequence),[1,2]);
 assert.equal(parsed.total,4);
 assert.throws(()=>parseFeedbackReport({...report,status:'triaged'}));
 assert.throws(()=>parseFeedbackList({items:Array.from({length:101},()=>report),nextCursor:'',total:1,observedAt:2,statusCounts:{},filter:{}}));
 assert.throws(()=>parseFeedbackList({items:[report],nextCursor:'x'.repeat(65),total:1,observedAt:2,statusCounts:{},filter:{}}));
 // A thread longer than a report carries is a size limit: that report is not kept in part.
 assert.deepEqual(parseFeedbackList({items:[{...report,id:'r-long',thread:Array.from({length:201},()=>event)},report],nextCursor:'',total:2,observedAt:2,statusCounts:{},filter:{}}).items.map(item=>item.id),['r-1']);
 assert.throws(()=>parseFeedbackReport({...report,thread:Array.from({length:201},()=>event)}));
});

test('the triage list carries its status counts with the page',()=>{
 const parsed=parseFeedbackList({items:[report],nextCursor:'',total:3,observedAt:2,
  statusCounts:{open:2,'in-progress':1,resolved:0,closed:0},filter:{kind:'playback'}});
 assert.equal(parsed.statusCounts.open,2);
 assert.equal(parsed.filter.kind,'playback');
 assert.throws(()=>parseFeedbackList({items:[report],nextCursor:'',total:1,observedAt:2,
  statusCounts:{open:'many'},filter:{}}));
});
