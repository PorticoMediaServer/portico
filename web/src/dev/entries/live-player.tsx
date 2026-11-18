import {createRoot} from 'react-dom/client';
import {defaultI18n} from '@i18n';
import styles from './live-player.module.css';
import {Icon, IconSprite} from '../../kit/Icon';
import {PlayerLive} from '../../kit/player';
import live from '../../kit/player/live.module.css';
import {livePlayerPort as port} from '../live-player-fixture';
import '../../kit/tokens.css';
import '../../kit/fonts.css';
import '../../bootstrap.css';

// The drawn live schedule exists only in this DEV specimen; no server data or artwork.
if (!import.meta.env.DEV) throw new Error('Live player specimens require the development server.');
const t = defaultI18n.t;

function Right() {
  return <>
    <button type="button" className={live.control} aria-label={t('player.mute')}><Icon name="i-vol" size={22}/></button>
    <span style={{width: 84, height: 4, borderRadius: 2, background: 'color-mix(in srgb, var(--color-text) 20%, transparent)', marginRight: 12}}><span style={{display: 'block', width: '70%', height: '100%', borderRadius: 2, background: 'var(--color-text)'}}/></span>
        <button type="button" className={live.control} aria-label={t('player.pictureInPicture')}><Icon name="i-pip" size={24}/></button>
    <button type="button" className={live.control} aria-label={t('next.states.shortcut.fullscreen')}><Icon name="i-full" size={24}/></button>
  </>;
}

createRoot(document.getElementById('root')!).render(<><IconSprite />
  <main style={{position: 'fixed', inset: 0, background: 'var(--color-ink)', color: 'var(--color-text)'}} data-live-player-specimen>
    <div aria-hidden="true" className={`portico-cover ${styles.backplate}`}/>
    <span className={styles.pictureLabel}>{t('next.live.livePicture')}</span>
    <PlayerLive port={port} playing={true} initialMiniGuideOpen
      onCollapse={() => {}} onStop={() => {}} onPlayPause={() => {}} onSkip={() => {}} onSeek={() => {}} onMore={() => {}}
      right={<Right/>} reportError={() => {}}/>
  </main></>);
