import {createRoot} from 'react-dom/client';
import {activityR7Reference} from '../../../../packages/i18n/src/catalog/en-US/next/dev/shell-fixtures';
import {SessionCard} from '../../kit/cards/FeatureCards';
import '../../kit/tokens.css';
import '../../kit/fonts.css';
import '../../bootstrap.css';

// Direct serve-only specimens: independent of the historical Cards gallery and its evidence.
if (!import.meta.env.DEV) throw new Error('Session identity specimens require the development server.');
const {jordan, priya, sam} = activityR7Reference.streams;
const specimens = [
  {id: 'different', person: jordan, viewer: {name: jordan.name, username: jordan.username, device: jordan.device}},
  {id: 'same', person: priya, viewer: {name: ` ${priya.name.toUpperCase()} `, username: priya.username, device: priya.device}},
  // Historical Sam remains a real profile label; no username is supplied in this specimen.
  {id: 'missing', person: sam, viewer: {name: sam.name, device: sam.device}},
];
createRoot(document.getElementById('root')!).render(<main style={{width: 488, display: 'grid', gap: 16}}>
  {specimens.map(({id, person, viewer}) => <section key={id} data-identity-specimen={id}>
    <SessionCard viewer={viewer} model={{title: person.title, caption: person.subtitle}} facts={[]} onNavigate={() => dispatchEvent(new CustomEvent('session-identity:navigate', {detail: id}))} />
  </section>)}
</main>);
