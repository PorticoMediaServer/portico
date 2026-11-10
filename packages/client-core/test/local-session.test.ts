import {test} from 'node:test';
import assert from 'node:assert/strict';
import {withUnknownFields} from './unknown-fields.ts';
import {currentUTCTimestamp,parseLocalSession,utcOrderKey} from '../src/local-session.ts';

const future = '2099-01-01T00:00:00Z';
const horizon = '2099-04-01T00:00:00Z';
const viewer = {accountId: 'a', profileId: 'p', serverId: 's', authority: 'local', role: 'owner'};
const session = (over: Record<string, unknown> = {}) => ({
  accessToken: 't'.repeat(43),
  expiresAt: future,
  viewer: {...viewer},
  sessionFamilyId: 'fam-1',
  tokenGeneration: '1',
  authorizationHorizon: horizon,
  ...over,
});

test('timestamps accept Z and +00:00 with fractions, and refuse the impossible', () => {
  assert.equal(currentUTCTimestamp('2026-09-17T10:00:00Z'), true);
  assert.equal(currentUTCTimestamp('2026-09-17T10:00:00+00:00'), true);
  assert.equal(currentUTCTimestamp('2026-09-17T10:00:00.123456789Z'), true);
  assert.equal(currentUTCTimestamp('2026-09-17T10:00:00.1+00:00'), true);
  for (const bad of ['', 'not a date', '2026-09-17', '2026-09-17T10:00:00', '2026-13-01T00:00:00Z',
    '2026-02-30T00:00:00Z', '2026-09-17T24:00:00Z', '2026-09-17T10:61:00Z', null, 123, {}, []]) {
    assert.equal(currentUTCTimestamp(bad), false, JSON.stringify(bad));
  }
});

test('order keys sort fractions numerically and treat Z like +00:00', () => {
  assert.ok(utcOrderKey('2026-09-17T10:00:00.1Z') < utcOrderKey('2026-09-17T10:00:00.2Z'));
  assert.ok(utcOrderKey('2026-09-17T10:00:00Z') < utcOrderKey('2026-09-17T10:00:01Z'));
  assert.equal(utcOrderKey('2026-09-17T10:00:00Z'), utcOrderKey('2026-09-17T10:00:00+00:00'));
  assert.equal(utcOrderKey('2026-09-17T10:00:00.1Z'), utcOrderKey('2026-09-17T10:00:00.100000000Z'));
});

test('a fresh session parses; an expired one is refused unless stored', () => {
  assert.equal(parseLocalSession(session()).viewer.accountId, 'a');
  assert.throws(() => parseLocalSession(session({expiresAt: '2026-01-01T00:00:00Z'})), {code: 'invalid_current_session'});
  assert.equal(parseLocalSession(session({expiresAt: '2026-01-01T00:00:00Z'}), {}, {stored: true}).sessionFamilyId, 'fam-1');
});

test('empty, oversized and control-character fields are refused', () => {
  for (const bad of [undefined, null, {}, [], '', 'x', {accessToken: ''}]) {
    assert.throws(() => parseLocalSession(bad), {code: 'invalid_current_session'}, JSON.stringify(bad));
  }
  assert.throws(() => parseLocalSession(session({accessToken: ''})));
  assert.throws(() => parseLocalSession(session({accessToken: 't'.repeat(16385)})));
  assert.equal(parseLocalSession(session({accessToken: 't'.repeat(16384)})).accessToken.length, 16384);
  assert.throws(() => parseLocalSession(session({accessToken: 'bad\x00token'})));
  assert.throws(() => parseLocalSession(session({viewer: {...viewer, accountId: ''}})));
  assert.throws(() => parseLocalSession(session({viewer: {...viewer, accountId: 'a'.repeat(257)}})));
  assert.equal(parseLocalSession(session({viewer: {...viewer, accountId: 'a'.repeat(256)}})).viewer.accountId.length, 256);
});

test('a viewer mismatch is refused with its own words', () => {
  assert.throws(
    () => parseLocalSession(session(), {accountId: 'someone-else'}),
    /does not match the selected account/,
  );
});

test('the server\'s own answer reads the same as the record this device saves', () => {
  const {sessionFamilyId, authorizationHorizon, viewer: saved, ...rest} = session();
  const wire = {...rest, sessionId: sessionFamilyId, sessionExpiresAt: authorizationHorizon, viewer: {accountId: saved.accountId, profileId: saved.profileId, serverId: saved.serverId, role: saved.role}};
  assert.deepEqual(parseLocalSession(wire), parseLocalSession(session()));
  assert.deepEqual(parseLocalSession(JSON.parse(JSON.stringify(parseLocalSession(wire)))), parseLocalSession(wire), 'and reads back after it is saved');
});

test('a sign-in that has not chosen a profile has the role account and no profile; any other role needs one', () => {
  const {profileId: _none, ...account} = {...viewer, role: 'account'};
  assert.equal(parseLocalSession(session({viewer: account})).viewer.profileId, '');
  const {profileId: _missing, ...owner} = viewer;
  assert.throws(() => parseLocalSession(session({viewer: owner})), {code: 'invalid_current_session'});
});

test('family, generation and horizon bounds hold at the edges', () => {
  for (const bad of ['', 'bad id!', 'x'.repeat(129)]) {
    assert.throws(() => parseLocalSession(session({sessionFamilyId: bad})), {code: 'invalid_current_session'}, String(bad));
  }
  assert.equal(parseLocalSession(session({sessionFamilyId: 'x'.repeat(128)})).sessionFamilyId.length, 128);
  for (const bad of ['0', '', '01', 'x', '9223372036854775808', '1'.repeat(20)]) {
    assert.throws(() => parseLocalSession(session({tokenGeneration: bad})), {code: 'invalid_current_session'}, String(bad));
  }
  assert.equal(parseLocalSession(session({tokenGeneration: '9223372036854775807'})).tokenGeneration, '9223372036854775807');
  // The access token may not outlive the authorization horizon.
  assert.throws(() => parseLocalSession(session({expiresAt: horizon, authorizationHorizon: future})), {code: 'invalid_current_session'});
});

test('device, installation and refresh bounds hold', () => {
  assert.throws(() => parseLocalSession(session({deviceId: ''})));
  assert.throws(() => parseLocalSession(session({installationId: ''})));
  assert.throws(() => parseLocalSession(session({refreshToken: 'short'})));
  assert.throws(() => parseLocalSession(session({refreshToken: 'x'.repeat(513)})));
  assert.equal(parseLocalSession(session({refreshToken: 'x'.repeat(16)})).refreshToken?.length, 16);
  assert.equal(parseLocalSession(session({refreshToken: 'x'.repeat(512)})).refreshToken?.length, 512);
  assert.throws(() => parseLocalSession(session({refreshToken: 'has space in it 123456'})));
});

test('a stored record forgives expiry, nothing else', () => {
  const past = session({expiresAt: '2026-01-01T00:00:00Z'});
  assert.equal(parseLocalSession(past, {}, {stored: true}).sessionFamilyId, 'fam-1');
  assert.throws(() => parseLocalSession({...past, sessionFamilyId: 'bad id!'}, {}, {stored: true}));
});

test('a sign-in answer from a newer server reads the same, and its unknown fields are not kept', () => {
  const key = 'k'.repeat(43);
  const answer = session({deviceId: 'device', installationId: 'inst', refreshToken: 'r'.repeat(43), serverIdentity: {publicKey: key, fingerprint: key}});
  assert.deepEqual(parseLocalSession(withUnknownFields(answer)), parseLocalSession(answer));
});
