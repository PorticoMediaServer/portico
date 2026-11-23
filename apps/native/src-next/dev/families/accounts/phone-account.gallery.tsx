import React, {useState} from 'react';
import {View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src/index';
import {nativeControlsFixtureMessages as fixture} from '../../../../../../packages/i18n/src/catalog/en-US/next/dev/controls-native-fixtures';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import type {Gallery} from '../../gallery-registry';
import {Button, FormError} from '../../../kit/controls';
import {useTheme} from '../../../kit/theme';
import {SignInPage} from '../../../pages/touch/accounts/SignInPage';
import {PhoneFindServer, type FoundServer} from '../../../kit/accounts/PhoneFindServer';
import {ServerSignInPage} from '../../../pages/touch/accounts/ServerSignInPage';
import {NoServersPage} from '../../../pages/touch/accounts/NoServersPage';
import {MovePasswordPage} from '../../../pages/touch/accounts/MovePasswordPage';
import {PhoneAccountMoveNotice, type PhoneAccountMoveNoticeState} from '../../../kit/accounts/PhoneAccountMoveNotice';
import {MoveCodePage} from '../../../pages/touch/accounts/MoveCodePage';
import {ServerIdentityFailureView} from '../../../kit/accounts/ServerIdentityFailureView';
import {NativeIdentityFailure} from '../../../platform/identity-failure';

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

/** A move client held at its password step, for the frame only: it never proves anything. */
function MovePassword({busy = false, error = false}: {busy?: boolean; error?: boolean}) {
  // One snapshot for the frame's life: the store must hand back the same object until it changes.
  const [controller] = useState(() => {
    const snapshot = {phase: 'password', busy, error: error ? Object.assign(new Error('invalid'), {code: 'password_wrong'}) : null};
    return {subscribe: () => noop, getSnapshot: () => snapshot, proveOldPassword: async () => {}, cancel: noop};
  });
  return <Whole><MovePasswordPage controller={controller as never} serverName="Portico demo" onBack={noop}/></Whole>;
}

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
    const snapshot = {phase, busy: false, username: phase === 'local' ? user : undefined, error: error ? Object.assign(new Error('unavailable'), {code: 'unavailable'}) : null};
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

function Move({state, busy = false}: {state: PhoneAccountMoveNoticeState; busy?: boolean}) {
  return <Whole><PhoneAccountMoveNotice state={state} busy={busy}/></Whole>;
}
const moveServer = 'Portico demo', longServer = fixture['next.controls.native.fixture.longServerName'];

const user = fixture['next.controls.native.fixture.username'], secret = fixture['next.controls.native.fixture.password'];
// The server-identity page (#5279): a saved server answering as someone else, or its certificate failing. A real mismatch
// can't be made on the demo, so these cases are the way to look at the page on a device.
function IdentityFailure({code, signIn = 'local', confirm = false}: {code: string; signIn?: 'local' | 'portico'; confirm?: boolean}) {
  const [open, setOpen] = useState(confirm), cancel = React.useRef<View>(null), opener = React.useRef<View>(null);
  const failure = new NativeIdentityFailure(code, {serverId: 'srv_fixture', familyId: 'family', fingerprint: 'fingerprint', name: 'Portico Demo', signIn} as never);
  return <Whole><ServerIdentityFailureView failure={failure} busy={false} confirm={open} onConfirmChange={setOpen} cancelRef={cancel} openerRef={opener}
    onRetry={noop} onChoose={noop} onForget={noop}/></Whole>;
}

const galleries: readonly Gallery[] = [
  {id: 'phone-account-welcome', title: T, platforms: P, screen: true, render: () => <Welcome/>},
  {id: 'phone-account-welcome-filled', title: T, platforms: P, screen: true, render: () => <Welcome username={user} password={secret}/>},
  {id: 'phone-account-welcome-long', title: T, platforms: P, screen: true, render: () => <Welcome username={fixture['next.controls.native.fixture.longUsername']} password={secret}/>},
  {id: 'phone-account-welcome-busy', title: T, platforms: P, screen: true, render: () => <Welcome busy username={user} password={secret}/>},
  {id: 'phone-account-welcome-error', title: T, platforms: P, screen: true, render: () => <Welcome notice/>},
  {id: 'phone-account-no-servers', title: T, platforms: P, screen: true, render: () => <Whole><NoServersPage busy={false} username={user} onSettings={noop} onDownloads={noop}
    onFind={noop} onRefresh={noop} onShare={noop} onSignOut={noop} onJoin={noop}/></Whole>},
  {id: 'phone-account-move-code', title: T, platforms: P, screen: true, render: () => <MoveCode phase="code"/>},
  {id: 'phone-account-move-code-error', title: T, platforms: P, screen: true, render: () => <MoveCode phase="code" error/>},
  {id: 'phone-account-move-local', title: T, platforms: P, screen: true, render: () => <MoveCode phase="local"/>},
  {id: 'phone-account-find', title: T, platforms: P, screen: true, render: () => <Find searching/>},
  {id: 'phone-account-find-filled', title: T, platforms: P, screen: true, render: () => <Find address="192.168.1.20:32500"/>},
  {id: 'phone-account-find-long', title: T, platforms: P, screen: true, render: () => <Find found={nearby(true)} address={fixture['next.controls.native.fixture.longAddress']}/>},
  {id: 'phone-account-find-empty', title: T, platforms: P, screen: true, render: () => <Find found={[]}/>},
  {id: 'phone-account-find-error', title: T, platforms: P, screen: true, render: () => <Find found={[]} failed error address="192.168.1.99:32500"/>},
  {id: 'phone-account-move-password', title: T, platforms: P, screen: true, render: () => <MovePassword/>},
  {id: 'phone-account-move-password-busy', title: T, platforms: P, screen: true, render: () => <MovePassword busy/>},
  {id: 'phone-account-move-password-error', title: T, platforms: P, screen: true, render: () => <MovePassword error/>},
  {id: 'phone-account-local', title: T, platforms: P, screen: true, render: () => <Local/>},
  {id: 'phone-account-local-filled', title: T, platforms: P, screen: true, render: () => <Local username={user} password={secret}/>},
  {id: 'phone-account-local-long', title: T, platforms: P, screen: true, render: () => <Local serverName={fixture['next.controls.native.fixture.longServerName']}
    address={fixture['next.controls.native.fixture.longAddress']} username={fixture['next.controls.native.fixture.longUsername']} password={secret}/>},
  {id: 'phone-account-local-busy', title: T, platforms: P, screen: true, render: () => <Local busy username={user} password={secret}/>},
  {id: 'phone-account-local-error', title: T, platforms: P, screen: true, render: () => <Local error username="nana" password={secret}/>},
  {id: 'phone-account-move-linked', title: T, platforms: P, screen: true, render: () => <Move state={{kind: 'linked', onChooseProfile: noop}}/>},
  {id: 'phone-account-move-to-portico', title: T, platforms: P, screen: true, render: () => <Move state={{kind: 'to-portico', serverName: moveServer, onContinue: noop, onCreateAccount: noop}}/>},
  {id: 'phone-account-move-to-local', title: T, platforms: P, screen: true, render: () => <Move state={{kind: 'to-local', serverName: moveServer, onEnterCode: noop, onChooseServer: noop}}/>},
  {id: 'phone-account-move-collision', title: T, platforms: P, screen: true, render: () => <Move state={{kind: 'collision', details: {serverName: moveServer, existingUsername: user}, onDifferentAccount: noop, onBack: noop}}/>},
  {id: 'phone-account-move-confirm-long', title: T, platforms: P, screen: true, render: () => <Move state={{kind: 'confirm', serverName: longServer,
    username: fixture['next.controls.native.fixture.longUsername'], account: user, onMove: noop, onNotYou: noop, onCancel: noop}}/>},
  {id: 'phone-account-move-busy', title: T, platforms: P, screen: true, render: () => <Move busy state={{kind: 'to-portico', serverName: moveServer, onContinue: noop, onCreateAccount: noop}}/>},
  {id: 'phone-account-identity-changed', title: T, platforms: P, screen: true, render: () => <IdentityFailure code="identity_mismatch"/>},
  {id: 'phone-account-identity-changed-forget', title: T, platforms: P, screen: true, render: () => <IdentityFailure code="identity_mismatch" confirm/>},
  {id: 'phone-account-identity-portico', title: T, platforms: P, screen: true, render: () => <IdentityFailure code="identity_mismatch" signIn="portico"/>},
  {id: 'phone-account-identity-certificate', title: T, platforms: P, screen: true, render: () => <IdentityFailure code="certificate_untrusted"/>},
];
export default galleries;
