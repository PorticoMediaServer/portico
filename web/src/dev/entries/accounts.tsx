import {createRoot} from 'react-dom/client';
import {useState} from 'react';
import {IconSprite} from '../../kit/Icon';
import {ProfilePINRecovery} from '../../kit/accounts/ProfilePINRecovery';
import {AuthStage, LocalServerSignIn, PorticoServerSignIn, ServerIdentityFailure, ServerProfiles, ServerProfilePIN} from '../../kit/accounts';
import {MoveNotPortico} from '../../screens/accounts/ChooserMove';
import {ServicesActions, ServicesAuthStage, ServicesHeading, ServicesLead, ServicesSignedInAccount} from '../../kit/accounts/ServicesAuth';
import {ServerReturnCard} from '../../services/AccountServerReturn';
import {Button} from '../../kit/controls/Button';
import {defaultI18n} from '@i18n';
import '../../kit/tokens.css';
import '../../kit/fonts.css';
import '../../bootstrap.css';

// Drawn demo facts exist only in this DEV specimen, never in runtime adapters.
if (!import.meta.env.DEV) throw new Error('Account specimens require development.');
const profiles = [
  {id: 'demo', name: 'Demo', eligible: true, primary: true, art: 'blue'},
  {id: 'alex', name: 'Alex', eligible: true, pinRequired: true, art: 'violet'},
  {id: 'kids', name: 'Kids', eligible: true, art: 'mint'},
  {id: 'sam', name: 'Sam', eligible: false, unavailableReason: 'server_restriction' as const, art: 'coral'},
];
const remembered = [
  {accountId: 'nana', username: 'nana_r', displayName: 'Nana', avatarVersion: 0, automaticSignIn: false, lastUsed: ''},
  {accountId: 'theo', username: 'theo', displayName: 'Theo', avatarVersion: 0, automaticSignIn: false, lastUsed: ''},
];
function Specimen() {
  const frame = new URLSearchParams(location.search).get('frame') ?? '4';
  const [remember, setRemember] = useState(false);
  const record = (kind: string, value?: unknown) => {document.documentElement.dataset.accountAction = JSON.stringify({kind, value});};
  if (frame === '13') return <ServerProfiles serverName="Portico demo" profiles={profiles} onSelect={value => record('profile', value.id)} onSwitchServer={() => record('servers')} autoFocusId="alex" />;
  if (frame === '14') return <ServerProfilePIN serverName="Portico demo" profile={profiles[1]} remember={remember} onRememberChange={setRemember} error
    onComplete={value => record('pin', value)} onOtherProfiles={() => record('profiles')} onForgotPIN={() => record('forgot-pin')} onSwitchServer={() => record('servers')} />;
  if (frame === 'identity') return <AuthStage layout="state" legal={false}><ServerIdentityFailure serverName="Portico demo" failure="local-changed"
    onRetry={() => record('retry')} onChooseServer={() => record('servers')} onForget={async () => record('forget')} /></AuthStage>;
  if (frame === 'welcome') return <ServicesAuthStage><ServicesHeading singleLine>{defaultI18n.t('auth.welcomeHome')}</ServicesHeading><ServicesLead>{defaultI18n.t('web.signin.accountLede')}</ServicesLead>
    <ServicesSignedInAccount username="nana_r" onDifferent={() => record('different')} />
    <ServicesActions><Button variant="primary" onClick={() => record('continue')}>{defaultI18n.t('action.continue')}</Button></ServicesActions></ServicesAuthStage>;
  if (frame === 'move-not-portico') return <MoveNotPortico server="Portico demo" onBack={() => record('back')} />;
  if (frame === '4b-http') return <PorticoServerSignIn serverName="Portico demo" plainHttp onContinue={() => record('hosted')} onRecovery={() => record('recovery')} />;
  if (frame === '4b-http-secure') return <PorticoServerSignIn serverName={new URLSearchParams(location.search).get('server') ?? 'Portico demo'} plainHttp onSecure={() => record('secure')} onContinue={() => record('hosted')} onRecovery={() => record('recovery')} />;
  if (frame === '5-return') return <ServerReturnCard server="Portico demo" address="66-94-102-39.ptc-example.portico.direct:32510" username="nana_r"
    onContinue={() => record('continue')} onCancel={() => record('cancel')} onNotYou={() => record('not-you')} />;
  // Auth18 on a Portico Accounts server: the Portico Account's password, checked by Portico Services.
  const portico = {
    '18-portico': {phase: 'ready', account: {username: 'demo', hasPassword: true, twoStep: false}},
    '18-portico-two-step': {phase: 'ready', account: {username: 'demo', hasPassword: true, twoStep: true}},
    '18-portico-no-password': {phase: 'ready', account: {username: 'demo', hasPassword: false, twoStep: false}},
    '18-portico-elsewhere': {phase: 'elsewhere'},
  } as const;
  if (frame in portico) return <ProfilePINRecovery profileName="Alex" username="demo" phase="portico" portico={portico[frame as keyof typeof portico]}
    onPassword={() => record('password')} onPorticoPassword={(password, code) => record('portico-password', {code})} onPorticoRetry={() => record('retry')}
    onPorticoElsewhere={() => record('elsewhere')} onSetPassword={() => record('set-password')} onCode={() => record('code')} onPIN={() => record('pin')}
    onClose={() => record('close')} onOpen={() => record('open')} />;
  if (frame === '4b') return <PorticoServerSignIn serverName="Portico demo" onContinue={() => record('portico')} onRecovery={() => record('recovery')} onMoveCode={() => record('move-code')} />;
  return <LocalServerSignIn serverName="Portico demo" initialUsername="nana_r" remembered={remembered}
    onSignIn={(username, password) => record('sign-in', {username, password})} onForgotPassword={() => record('forgot-password')} onJoin={() => record('join')} />;
}
createRoot(document.getElementById('root')!).render(<><IconSprite /><main data-account-specimen><Specimen /></main></>);
