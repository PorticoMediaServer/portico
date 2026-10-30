// Real ResetPage form/controller. The synthetic unsubmitted token only selects its form view;
// there is no account fixture, API interception, successful reset or adopted session here.
import {createRoot} from 'react-dom/client';
import {IconSprite} from '../../src/kit/Icon';
import {EmailVerified, PasswordChanged, ResetPage} from '../../src/services/LinkPages';
import {servicesAccount} from '../../src/services/session';
import '../../src/kit/tokens.css';
import '../../src/kit/fonts.css';
import '../../src/bootstrap.css';

// The two explicit state variants exercise the same stateless views as the real
// controllers. They do not submit a reset, verify an email or adopt a session.
const state = new URLSearchParams(location.search).get('state');
createRoot(document.getElementById('root')!).render(<><IconSprite />{state === 'reset-done' ? <PasswordChanged /> : state === 'verify-elsewhere' ? <EmailVerified outcome="elsewhere" /> : <ResetPage account={servicesAccount()} token="unsubmitted-layout-test" />}</>);
