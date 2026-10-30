import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeShellNavigation, type NativeShellRoute} from '../src-next/shell/routes';
import type {ShellAuthority} from '../src-next/shell/model';
import {readRouteBack} from '../src-next/screens/read/read-route-back';

const authority: ShellAuthority = {serverId: 's', accountId: 'a', profileId: 'p', epoch: 'e', viewerFence: 'v'};
const target: Extract<NativeShellRoute, {kind: 'read'}> = {kind: 'read', itemId: 'book', sourceId: 'original'};

test('one current Reader Back spends its original render once', () => {
  const navigation = new NativeShellNavigation(authority, () => authority);
  try {
    navigation.open({kind: 'notifications'}, authority); navigation.open(target, authority);
    const expected = navigation.getSnapshot(), back = readRouteBack(navigation, expected, target, () => true);
    back(); const after = navigation.getSnapshot();
    assert.equal(after.route.kind, 'notifications');
    back(); assert.strictEqual(navigation.getSnapshot(), after);
  } finally {navigation.dispose();}
});

test('inactive, retired, replaced and same-object reopened readers cannot pop a successor', () => {
  for (const mode of ['inactive', 'retired', 'replaced', 'reopened', 'authority'] as const) {
    let live = true, currentAuthority = authority;
    const navigation = new NativeShellNavigation(authority, () => currentAuthority);
    try {
      navigation.open(target, authority);
      const expected = navigation.getSnapshot();
      const inactive = {...target};
      const back = readRouteBack(navigation, expected, mode === 'inactive' ? inactive : target, () => live);
      if (mode === 'retired') live = false;
      if (mode === 'replaced') navigation.open({kind: 'licenses'}, authority);
      if (mode === 'reopened') {navigation.back(authority); navigation.open(target, authority);}
      if (mode === 'authority') currentAuthority = {...authority, viewerFence: 'replacement'};
      const shown = navigation.getSnapshot(); back();
      assert.strictEqual(navigation.getSnapshot(), shown, mode);
    } finally {navigation.dispose();}
  }
});

test('retirement refuses before access and reentrant currentness cannot spend twice or adopt a route', () => {
  const navigation = new NativeShellNavigation(authority, () => authority);
  try {
    navigation.open(target, authority); const expected = navigation.getSnapshot();
    let reads = 0;
    readRouteBack({getSnapshot: () => {reads++; throw new Error('retired accessor');}, back: navigation.back.bind(navigation)}, expected, target, () => false)();
    assert.equal(reads, 0);
    let back!: () => void;
    back = readRouteBack(navigation, expected, target, () => {back(); return true;});
    back(); assert.equal(navigation.getSnapshot().route.kind, 'shell');
    navigation.open(target, authority);
    const original = navigation.getSnapshot(); let checks = 0;
    const late = readRouteBack(navigation, original, target, () => {
      if (++checks === 2) navigation.open({kind: 'notifications'}, authority);
      return true;
    });
    late(); assert.equal(navigation.getSnapshot().route.kind, 'notifications');
  } finally {navigation.dispose();}
});
