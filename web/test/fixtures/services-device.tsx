// Controlled presentation of the actual device view. Values are exactly account.html
// frames 2/3/5/6, not an API/session fixture or proof of a device admission.
import {useState} from 'react';
import {createRoot} from 'react-dom/client';
import {IconSprite} from '../../src/kit/Icon';
import {DeviceDecision, DeviceView} from '../../src/services/DeviceView';
import '../../src/kit/tokens.css';
import '../../src/kit/fonts.css';
import '../../src/bootstrap.css';

function Fixture() {
  const query = new URLSearchParams(location.search);
  const fromQR = query.has('qr');
  const [code, setCode] = useState(fromQR ? '7KQM-4TXP' : '');
  const [state, setState] = useState(query.get('state'));
  return <><IconSprite />{state === 'approved' || state === 'denied' ? <DeviceDecision decision={state === 'approved' ? 'approve' : 'deny'} onAnother={() => setState('entry')} /> : <DeviceView name="Demo" code={code} fromQR={fromQR}
    preview={state === 'review' ? {deviceName: 'Living room TV', platform: 'tvos', userCode: '7KQM-4TXP'} : undefined}
    busy={false} onCodeChange={setCode} onInspect={() => setState('review')} onDecision={decision => setState(decision === 'approve' ? 'approved' : 'denied')} onAnother={() => setState('entry')} onSignOut={() => {}} />}</>;
}
createRoot(document.getElementById('root')!).render(<Fixture />);
