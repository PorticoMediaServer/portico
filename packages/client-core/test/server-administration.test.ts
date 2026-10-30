import {test} from 'node:test';
import assert from 'node:assert/strict';
import {
 parseAccessMember,parseAccessMemberPage,parseLimitsDocument,parseMemberLimits,parseInvitation,parseInvitationPage,
 parseAccessDevicePage,parseLogPage,parseLogSettings,parseLogEvent,parseClientLogUploadPage,
 parseConnectivityPolicy,parseConnectivityStatus,grantsTier,managesTier,
 parseMemberDeletionPreview,previewMemberDeletion,deleteMember,
} from '../src/server-administration.ts';

const limits={accessTokens:true,maxProfiles:8,allowRemote:true,conversion:'server',maxStreams:1,maxStreamsMode:'limit',remoteBitrateKbps:4000,maxContentRating:'PG-13',allowUnrated:true,
 schedule:{timezone:'UTC',windows:[{days:[1,3],startMinute:600,endMinute:780}]},
 channelPolicy:{mode:'deny',channels:['news']},tagPolicy:{deniedLabels:['Spoilers']}};

test('stream intent and server ceiling facts remain distinct at the client boundary', () => {
 for (const maxStreamsMode of ['inherit','unlimited','limit']) {
  const maxStreams=maxStreamsMode==='limit'?3:0;
  const parsed=parseMemberLimits({...limits,maxStreamsMode,maxStreams});
  assert.equal(parsed.maxStreamsMode,maxStreamsMode);assert.equal(parsed.maxStreams,maxStreams);
 }
 for (const patch of [{maxStreamsMode:'automatic',maxStreams:0},{maxStreamsMode:'limit',maxStreams:0},{maxStreamsMode:'unlimited',maxStreams:3}]) assert.throws(()=>parseMemberLimits({...limits,...patch}));
 const document=parseLimitsDocument({accountId:'member',revision:1,limits:{...limits,maxStreamsMode:'unlimited',maxStreams:0},serverMaxStreams:2,effectiveMaxStreams:2});
 assert.equal(document.serverMaxStreams,2);assert.equal(document.effectiveMaxStreams,2);assert.equal(document.limits.maxStreamsMode,'unlimited');
 assert.throws(()=>parseLimitsDocument({accountId:'member',revision:1,limits:{...limits,maxStreamsMode:'inherit',maxStreams:0},serverMaxStreams:-1,effectiveMaxStreams:2}));
});

test('member limits round-trip, with the rating kept as the server stored it', () => {
 const parsed = parseMemberLimits(limits);
 assert.equal(parsed.maxContentRating, 'PG-13');
 assert.equal(parsed.ratingSystem, '');
 assert.equal(parsed.maximumAge, -1, 'an older document without the resolved age reads as no limit');
 // A limit set from a published rating system comes back with its system and the age it stands for.
 const british = parseMemberLimits({...limits, ratingSystem: 'bbfc', maxContentRating: '15', maximumAge: 15});
 assert.deepEqual([british.ratingSystem, british.maxContentRating, british.maximumAge], ['bbfc', '15', 15]);
 assert.throws(() => parseMemberLimits({...limits, maximumAge: 40}));
 assert.throws(() => parseMemberLimits({...limits, ratingSystem: 7}));
 // The retired maxSessions field is never sent: it is absent from the parsed
 // document, so a limits save cannot carry it back to a server that rejects it.
 assert.equal('maxSessions' in parsed, false);
  assert.equal(parsed.channelPolicy.mode, 'deny');
  assert.deepEqual([...parsed.tagPolicy.deniedLabels], ['Spoilers']);
  assert.deepEqual([...parsed.schedule.windows[0].days], [1, 3]);
  assert.throws(() => parseMemberLimits({...limits, maxContentRating: 'X'.repeat(40)}));
  assert.throws(() => parseMemberLimits({...limits, maxStreams: 100000}));
  assert.throws(() => parseMemberLimits({...limits, schedule: {timezone: 'UTC', windows: [{days: [], startMinute: 0, endMinute: 10}]}}));
  // The canonical producer publishes these permissions explicitly. Missing values
  // must not silently grant tokens or remote access on the owner screen.
  assert.throws(() => parseMemberLimits({}));
});

