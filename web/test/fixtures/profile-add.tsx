// Test-only transport fixture for the real checked source and mounted dialog.
import {useEffect, useState} from 'react';
import {createRoot} from 'react-dom/client';
import {ProfileList} from '../../src/screens/settings/account-profile/ProfileList';
import {observeProfileFailures} from '../../src/screens/settings/account-profile/failures';
import {ProfileSettingsSource} from '../../src/screens/settings/account-profile/source';
import {IconSprite} from '../../src/kit/Icon';
import '../../src/kit/tokens.css';
import '../../src/kit/fonts.css';
import '../../src/bootstrap.css';

const mode = new URLSearchParams(location.search).get('mode') ?? 'success';
let current = true, writes = 0, closes = 0, failures = 0, release: (() => void) | undefined;
const count = mode === 'limit' ? 7 : 1;
const manager = mode !== 'readonly';
const accountDocument = {
  authority: 'local', serverId: 'test-server', canManage: manager,
  capabilities: {manageAccount: manager, managePeople: manager, manageServer: manager, manageSignIn: manager},
  account: {id: 'test-account', username: 'test-owner', primaryProfileId: 'test-main', role: 'owner', revision: 1, disabled: false, allowedLibraries: []},
  profiles: Array.from({length: count}, (_, i) => ({id: i === 0 ? 'test-main' : `test-${i}`, name: `Profile ${i}`, primary: i === 0, position: i, art: 'blue', revision: 1, pinRevision: 1, pinRequired: false})),
};
const source = new ProfileSettingsSource({request: async <T>(path: string, method?: string, body?: unknown): Promise<T> => {
  if (method === 'POST') {
    writes++;await new Promise<void>(resolve => {release = resolve;});
    if (mode === 'failure') throw Object.assign(new Error('test refusal'), {status: 403});
    const input = body as {name: string; art: string};
    accountDocument.profiles.push({id: 'test-created', name: input.name, primary: false, position: accountDocument.profiles.length, art: input.art, revision: 1, pinRevision: 1, pinRequired: false});
  } else if (path !== '/v1/direct') throw new Error('Unexpected test route');
  return structuredClone(accountDocument) as T;
}}, {serverId: 'test-server', accountId: 'test-account', profileId: 'test-main'}, () => current);
await source.load();
function Fixture() {
  const [open, setOpen] = useState(true), [failure, setFailure] = useState(false);
  useEffect(() => observeProfileFailures(source, () => current, () => {failures++;closes++;setOpen(false);setFailure(true);}), []);
  return <><IconSprite />{open && <ProfileList source={source} isCurrent={() => current} onSelect={() => {}}
    onFailure={() => {failures++;closes++;setOpen(false);setFailure(true);}} />}
    <output data-ready data-failure={failure || undefined} /></>;
}
Object.assign(window, {profileTest: {
  facts: () => ({writes, closes, failures, current, count: accountDocument.profiles.length}),
  release: () => release?.(),
  retire: () => {current = false;source.dispose();},
}});
createRoot(globalThis.document.getElementById('root')!).render(<Fixture />);
