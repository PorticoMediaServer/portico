// Isolated real-kit verification, with only the approved connect26/31 initial shown in the frame.
// No account session, server bootstrap, invitation response or data-feed fixture is installed.
import {useState} from 'react';
import {createRoot} from 'react-dom/client';
import {IconSprite} from '../../src/kit/Icon';
import {ProfileAvatar} from '../../src/kit/shell/AccountSurfaces';
import {ServicesHeader, ServicesStart} from '../../src/kit/accounts/ServicesWorkspace';
import {distributionPage} from '../../src/services/distribution';
import '../../src/kit/tokens.css';
import '../../src/kit/fonts.css';
import '../../src/bootstrap.css';

function Fixture() {
  const [invitation, setInvitation] = useState('');
  const download = () => location.assign(distributionPage);
  return <><IconSprite /><ServicesStart header={<ServicesHeader notifications={false} avatar={<ProfileAvatar initial="N" name="N" onClick={() => {}} />} />}
    invitation={invitation} onInvitationChange={setInvitation} onJoin={() => { window.dispatchEvent(new CustomEvent('services-test-join', {detail: invitation})); }} onPaste={() => { void navigator.clipboard.readText().then(setInvitation); }}
    onDownload={download} onApps={download} onCopyDownload={() => { void navigator.clipboard.writeText(distributionPage); }} onShareDownload={() => { if (navigator.share) void navigator.share({url: distributionPage}); else void navigator.clipboard.writeText(distributionPage); }} /></>;
}
createRoot(document.getElementById('root')!).render(<Fixture />);