test('a limits document is pinned to the account it was read for', () => {
  const document = parseLimitsDocument({accountId: 'member', revision: 3, updatedAt: '2026-09-16T12:00:00Z', limits,serverMaxStreams:null,effectiveMaxStreams:1}, 'member');
  assert.equal(document.revision, 3);
  assert.equal(document.updatedAt, '2026-09-16T12:00:00Z');
  assert.throws(() => parseLimitsDocument({accountId: 'other', revision: 1, limits}, 'member'));
  assert.throws(() => parseLimitsDocument({accountId: 'member', revision: 0, limits}));
});

test('a member carries its tier and both revisions', () => {
  const member = parseAccessMember({accountId: 'a', username: 'ada', profileId: 'p', role: 'admin', disabled: false,
    revision: 2, allowedLibraries: ['lib'], limitsRevision: 1, limits,serverMaxStreams:null,effectiveMaxStreams:1});
  assert.equal(member.role, 'admin');
  assert.equal(member.limitsRevision, 1);
  assert.throws(() => parseAccessMember({accountId: 'a', username: 'ada', profileId: 'p', role: 'root', disabled: false, revision: 1, allowedLibraries: [], limitsRevision: 1, limits}));
  const page = parseAccessMemberPage({limit: 25, nextCursor: 'cursor', items: [{accountId: 'a', username: 'ada', profileId: 'p', role: 'member', disabled: false, revision: 1, allowedLibraries: [], limitsRevision: 1, limits,serverMaxStreams:null,effectiveMaxStreams:1}]});
  assert.equal(page.items.length, 1);
  assert.equal(page.nextCursor, 'cursor');
  // A page that returns more rows than it promised is a server disagreeing with
  // itself, which the client refuses rather than renders.
  assert.throws(() => parseAccessMemberPage({limit: 0, items: []}));
});
test('a member page is refused whole when one member cannot be read, so nobody with access is ever missing from it', () => {
  const member = {accountId: 'a', username: 'ada', profileId: 'p', role: 'member', disabled: false, revision: 1, allowedLibraries: [], limitsRevision: 1, limits,serverMaxStreams:null,effectiveMaxStreams:1};
  const page = parseAccessMemberPage({limit: 25, nextCursor: '', futureField: 1, items: [member,
    {...member, username: 'again'}, {...member, accountId: 'f', username: 'fay', futureField: {a: 1}}]});
  assert.deepEqual(page.items.map(row => [row.accountId, row.username]), [['a', 'ada'], ['f', 'fay']]);
  assert.equal('futureField' in page.items[1], false);
  // A role, a limit or a revision this app cannot read is never guessed and never hidden.
  for (const unreadable of [{...member, accountId: 'b', role: 'root'}, {...member, accountId: 'c', limits: {...limits, maximumAge: 99}},
    {...member, accountId: 'd', limits: {...limits, channelPolicy: {mode: 'sometimes', channels: []}}}, {...member, accountId: 'e', revision: 0}, null])
    assert.throws(() => parseAccessMemberPage({limit: 25, items: [member, unreadable]}));
  // The page size, its cursor and its outer shape still refuse the whole page.
  assert.throws(() => parseAccessMemberPage({limit: 1, items: [member, {...member, accountId: 'b'}]}));
  assert.throws(() => parseAccessMemberPage({limit: 1001, items: [member]}));
  assert.throws(() => parseAccessMemberPage({limit: 25, nextCursor: 7, items: [member]}));
  assert.throws(() => parseAccessMemberPage({limit: 25, items: {}}));
});

