import {createRoot} from 'react-dom/client';
import {defaultI18n, formatMessage} from '@i18n';
import {livePlayerFixtureMessages} from '../../../../packages/i18n/src/catalog/en-US/next/dev/live-player-fixtures';
import {ColourBackdrop} from '../../kit/artwork';
import styles from './live-player.module.css';
import {Icon, IconSprite} from '../../kit/Icon';
import {LiveChrome} from '../../screens/live/LiveChrome';
import type {LiveChromePort} from '../../screens/live/live-chrome-types';
import live from '../../screens/live/live-chrome.module.css';
import '../../kit/tokens.css';
import '../../kit/fonts.css';
import '../../bootstrap.css';

// The drawn live schedule exists only in this DEV specimen; no server data or artwork.
if (!import.meta.env.DEV) throw new Error('Live player specimens require the development server.');
const fixture = (key: keyof typeof livePlayerFixtureMessages) => formatMessage(livePlayerFixtureMessages[key], {}, {locale: 'en-US'});
const t = defaultI18n.t, timezone = 'America/Halifax';
const at = (hour: number, minute: number) => Date.UTC(2026, 9, 21, hour + 3, minute);
const now = at(16, 5);
const mark = (text: string, seed: string) => ({text, seed});
const channel = (id: string, number: string, name: string, markText: string, seed: string) => ({id, sourceId: 'synthetic', kind: 'live-source' as const,
  generation: `gen-${id}`, number, name, mark: mark(markText, seed), favorite: false, recordAvailable: true, tuneAvailable: true});
const sse = channel('sse', '202', fixture('next.live.fixture.sportsExtra'), 'SSE', 'synthetic:sse');
const ss = channel('ss', '201', fixture('next.live.fixture.sports'), 'SS', 'synthetic:ss');
const lm = channel('lm', '301', fixture('next.live.fixture.movies'), 'LM', 'synthetic:lm');
const lc = channel('lc', '302', fixture('next.live.fixture.classics'), 'LC', 'synthetic:lc');
const pk = channel('pk', '401', fixture('next.live.fixture.kids'), 'PK', 'synthetic:pk');
const programme = (id: string, title: string, startMs: number, endMs: number, extra: Record<string, unknown> = {}) => ({id, title, startMs, endMs, ...extra});
const current = programme('cycling', fixture('next.live.fixture.cycling'), at(14, 0), at(17, 0), {subtitle: fixture('next.live.fixture.stage'), episode: {season: 5, number: 9}});
const rows = [
  {channel: ss, programme: programme('cycling-ss', fixture('next.live.fixture.cycling'), at(14, 0), at(18, 30)), nextStartMs: at(18, 30)},
  {channel: sse, programme: current, nextStartMs: at(17, 0)},
  {channel: lm, programme: programme('glass', fixture('next.live.fixture.glass'), at(13, 57), at(16, 11)), nextStartMs: at(16, 11)},
  {channel: lc, programme: programme('keeper', fixture('next.live.fixture.keeper'), at(15, 54), at(17, 52)), nextStartMs: at(17, 52)},
  {channel: pk, programme: programme('story', fixture('next.live.fixture.story'), at(16, 0), at(16, 30), {subtitle: fixture('next.live.fixture.homecoming'), episode: {season: 3, number: 1}}), nextStartMs: at(16, 30)},
];
const port: LiveChromePort = {fence: {} as never, channel: sse, programme: current,
  nextProgramme: {title: fixture('next.live.fixture.cycling'), startMs: at(17, 0), endMs: at(17, 30)},
  window: {mediaOriginMs: at(15, 35), programmeStartMs: at(14, 0), programmeEndMs: at(17, 0), seekStartMs: at(15, 35), seekEndMs: now, liveEdgeMs: now,
    positionMs: at(16, 3), behindSeconds: 120, atLive: false},
  down: rows[0], up: rows[2], miniGuide: rows, skipBackSeconds: 10, skipForwardSeconds: 30,
  isCurrent: () => true, record: async () => {}, goLive: async () => {}, tune: async () => {}, tuneTo: async () => {},
  openFullGuide: () => {}, openRecordings: () => {}};

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
    <ColourBackdrop fill shape="round" className={styles.backplate}/>
    <span className={styles.pictureLabel}>{t('next.live.livePicture')}</span>
    <LiveChrome port={port} playing={true} initialMiniGuideOpen
      onCollapse={() => {}} onStop={() => {}} onPlayPause={() => {}} onSkip={() => {}} onSeek={() => {}} onMore={() => {}}
      right={<Right/>} reportError={() => {}}/>
  </main></>);
