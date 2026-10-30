import {createRoot} from 'react-dom/client';
import {activityR7Cases} from './ActivityReference';
import {IconSprite} from '../kit/Icon';
import styles from '../gallery.module.css';
import activityStyles from '../kit/shell/activity-gallery.module.css';
import '../kit/tokens.css';
import '../kit/fonts.css';
import '../bootstrap.css';

/** Isolated DEV entry: the same Activity gallery cases, without importing unrelated families. */
if (!import.meta.env.DEV) throw new Error('Activity fixtures require the development server.');
const specimen = activityR7Cases.find(item => item.id === new URLSearchParams(location.search).get('case'));
if (!specimen) throw new Error('Unknown Activity fixture.');
const Render = specimen.render;
createRoot(document.getElementById('root')!).render(<><IconSprite /><main className={styles.capture} data-gallery-ready="true"><div className={activityStyles.referenceSurface} data-gallery-capture style={{width: specimen.width, height: specimen.height}}><Render /></div></main></>);