test('an invitation code is accepted only where the server issues one', () => {
  const created = parseInvitation({id: 'i1', notes: 'Link for Sam', role: 'member', allowedLibraries: [],
    state: 'pending', createdAt: '2026-09-16T12:00:00Z', expiresAt: '2026-09-23T12:00:00Z', revision: 1, code: 'c'.repeat(43), access: {maxStreamsMode: 'inherit', maxStreams: 0, allowRemote: true, accessTokens: true, maxContentRating: ''}});
  assert.equal(created.code, 'c'.repeat(43));
  const listed = parseInvitation({id: 'i1', role: 'member', allowedLibraries: [],
    state: 'accepted', createdAt: '2026-09-16T12:00:00Z', expiresAt: '2026-09-23T12:00:00Z', acceptedAt: '2026-09-17T12:00:00Z', accountId: 'acc', revision: 2, access: {maxStreamsMode: 'inherit', maxStreams: 0, allowRemote: true, accessTokens: true, maxContentRating: ''}});
  assert.equal(listed.code, undefined);
  assert.equal(listed.accountId, 'acc');
  const linkOnly={id:'i2',role:'member',allowedLibraries:[],state:'pending',createdAt:'2026-09-16T12:00:00Z',expiresAt:'2026-09-23T12:00:00Z',revision:1,access:{maxStreamsMode:'inherit',maxStreams:0,allowRemote:true,accessTokens:true,maxContentRating:''}};
  assert.equal(Object.hasOwn(parseInvitation(linkOnly),'email'),false);
  assert.throws(()=>parseInvitation({...linkOnly,email:'guest@example.com'}));
  assert.throws(() => parseInvitation({...linkOnly,state:'unknown'}));
  assert.throws(() => parseInvitation({...linkOnly,createdAt:'not-a-date'}));
  const page = parseInvitationPage({limit: 2, items: [],totalCount:0,matchedCount:0});
  assert.deepEqual([...page.items], []);
});

test('administrative device pages describe real activity and refuse retired trust/approval policy',()=>{
 const row={id:'living-room',accountId:'a',username:'alice',profileId:'p',profileName:'Evening',name:'Living room',platform:'tvos',app:'Portico',appVersion:'1',lastNetwork:'home',liveState:'watching',firstSeenAt:'2026-09-16T12:00:00Z',lastSeenAt:'2026-09-16T12:30:00Z',revision:1};
 const page=parseAccessDevicePage({limit:25,items:[row]});
 assert.equal(page.items[0].liveState,'watching');assert.equal(page.items[0].profileName,'Evening');
 assert.equal('approvalRequired' in page,false);assert.equal('trust' in page.items[0],false);
 assert.equal(Object.hasOwn(page.items[0],'sessionCount'),false);
 for(const sessionCount of [0,2,Number.MAX_SAFE_INTEGER]) assert.equal(parseAccessDevicePage({limit:25,items:[{...row,sessionCount}]}).items[0].sessionCount,sessionCount);
 for(const sessionCount of [-1,0.5,'2',null,Number.MAX_SAFE_INTEGER+1]) assert.throws(()=>parseAccessDevicePage({limit:25,items:[{...row,sessionCount}]}));
 assert.throws(()=>parseAccessDevicePage({limit:25,approvalRequired:false,items:[row]}));
 assert.throws(()=>parseAccessDevicePage({limit:25,items:[{...row,trust:'approved'}]}));
 assert.throws(()=>parseAccessDevicePage({limit:25,items:[{...row,liveState:'approved'}]}));
});

