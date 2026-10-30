import {createRoot} from 'react-dom/client';
import family from '../families/dock';
import {IconSprite} from '../../kit/Icon';
import styles from '../../kit/dock/gallery.module.css';
import '../../kit/tokens.css';
import '../../kit/fonts.css';
import '../../bootstrap.css';

// Standalone DEV fixture: no shell, runtime adapter, authentication or second media host.
if (!import.meta.env.DEV) throw new Error('Dock fixtures require the development server.');
// This native-size specimen owns the viewport, including body-mounted menu portals.
document.documentElement.classList.add(styles.widgetDocument);
const specimen = family.cases.find(item => item.id === new URLSearchParams(location.search).get('case'));
if (!specimen) throw new Error('Unknown dock fixture.');
const Render = specimen.render;
createRoot(document.getElementById('root')!).render(<><IconSprite /><main data-gallery-ready="true" data-gallery-capture style={{width: specimen.width, height: specimen.height}}><Render /></main></>);
