import test from 'node:test';
import assert from 'node:assert/strict';
import {createShellFactsSource, forwardFacts, subscribeFacts} from '../src/shell/fact-source.ts';
import type {ShellFacts} from '../src/shell/bootstrap.ts';

const initial = (): ShellFacts => ({serverName: 'Study', preferences: {effective: {}} as ShellFacts['preferences'], identity: {
  viewer: {authority:'local', serverId:'server', accountId:'account', profileId:'profile', role:'owner'},
  profileName:'Evening', serverName:'Study', profiles:[], libraries:[{id:'library', name:'Films', kind:'movies'}],
  canManageProfiles:false, accountFacts:[], liveAvailable:false, unread:0, showNotificationDot:false,
}});
test('fact publication shares equal branches, keeps immutable originals and notifies copied contexts', () => {
  const source = createShellFactsSource(initial()), first = source.getSnapshot();
  const context = {facts:first, factsSource:source, isCurrent:() => true};
  const receipt = {authority:first.identity.viewer, target:first.identity.libraries[0]};
  const copy = forwardFacts(context, {route:'original'});
  let notices = 0; const off = subscribeFacts(copy, () => {notices++;});
  source.update({identity:{...first.identity, serverName:'Den'}, serverName:'Den'});
  assert.equal(copy.facts.serverName, 'Den'); assert.equal(notices, 1);
  assert.equal(copy.facts.identity.viewer, receipt.authority); assert.equal(copy.facts.identity.libraries, first.identity.libraries);
  assert.equal(first.serverName, 'Study'); assert.equal(first.identity.serverName, 'Study');
  assert.equal(receipt.target.name, 'Films'); assert.equal(copy.isCurrent, context.isCurrent);
  assert.ok(Object.isFrozen(first.identity.libraries[0]));
  source.update({identity:{...copy.facts.identity}}); assert.equal(notices, 1, 'equal observations do not republish');
  off(); source.dispose(); source.update({serverName:'late'}); assert.equal(copy.facts.serverName, 'Den');
});
