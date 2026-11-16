import React, {useState} from 'react';
import {View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src/index';
import {nativeControlsFixtureMessages as fixture} from '../../../../../../packages/i18n/src/catalog/en-US/next/dev/controls-native-fixtures';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import type {Gallery} from '../../gallery-registry';
import {FormError} from '../../../kit/controls/FormError';
import {Button} from '../../../kit/controls';
import {useTheme} from '../../../kit/theme';
import {TVCredentialForm, TVFrontDoor} from '../../../tv/auth';
import {AccountEntry} from '../../../screens/accounts/AccountEntry';
import {ServersRoute} from '../../../screens/accounts/AccountRoutes';
import {TVServerChooser} from '../../../screens/accounts/TVServerChooser';

// The five TV account screens, each in the states the review desk signs against: at rest, focused,
// empty, filled, long value, busy and error. Each case is one whole screen (the account frame owns the
// window), so it cancels the Kit window's gutter. Focus is wherever the screen's own entry rule puts it.
const t = defaultI18n.t, noop = () => {};

function Whole({children}: {children: React.ReactNode}) {
  const theme = useTheme();
  return <View style={{flex: 1, marginHorizontal: -theme.gutter}}>{children}</View>;
}

function Credentials({kind, username = '', password = '', busy = false, error = false, serverName = fixture['next.controls.native.fixture.serverName']}: {
  kind: 'local' | 'portico'; username?: string; password?: string; busy?: boolean; error?: boolean; serverName?: string;
}) {
  const [user, setUser] = useState(username), [secret, setSecret] = useState(password);
  const shown = error ? <FormError placement="block">{presentError(Object.assign(new Error('invalid'), {code: 'password_wrong'}), 'sign-in').body}</FormError> : undefined;
  const common = {username: user, password: secret, onUsernameChange: setUser, onPasswordChange: setSecret, busy, error: shown, onSubmit: noop, onBack: noop};
  return <Whole>{kind === 'local'
    ? <TVCredentialForm {...common} kind="local" serverName={serverName}/>
    : <TVCredentialForm {...common} kind="portico" usernamePlaceholder="auth.usernameOrEmail"/>}</Whole>;
}

const row = (id: string, name: string, signIn: 'local' | 'portico', signedIn: boolean, address = 'livingroom.local:32400') =>
  ({id, name, signIn, signedIn, familyId: `family-${id}`, fingerprint: `fp-${id}`, address, sharesName: false,
    labelKey: signIn === 'portico' ? 'accounts.session.portico' : 'accounts.session.local'});
type Snapshot = NonNullable<React.ComponentProps<typeof TVServerChooser>['snapshot']>;
const servers = (long: boolean) => ({selectedServerId: 'a', servers: [
  row('a', fixture['next.controls.native.fixture.serverName'], 'portico', true),
  row('b', long ? fixture['next.controls.native.fixture.longServerName'] : 'Cabin', 'local', true),
  row('c', 'Studio', 'local', false),
]}) as unknown as Snapshot;

function Chooser({long = false, busy = false}: {long?: boolean; busy?: boolean}) {
  return <Whole><TVServerChooser snapshot={servers(long)} busy={busy} owner onSelect={noop} onSignOut={noop} onForget={noop} onRefresh={noop} onAdd={noop}
    footer={<View style={{alignSelf: 'stretch', gap: 8, marginTop: 10}}>
      <Button variant="link" disabled={busy} onPress={noop}>{t('settings.linkTV')}</Button>
      <Button variant="link" disabled={busy} onPress={noop}>{t('accounts.signOut.portico')}</Button>
    </View>}/></Whole>;
}


const T = 'next.shell.native.accountGallery' as const;
const galleries: readonly Gallery[] = [
  {id: 'tv-account-welcome', title: T, platforms: ['tv'], render: () => <Whole><TVFrontDoor busy={false} onPortico={noop} onLocal={noop}/></Whole>},
  {id: 'tv-account-welcome-busy', title: T, platforms: ['tv'], render: () => <Whole><TVFrontDoor busy onPortico={noop} onLocal={noop}/></Whole>},
  {id: 'tv-account-address-empty', title: T, platforms: ['tv'], render: () => <Whole><AccountEntry initialStage="address" onComplete={noop} onBack={noop}/></Whole>},
  {id: 'tv-account-address-filled', title: T, platforms: ['tv'], render: () => <Whole><AccountEntry initialStage="address" initialAddress="https://demo.getportico.tv" onComplete={noop} onBack={noop}/></Whole>},
  {id: 'tv-account-address-long', title: T, platforms: ['tv'], render: () => <Whole><AccountEntry initialStage="address" initialAddress={fixture['next.controls.native.fixture.longAddress']} onComplete={noop} onBack={noop}/></Whole>},
  {id: 'tv-account-sign-in-empty', title: T, platforms: ['tv'], render: () => <Credentials kind="local"/>},
  {id: 'tv-account-sign-in-username', title: T, platforms: ['tv'], render: () => <Credentials kind="local" username={fixture['next.controls.native.fixture.username']}/>},
  {id: 'tv-account-sign-in-filled', title: T, platforms: ['tv'], render: () => <Credentials kind="local" username={fixture['next.controls.native.fixture.username']} password={fixture['next.controls.native.fixture.password']}/>},
  {id: 'tv-account-sign-in-long', title: T, platforms: ['tv'], render: () => <Credentials kind="local" serverName={fixture['next.controls.native.fixture.longServerName']} username={fixture['next.controls.native.fixture.longUsername']}/>},
  {id: 'tv-account-sign-in-busy', title: T, platforms: ['tv'], render: () => <Credentials kind="local" busy username={fixture['next.controls.native.fixture.username']} password={fixture['next.controls.native.fixture.password']}/>},
  {id: 'tv-account-sign-in-error', title: T, platforms: ['tv'], render: () => <Credentials kind="local" error username={fixture['next.controls.native.fixture.username']}/>},
  {id: 'tv-account-portico-empty', title: T, platforms: ['tv'], render: () => <Credentials kind="portico"/>},
  {id: 'tv-account-servers', title: T, platforms: ['tv'], render: () => <Chooser/>},
  {id: 'tv-account-servers-long', title: T, platforms: ['tv'], render: () => <Chooser long/>},
  {id: 'tv-account-servers-busy', title: T, platforms: ['tv'], render: () => <Chooser busy/>},
  {id: 'tv-account-link', title: T, platforms: ['tv'], render: () => <Whole><ServersRoute initialPanel="consent-kind" onBack={noop}/></Whole>},
];
export default galleries;