test('log records keep their sequence as a string so a long-running server does not lose precision', () => {
  const page = parseLogPage({level: 'warn', effectiveLevel: 'debug', limit: 100, nextCursor: '9007199254740993',
    items: [{sequence: '9007199254740993', at: '2026-09-16T12:00:00Z', level: 'error', category: 'playback', message: 'stalled'}]});
  assert.equal(page.items[0].sequence, '9007199254740993');
  assert.equal(page.effectiveLevel, 'debug');
  // A line this app cannot read is left out alone; a level or a category it does not know reads as an informational server line.
  const mixed = parseLogPage({level: 'chatty', effectiveLevel: 'warn', limit: 10, items: [{sequence: 'abc', at: '2026-09-16T12:00:00Z', level: 'error', category: 'server', message: 'x'},
    {sequence: '5', at: '2026-09-16T12:00:00Z', level: 'trace', category: 'transcode', message: 'frame'}, {sequence: '5', at: '2026-09-16T12:00:00Z', level: 'error', category: 'server', message: 'again'}]});
  assert.deepEqual(mixed.items, [{sequence: '5', at: '2026-09-16T12:00:00Z', level: 'info', category: 'server', message: 'frame'}]);
  assert.equal(mixed.level, 'info');
  assert.equal(mixed.effectiveLevel, 'warn');
  assert.throws(() => parseLogPage({level: 'warn', effectiveLevel: 'warn', limit: 1, items: [page.items[0], page.items[0]]}));
  const record = parseLogEvent('{"sequence":"4","at":"2026-09-16T12:00:00Z","level":"info","category":"scan","message":"walking"}');
  assert.equal(record.category, 'scan');
  assert.throws(() => parseLogEvent('not json'));
});

test('log settings publish the level in force and any open debug window', () => {
  const settings = parseLogSettings({revision: 4, logLevel: 'info', effectiveLevel: 'debug', debugWindowUntil: '2026-09-16T12:05:00Z',
    retention: [{category: 'scan', days: 2}], levels: ['error', 'warn', 'info', 'debug'], categories: ['server', 'scan']});
  assert.equal(settings.effectiveLevel, 'debug');
  assert.equal(settings.retention[0].days, 2);
  assert.equal(settings.debugWindowUntil, '2026-09-16T12:05:00Z');
  // No window open: the field is simply absent.
  const quiet = parseLogSettings({revision: 4, logLevel: 'info', effectiveLevel: 'info', retention: [], levels: [], categories: []});
  assert.equal(quiet.debugWindowUntil, undefined);
  assert.throws(() => parseLogSettings({revision: 4, logLevel: 'info', effectiveLevel: 'info', retention: [{category: 'nope', days: 1}], levels: [], categories: []}));
  assert.throws(() => parseLogSettings({revision: 4, logLevel: 'info', effectiveLevel: 'info', retention: [{category: 'scan', days: 0}], levels: [], categories: []}));
});

test('a client log listing carries metadata without bodies', () => {
  const page = parseClientLogUploadPage({limit: 25, items: [{id: 'u1', accountId: 'a', profileId: 'p', deviceId: 'd',
    platform: 'tvos', appVersion: '1.0', receivedAt: '2026-09-16T12:00:00Z', bytes: 42}]});
  assert.equal(page.items[0].body, undefined);
  assert.equal(page.items[0].bytes, 42);
});

