import {createRoot} from 'react-dom/client';
import family from './cards';
import {IconSprite} from '../../kit/Icon';
import styles from '../../gallery.module.css';
import '../../kit/tokens.css';
import '../../kit/fonts.css';
import '../../bootstrap.css';

// Standalone DEV fixture entrypoints use a dot so gallery discovery omits them.
if (!import.meta.env.DEV) throw new Error('Card fixtures require the development server.');
const query = new URLSearchParams(location.search);
const specimen = family.cases.find(item => item.id === query.get('case'));
if (!specimen) throw new Error('Unknown card fixture.');
const Render = specimen.render;
createRoot(document.getElementById('root')!).render(<><IconSprite /><main className={styles.capture}>
  <div data-gallery-capture className={styles.surface} style={{width: specimen.width, height: specimen.height}}><Render /></div>
</main></>);
