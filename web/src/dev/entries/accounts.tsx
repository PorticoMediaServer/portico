import {createRoot} from 'react-dom/client';
import {useState} from 'react';
import {IconSprite} from '../../kit/Icon';
import {LocalServerSignIn, PorticoServerSignIn, ServerProfiles, ServerProfilePIN} from '../../kit/accounts';
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
  if (frame === '4b') return <PorticoServerSignIn serverName="Portico demo" onContinue={() => record('portico')} onRecovery={() => record('recovery')} onOldPassword={() => record('old-password')} />;
  return <LocalServerSignIn serverName="Portico demo" initialUsername="nana_r" remembered={remembered}
    onSignIn={(username, password) => record('sign-in', {username, password})} onForgotPassword={() => record('forgot-password')} onJoin={() => record('join')} />;
}
createRoot(document.getElementById('root')!).render(<><IconSprite /><main data-account-specimen><Specimen /></main></>);
