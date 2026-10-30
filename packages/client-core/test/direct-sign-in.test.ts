import {test} from 'node:test';
import assert from 'node:assert/strict';
import {completeDirectSignInChallenge, directSignIn, directSignInContinuation, parseDirectSignIn, parseDirectSnapshot} from '../src/profile-management.ts';

// The exact documented direct sign-in shapes (`DirectSignIn` and `Envelope`). No accountToken/accountExpiresAt; one device-bound
// session that names its device and installation.
const profile = (id: string, over: Record<string, unknown> = {}) => ({id, name: id, primary: id === 'p1', position: id === 'p1' ? 0 : 1, art: 'blue', revision: 1, pinRevision: 1, pinRequired: false, allowedLibraries: [], ...over});
const snapshot = (profiles: unknown[]) => ({authority: 'local', serverId: 'srv', account: {id: 'acct', username: 'owner', primaryProfileId: 'p1', role: 'owner', revision: 1, disabled: false, allowedLibraries: []}, profiles, canManage: true, capabilities: {manageAccount:true,managePeople:true,manageServer:true,manageSignIn:true}, deviceId: 'dev', installationId: 'inst'});
const session = (role: string) => ({accessToken: 'a'.repeat(43), refreshToken: 'r'.repeat(43), deviceId: 'dev', installationId: 'inst', expiresAt: '2099-01-01T00:00:00Z', viewer: {accountId: 'acct', profileId: 'p1', serverId: 'srv', authority: 'local', role}, sessionFamilyId: 'fam', tokenGeneration: '1', authorizationHorizon: '2099-03-01T00:00:00Z'});

test('several profiles: an account-scoped session, and a profile is chosen next', () => {
  const r = parseDirectSignIn({...snapshot([profile('p1'), profile('p2')]), session: session('account')});
  assert.equal(r.kind, 'signed-in');
  if (r.kind !== 'signed-in') return;
  assert.equal(r.accountScoped, true);
  assert.equal(r.passwordChangeRequired, false);
  assert.equal(r.session.accessToken, 'a'.repeat(43));
  assert.equal(r.session.installationId, 'inst');
  assert.equal(r.session.deviceId, 'dev');
  assert.equal(r.snapshot.profiles.length, 2);
});

test('one unprotected profile: the session is the viewer', () => {
  const r = parseDirectSignIn({...snapshot([profile('p1')]), session: session('owner')});
  assert.ok(r.kind === 'signed-in' && !r.accountScoped);
});

test('a viewer session is refused where a profile must be chosen or a password changed', () => {
  assert.throws(() => parseDirectSignIn({...snapshot([profile('p1'), profile('p2')]), session: session('owner')}));
  assert.throws(() => parseDirectSignIn({...snapshot([profile('p1', {pinRequired: true})]), session: session('owner')}));
  assert.throws(() => parseDirectSignIn({...snapshot([profile('p1')]), passwordChangeRequired: true, session: session('owner')}));
  const forced = parseDirectSignIn({...snapshot([profile('p1')]), passwordChangeRequired: true, session: session('account')});
  assert.ok(forced.kind === 'signed-in' && forced.passwordChangeRequired && forced.accountScoped);
});

test('the old account-token shape is no longer accepted (a session is required)', () => {
  assert.throws(() => parseDirectSignIn({...snapshot([profile('p1'), profile('p2')]), accountToken: 't'.repeat(43), accountExpiresAt: '2099-03-01T00:00:00Z'}));
});

test('CD-34: direct admin profile management keeps owner/admin/member', () => {
  for (const role of ['owner', 'admin', 'member']) {
    const r = parseDirectSignIn({...snapshot([profile('p1')]), account: {...(snapshot([profile('p1')] as any) as any).account, role}, capabilities: {manageAccount:true,managePeople:role!=='member',manageServer:role!=='member',manageSignIn:role==='owner'}, session: session(role)});
    assert.equal(r.kind, 'signed-in');
    if (r.kind !== 'signed-in') continue;
    assert.equal(r.snapshot.account.role, role);
  }
  assert.throws(() => parseDirectSignIn({...snapshot([profile('p1')]), account: {...(snapshot([profile('p1')] as any) as any).account, role: 'viewer'}, session: session('owner')}));
});

