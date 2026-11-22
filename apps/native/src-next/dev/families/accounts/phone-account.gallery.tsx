import React, {useState} from 'react';
import {View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src/index';
import {nativeControlsFixtureMessages as fixture} from '../../../../../../packages/i18n/src/catalog/en-US/next/dev/controls-native-fixtures';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import type {Gallery} from '../../gallery-registry';
import {Button, FormError} from '../../../kit/controls';
import {useTheme} from '../../../kit/theme';
import {SignInPage} from '../../../pages/touch/accounts/SignInPage';
import {PhoneFindServer, type FoundServer} from '../../../screens/accounts/PhoneFindServer';
import {ServerSignInPage} from '../../../pages/touch/accounts/ServerSignInPage';
import {MoveCodePage} from '../../../pages/touch/accounts/MoveCodePage';

// The phone and tablet sign-in screens (auth.html frames 4, 5 and 6) in the states the review desk signs against:
// at rest, filled, long values, busy and error. Each case is the whole screen, so it cancels the Kit window's gutter.
const t = defaultI18n.t, noop = () => {};
const P = ['phone', 'tablet'] as const;
const T = 'next.shell.native.accountGallery' as const;

function Whole({children}: {children: React.ReactNode}) {
  const theme = useTheme();
  return <View style={{flex: 1, marginHorizontal: -theme.gutter}}>{children}</View>;
}
const wrong = () => <FormError placement="block">{presentError(Object.assign(new Error('invalid'), {code: 'password_wrong'}), 'sign-in').body}</FormError>;

function Welcome({username = '', password = '', busy = false, notice = false}: {username?: string; password?: string; busy?: boolean; notice?: boolean}) {
  const [user, setUser] = useState(username), [secret, setSecret] = useState(password);
  return <Whole><SignInPage busy={busy} passwordEnabled registration recovery username={user} password={secret} onUsernameChange={setUser} onPasswordChange={setSecret}
    error={notice ? <FormError placement="block">{t('accounts.provider.unavailable', {provider: t('accounts.provider.apple')})}</FormError> : undefined}
    onContinue={noop} onProvider={noop} onForgot={noop} onCreate={noop} onLocal={noop} onOpenLink={noop}/></Whole>;
}

const nearby = (long: boolean): readonly FoundServer[] => [
  {origin: 'https://192.168.1.20:32500', name: long ? fixture['next.controls.native.fixture.longServerName'] : 'Portico demo', host: '192.168.1.20', setupRequired: false},
  {origin: 'https://192.168.1.34:32500', name: 'New Portico server', host: '192.168.1.34', setupRequired: true},
];
function Find({found = nearby(false), address = '', searching = false, failed = false, error = false}: {
  found?: readonly FoundServer[]; address?: string; searching?: boolean; failed?: boolean; error?: boolean;
}) {
  const [value, setValue] = useState(address);
  return <Whole><PhoneFindServer busy={false} error={error ? wrong() : undefined} address={value} onAddressChange={setValue} found={found} searching={searching} failed={failed}
    localNetworkNote onContinue={noop} onChoose={noop} onSearchAgain={noop} onPortico={noop} onInvitation={noop} onBack={noop}/></Whole>;
}

/** A move client held at its code or new-sign-in step, for the frame only: it never reads, previews or redeems. */
function MoveCode({phase, error = false}: {phase: 'code' | 'local'; error?: boolean}) {
  // One snapshot for the frame's life: the store must hand back the same object until it changes.
  const [controller] = useState(() => {
    const snapshot = {phase, busy: false, username: phase === 'local' ? user : undefined, error: error ? Object.assign(new Error('invalid'), {code: 'password_wrong'}) : null};
    return {subscribe: () => noop, getSnapshot: () => snapshot, isCurrentOpening: () => true, readCodeInput: () => undefined,
      previewCode: async () => {}, redeemLocal: async () => {}, cancel: noop};
  });
  return <Whole><MoveCodePage controller={controller as never} isCurrent={() => true} onBack={noop}/></Whole>;
}

function Local({serverName = 'Portico demo', address = '192.168.1.20:32500', username = '', password = '', busy = false, error = false}: {
  serverName?: string; address?: string; username?: string; password?: string; busy?: boolean; error?: boolean;
}) {
  const [user, setUser] = useState(username), [secret, setSecret] = useState(password);
  return <Whole><ServerSignInPage busy={busy} error={error ? wrong() : undefined} passwordInvalid={error} serverName={serverName} address={address} username={user} password={secret}
    onUsernameChange={setUser} onPasswordChange={setSecret} onSignIn={noop} onBack={noop}
    links={<Button variant="link" onPress={noop}>{t('auth.forgotPassword')}</Button>}/></Whole>;
}

const user = fixture['next.controls.native.fixture.username'], secret = fixture['next.controls.native.fixture.password'];
const galleries: readonly Gallery[] = [
  {id: 'phone-account-welcome', title: T, platforms: P, screen: true, render: () => <Welcome/>},
  {id: 'phone-account-welcome-filled', title: T, platforms: P, screen: true, render: () => <Welcome username={user} password={secret}/>},
  {id: 'phone-account-welcome-long', title: T, platforms: P, screen: true, render: () => <Welcome username={fixture['next.controls.native.fixture.longUsername']} password={secret}/>},
  {id: 'phone-account-welcome-busy', title: T, platforms: P, screen: true, render: () => <Welcome busy username={user} password={secret}/>},
  {id: 'phone-account-welcome-error', title: T, platforms: P, screen: true, render: () => <Welcome notice/>},
  {id: 'phone-account-move-code', title: T, platforms: P, screen: true, render: () => <MoveCode phase="code"/>},
  {id: 'phone-account-move-code-error', title: T, platforms: P, screen: true, render: () => <MoveCode phase="code" error/>},
  {id: 'phone-account-move-local', title: T, platforms: P, screen: true, render: () => <MoveCode phase="local"/>},
  {id: 'phone-account-find', title: T, platforms: P, screen: true, render: () => <Find searching/>},
  {id: 'phone-account-find-filled', title: T, platforms: P, screen: true, render: () => <Find address="192.168.1.20:32500"/>},
  {id: 'phone-account-find-long', title: T, platforms: P, screen: true, render: () => <Find found={nearby(true)} address={fixture['next.controls.native.fixture.longAddress']}/>},
  {id: 'phone-account-find-empty', title: T, platforms: P, screen: true, render: () => <Find found={[]}/>},
  {id: 'phone-account-find-error', title: T, platforms: P, screen: true, render: () => <Find found={[]} failed error address="192.168.1.99:32500"/>},
  {id: 'phone-account-local', title: T, platforms: P, screen: true, render: () => <Local/>},
  {id: 'phone-account-local-filled', title: T, platforms: P, screen: true, render: () => <Local username={user} password={secret}/>},
  {id: 'phone-account-local-long', title: T, platforms: P, screen: true, render: () => <Local serverName={fixture['next.controls.native.fixture.longServerName']}
    address={fixture['next.controls.native.fixture.longAddress']} username={fixture['next.controls.native.fixture.longUsername']} password={secret}/>},
  {id: 'phone-account-local-busy', title: T, platforms: P, screen: true, render: () => <Local busy username={user} password={secret}/>},
  {id: 'phone-account-local-error', title: T, platforms: P, screen: true, render: () => <Local error username="nana" password={secret}/>},
];
export default galleries;
