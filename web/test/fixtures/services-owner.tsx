import {useState} from 'react';
import {createRoot} from 'react-dom/client';
import {OwnerApprovalReview} from '../../src/services/OwnerApprovalPage';
import {IconSprite} from '../../src/kit/Icon';
import {servicesI18n} from '../../src/services/i18n';
import '../../src/kit/tokens.css';
import '../../src/kit/fonts.css';
import '../../src/bootstrap.css';

// Drawn auth24e2 facts are a DEV-only component specimen. No account, pending
// journal, provider, API, Server grant or real owner decision is installed.
if (!import.meta.env.DEV) throw new Error('Owner specimens require development.');
function Specimen() {
  const {t} = servicesI18n(), [approved, setApproved] = useState(false);
  const action = (value: string) => {document.documentElement.dataset.ownerAction = value;};
  const count = new URLSearchParams(location.search).has('without-count') ? undefined : 86;
  return <><IconSprite /><OwnerApprovalReview display={{name: 'Portico demo', address: 'http://192.168.1.20:32500', accountName: 'Demo',
    requestedAt: '2030-01-02T03:04:05Z', expiresAt: '2030-01-02T03:05:05Z', ...(count === undefined ? {} : {otherPeopleCount: count})}}
    requested={t('services.claim.justNow')} approved={approved}
    onApprove={() => {action('approve'); setApproved(true);}} onDeny={() => action('deny')} onReturn={() => action('return')} /></>;
}
createRoot(document.getElementById('root')!).render(<Specimen />);