test('connectivity policy and status parse the whole report', () => {
  const policy = {revision: 7, remoteSignInPolicy: 'owner-only', remoteBitrateLimitKbps: 8000,
    connectionLimit:0,advertiseUrl:'', lanNetworks: ['192.168.1.0/24'], accessUrls: ['https://media.example.com'], lanDiscoveryEnabled: false};
  const parsed = parseConnectivityPolicy(policy);
  assert.equal(parsed.remoteSignInPolicy, 'owner-only');
  assert.equal(parsed.lanDiscoveryEnabled, false);
  // DLNA is off when an older server does not report its switch.
  assert.equal(parsed.dlnaEnabled, false);
  assert.equal(parseConnectivityPolicy({...policy, dlnaEnabled: true}).dlnaEnabled, true);
  assert.throws(() => parseConnectivityPolicy({...policy, dlnaEnabled: 'on'}));
  assert.throws(() => parseConnectivityPolicy({...policy, connectionLimit: -1}));
  assert.throws(() => parseConnectivityPolicy({...policy, remoteBitrateLimitKbps: -1}));
  const status = parseConnectivityStatus({connection:{secure:false,remote:false,plainHttpOutsideHome:false,clientAddress:'127.0.0.1',accessUrls:[]},observedAt: '2026-09-16T12:00:00Z', policy,
    tls: {configured: true, state: 'active', tlsReady: true, publiclyTrusted: false, listenerBound: true, listenPort: 8443, notAfter: '2026-12-01T00:00:00Z'},
    discovery: {enabled: false, supported: true, state: 'stopped', serviceType: '_portico._tcp.local.'},
    addresses: [{address: '192.168.1.20', family: 'ipv4', class: 'lan', source: 'interface'}],
    interfaces: [{name: 'en0', up: true, loopback: false, addresses: ['192.168.1.20/24']}],
    warnings: ['secure_connections_required_without_tls']});
  assert.equal(status.tls.listenPort, 8443);
  assert.equal(status.discovery.state, 'stopped');
  assert.equal(status.addresses[0].class, 'lan');
  assert.equal(status.warnings.length, 1);
  // A discovery state this app does not know reads as unavailable, and a custom certificate it cannot read is left off.
  const newer = parseConnectivityStatus({connection: {secure: false, remote: true, plainHttpOutsideHome: true, clientAddress: '203.0.113.9', accessUrls: ['https://media.example', 7], later: true}, observedAt: '2026-09-16T12:00:00Z', policy, futureField: 1,
    tls: {configured: true, tlsReady: true, publiclyTrusted: false, listenerBound: true, listenPort: 8443, customCertificate: {state: 'renewing'}},
    discovery: {enabled: true, supported: true, state: 'humming'}, addresses: [], interfaces: [], warnings: []});
  assert.equal(newer.discovery.state, 'unavailable');
  assert.equal(newer.tls.customCertificate, undefined);
  assert.equal(newer.tls.listenPort, 8443);
  // What the server says about this connection is read with the rest, and an advertised address that cannot be read is left out alone.
  assert.deepEqual(newer.connection, {secure: false, remote: true, plainHttpOutsideHome: true, clientAddress: '203.0.113.9', accessUrls: ['https://media.example']});
  // A status that says nothing readable about the connection claims nothing about it.
  const silent = parseConnectivityStatus({connection: 'unknown', observedAt: '2026-09-16T12:00:00Z', policy,
    tls: {configured: true, tlsReady: true, publiclyTrusted: false, listenerBound: true, listenPort: 8443},
    discovery: {enabled: true, supported: true, state: 'stopped'}, addresses: [], interfaces: [], warnings: []});
  assert.deepEqual(silent.connection, {secure: false, remote: false, plainHttpOutsideHome: false, clientAddress: '', accessUrls: []});
});

test('CD-39: a host-sized report renders bounded slices with exact omitted counts', () => {
  const policy = {revision: 7, remoteSignInPolicy: 'allow', remoteBitrateLimitKbps: 0,
    connectionLimit:0,advertiseUrl:'', lanNetworks: [], accessUrls: [], lanDiscoveryEnabled: false};
  const base = {connection:{secure:false,remote:false,plainHttpOutsideHome:false,clientAddress:'127.0.0.1',accessUrls:[]},observedAt: '2026-09-16T12:00:00Z', policy,
    tls: {configured: false, tlsReady: false, publiclyTrusted: false, listenerBound: false, listenPort: 0},
    discovery: {enabled: false, supported: false, state: 'unavailable'}, warnings: []};
  const address = (n: number) => ({address: `10.0.0.${n % 256}`, family: 'ipv4', class: 'lan', source: 'interface'});
  const iface = (n: number) => ({name: `veth${n}`, up: true, loopback: false, addresses: [`10.0.0.${n % 256}/24`]});
  const status = parseConnectivityStatus({...base,
    addresses: Array.from({length: 257}, (_, n) => address(n)),
    interfaces: Array.from({length: 129}, (_, n) => iface(n))});
  assert.equal(status.addresses.length, 256);
  assert.equal(status.interfaces.length, 128);
  assert.equal(status.omittedAddresses, 1);
  assert.equal(status.omittedInterfaces, 1);
  // Retained entries are still validated: one that cannot be read is left out and counted with the omitted ones.
  const partial = parseConnectivityStatus({...base, addresses: [{...address(0), family: 'carrier-pigeon'}, address(1)], interfaces: [iface(0), {name: ''}]});
  assert.deepEqual(partial.addresses.map(entry => entry.address), ['10.0.0.1']);
  assert.equal(partial.omittedAddresses, 1);
  assert.equal(partial.interfaces.length, 1);
  assert.equal(partial.omittedInterfaces, 1);
  assert.throws(() => parseConnectivityStatus({...base, addresses: [], interfaces: [{...iface(0), addresses: new Array(65).fill('10.0.0.1/24')}]}));
  // The policy enums stay strict.
  assert.throws(() => parseConnectivityStatus({...base, policy: {...policy, remoteSignInPolicy: 'sometimes'}, addresses: [], interfaces: []}));
});

