import {strict as assert} from 'node:assert';
import {test} from 'node:test';
import {canReadRecordingTuners} from '../src/screens/live/recording-status-access.ts';

const owner = {viewer: {role: 'owner', serverId: 'server'}, primaryProfile: true, capabilities: {manageServer: true}};

test('Recordings tuner detail requires the actual primary owner and server capability', () => {
  assert.equal(canReadRecordingTuners(owner, 'server'), true);
  assert.equal(canReadRecordingTuners({...owner, primaryProfile: false}, 'server'), false);
  assert.equal(canReadRecordingTuners({...owner, primaryProfile: undefined}, 'server'), false);
  assert.equal(canReadRecordingTuners({...owner, capabilities: undefined}, 'server'), false);
  assert.equal(canReadRecordingTuners({...owner, capabilities: {manageServer: false}}, 'server'), false);
});

test('recording or storage access never admits a member, administrator or another server', () => {
  assert.equal(canReadRecordingTuners(undefined, 'server'), false);
  assert.equal(canReadRecordingTuners({...owner, viewer: {role: 'member', serverId: 'server'}}, 'server'), false);
  assert.equal(canReadRecordingTuners({...owner, viewer: {role: 'admin', serverId: 'server'}}, 'server'), false);
  assert.equal(canReadRecordingTuners(owner, 'other-server'), false);
});
