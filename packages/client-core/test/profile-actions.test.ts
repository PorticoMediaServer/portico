import test from 'node:test';
import assert from 'node:assert/strict';
import {profileActions} from '../src/presentation/index.ts';
import {enUS} from '../../i18n/src/index.ts';
import {isIconId} from '../../design/src/index.ts';

test('CON-12: profileActions order and permission filtering', () => {
  const full = profileActions({hasPicture: true, pinRequired: true, primary: false}, {canManage: true, isCurrent: false});
  assert.deepEqual(full.map(a => a.id), ['picture', 'removePicture', 'rename', 'limits', 'changePin', 'forgetDevices', 'delete']);
  assert.equal(full.at(-1)!.destructive, true);

  const noPicture = profileActions({hasPicture: false, pinRequired: false, primary: false}, {canManage: true});
  assert.deepEqual(noPicture.map(a => a.id), ['picture', 'rename', 'limits', 'changePin', 'forgetDevices', 'delete']);

  const forgot = profileActions({hasPicture: false, pinRequired: true, primary: false}, {canManage: false, isCurrent: true});
  assert.ok(forgot.some(a => a.id === 'forgotPin'), 'non-manager with PIN gets Forgot PIN');
  assert.ok(!forgot.some(a => a.id === 'changePin'));
  assert.ok(!forgot.some(a => a.id === 'rename'), 'rename needs manage');

  const primary = profileActions({hasPicture: false, pinRequired: false, primary: true}, {canManage: true});
  assert.ok(!primary.some(a => a.id === 'delete'), 'primary never deleted');
});

test('CON-12: labels are catalogue IDs and glyphs are registry IDs', () => {
  for (const a of profileActions({hasPicture: true, pinRequired: true}, {canManage: true})) {
    assert.ok(a.label in enUS, a.label);
    assert.ok(isIconId(a.icon), a.icon);
  }
  // US spelling: Favorite never appears; picture items carry an ellipsis (CON-22).
  assert.ok(enUS['profile.changePicture'].endsWith('…'));
  assert.ok(enUS['profile.addPicture'].endsWith('…'));
  assert.ok(!enUS['profile.removePicture'].endsWith('…'));
});

test('a profile that cannot manage profiles sees its own limits and is offered neither another profile’s limits nor Delete', async () => {
  const {profileActions} = await import('../src/presentation/index.ts');
  const ids = (viewer: {canManage?: boolean; isCurrent?: boolean}, primary = false) => profileActions({hasPicture: false, pinRequired: false, primary}, viewer).map(a => a.id);
  assert.ok(ids({canManage: false, isCurrent: true}).includes('limits'), 'its own limits can be looked at');
  assert.equal(ids({canManage: false, isCurrent: true}).includes('delete'), false);
  assert.equal(ids({canManage: false, isCurrent: false}).includes('limits'), false);
  assert.equal(ids({canManage: false, isCurrent: false}).includes('delete'), false);
  assert.ok(ids({canManage: true, isCurrent: false}).includes('limits'));
  assert.ok(ids({canManage: true, isCurrent: false}).includes('delete'));
  assert.equal(ids({canManage: true, isCurrent: false}, true).includes('delete'), false, 'the main profile is never deleted');
});