test('the tier helpers mirror the server predicate', () => {
  assert.ok(grantsTier('owner', 'admin'));
  assert.ok(grantsTier('admin', 'admin'));
  assert.ok(!grantsTier('member', 'admin'));
  assert.ok(!grantsTier('admin', 'owner'));
  assert.ok(!grantsTier('root', 'member'));
  assert.ok(managesTier('owner', 'admin'));
  assert.ok(managesTier('admin', 'member'));
  assert.ok(!managesTier('admin', 'admin'));
  assert.ok(!managesTier('owner', 'owner'));
  assert.ok(!managesTier('member', 'member'));
});

test('connectivity policy: network parity rows parse, with shipped defaults for an older server', () => {
  const base = {revision: 4, remoteSignInPolicy: 'allow', remoteBitrateLimitKbps: 0, connectionLimit:0,advertiseUrl:'', lanNetworks: [], accessUrls: ['https://media.example.com'], lanDiscoveryEnabled: true};
  const older = parseConnectivityPolicy(base);
  assert.deepEqual([older.trustedProxies, older.uploadCapacityKbps, older.pausedSessionTimeoutMinutes, older.advertisedInterface, older.customCertificateDomain], [[], 0, 0, '', '']);
  const newer = parseConnectivityPolicy({...base, trustedProxies: ['127.0.0.1/32'], uploadCapacityKbps: 20000, pausedSessionTimeoutMinutes: 60, advertisedInterface: 'br0',
    customCertificatePath: '/certs/full.pem', customCertificateKeyPath: '/certs/key.pem', customCertificateDomain: 'media.example.com'});

  assert.equal(newer.uploadCapacityKbps, 20000);
  assert.equal(newer.advertisedInterface, 'br0');
  assert.equal(newer.customCertificateKeyPath, '/certs/key.pem');
  assert.throws(() => parseConnectivityPolicy({...base, pausedSessionTimeoutMinutes: 5000}));
});

test('member deletion: the preview is read, checked and confirmed with its revision', async () => {
 const doc={accountId:'acct_1',username:'sam',role:'member',revision:4,porticoAccount:false,profiles:2,devices:3,playlists:1,savedResources:0,playsKept:57};
 assert.equal(parseMemberDeletionPreview(doc).playsKept, 57);
 assert.throws(() => parseMemberDeletionPreview({...doc, role: 'superuser'}));
 assert.throws(() => parseMemberDeletionPreview({...doc, revision: 0}));
 assert.throws(() => parseMemberDeletionPreview({...doc, profiles: -1}));
 const calls: {path: string; method?: string; body?: unknown}[] = [];
 const api={request:async <T,>(path:string,method?:string,body?:unknown):Promise<T>=>{calls.push({path,method,body});return (method==='GET'?doc:undefined) as T;}};
 const preview=await previewMemberDeletion(api,'acct_1');
 await deleteMember(api,preview);
 assert.deepEqual(calls,[
  {path:'/v1/direct/members/acct_1/deletion-preview',method:'GET',body:undefined},
  {path:'/v1/direct/members/acct_1',method:'DELETE',body:{expectedRevision:4}},
 ]);
 // A preview that answers for a different member is refused rather than confirmed.
 await assert.rejects(previewMemberDeletion(api,'acct_2'));
});