test('two-step sign-in returns a challenge, finished with a code', async () => {
  const r = parseDirectSignIn({challenge: {token: 'c'.repeat(43), expiresAt: '2099-01-01T00:00:00Z'}});
  assert.deepEqual(r, {kind: 'challenge', token: 'c'.repeat(43), expiresAt: '2099-01-01T00:00:00Z'});
  const calls: unknown[] = [];
  const api = {request: async <T,>(path: string, method?: string, body?: unknown): Promise<T> => { calls.push([path, method, body]); return {...snapshot([profile('p1'), profile('p2')]), session: session('account')} as T; }};
  const done = await completeDirectSignInChallenge(api, 'c'.repeat(43), ' 123456 ');
  assert.equal(done.kind, 'signed-in');
  assert.deepEqual(calls, [['/v1/auth/two-factor/challenge', 'POST', {token: 'c'.repeat(43), code: '123456'}]]);
  await assert.rejects(completeDirectSignInChallenge(api, 'c'.repeat(43), ''));
});

test('retired device approval response is rejected without creating a holding state', () => {
  assert.throws(() => parseDirectSignIn({deviceApprovalPending: true, deviceId: 'dev', installationId: 'inst'}), {code: 'invalid_response'});
});

test('required factor setup stays a restricted account continuation with no envelope authority added',()=>{
 const raw={...snapshot([profile('p1')]),canManage:false,capabilities:{manageAccount:false,managePeople:false,manageServer:false,manageSignIn:false},twoStepSetupRequired:true,session:session('account')};
 const result=parseDirectSignIn(raw);
 assert.equal(result.kind,'signed-in');if(result.kind!=='signed-in')return;
 assert.equal(result.snapshot.twoStepSetupRequired,true);
 assert.equal(result.accountScoped,true);
 assert.equal(directSignInContinuation(result),'two-step-setup');
 assert.equal('twoStepSetupRequired' in result.session,false,'the existing Envelope remains unchanged');
 assert.throws(()=>parseDirectSignIn({...raw,session:session('owner')}));
 assert.throws(()=>parseDirectSignIn({...raw,canManage:true,capabilities:snapshot([]).capabilities}));
 for(const flag of ['true',1,null])assert.throws(()=>parseDirectSignIn({...raw,twoStepSetupRequired:flag}));
 const password=parseDirectSignIn({...raw,passwordChangeRequired:true});
 assert.equal(directSignInContinuation(password),'password-change','password recovery precedes required setup');
 const ordinary=parseDirectSignIn({...raw,twoStepSetupRequired:false});
 assert.equal(directSignInContinuation(ordinary),'profile-selection');
 assert.equal(directSignInContinuation(parseDirectSignIn({challenge:{token:'c'.repeat(43),expiresAt:'2099-01-01T00:00:00Z'}})),'challenge');
});

test('primary-profile admission refuses a viewer role contradicting its actual account membership', () => {
 for(const accountRole of ['owner','admin','member']){
  const base=snapshot([profile('p1')]);
  const actual={...base,account:{...base.account,role:accountRole},capabilities:{manageAccount:true,managePeople:accountRole!=='member',manageServer:accountRole!=='member',manageSignIn:accountRole==='owner'}};
  for(const viewerRole of ['owner','admin','member']){
   if(viewerRole===accountRole)assert.doesNotThrow(()=>parseDirectSignIn({...actual,session:session(viewerRole)}));
   else assert.throws(()=>parseDirectSignIn({...actual,session:session(viewerRole)}));
  }
  const restricted=parseDirectSignIn({...actual,session:session('account')});
  assert.equal(restricted.kind==='signed-in'&&restricted.session.viewer.role,'account');
 }
});
test('disabled account facts remain readable for management but can never become a sign-in', () => {
 const base=snapshot([profile('p1')]),disabled={...base,account:{...base.account,disabled:true}};
 assert.equal(parseDirectSnapshot(disabled).account.disabled,true);
 for(const role of ['owner','account'])assert.throws(()=>parseDirectSignIn({...disabled,session:session(role)}));
});
