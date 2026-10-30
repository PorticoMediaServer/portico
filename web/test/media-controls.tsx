import {createRoot} from 'react-dom/client';
import {IconSprite} from '../src/kit/Icon';
import {DeviceEqualizer} from '../src/kit/media/Equalizer';
import {BrowserSettingsEqualizer} from '../src/kit/media/BrowserSettingsEqualizer';
import {createBrowserEqualizerPort} from '../src/kit/media/equalizer-port';
import {flatEqualizer} from '../src/platform/playback/audio/equalizer';
import {saveDeviceEqualizer, loadDeviceEqualizer} from '../src/platform/playback/audio/equalizer-store';
import '../src/kit/tokens.css';
import '../src/kit/fonts.css';
import '../src/bootstrap.css';

if (!import.meta.env.DEV) throw new Error('Media control verification requires the development server.');
const query = new URLSearchParams(location.search), layout = query.get('layout') ?? 'settings';
if (!['settings','panel','phone','tv'].includes(layout)) throw new Error('Unknown equalizer layout.');
const settings = query.get('state') === 'flat' ? {...flatEqualizer,enabled:true} : {enabled:query.get('state') !== 'off',preampDb:-6,bandsDb:[5.5,3,-.5,-1.5,.5,-1,2.5,-2,1.5,-3.5]};
if (!query.has('persist')) await saveDeviceEqualizer(settings);
const context = new AudioContext();
const errors: string[] = [], port = createBrowserEqualizerPort(error => errors.push(String(error)));
Object.assign(globalThis,{mediaControlProof:{port,read:loadDeviceEqualizer,errors,sampleRate:context.sampleRate}});
addEventListener('pagehide',()=>{void context.close();},{once:true});
const width = layout === 'phone' ? 390 : layout === 'panel' ? 448 : layout === 'tv' ? 1728 : 1000;
document.body.style.margin='0';
createRoot(document.getElementById('root')!).render(<><IconSprite /><main style={{width,padding:layout === 'phone' || layout === 'panel' ? 0 : 24,boxSizing:'border-box'}}>
  {query.has('bound') ? <BrowserSettingsEqualizer layout="settings" onError={error=>errors.push(String(error))} /> : <DeviceEqualizer port={port} layout={layout as 'settings'|'panel'|'phone'|'tv'} sampleRate={context.sampleRate} reportError={error=>errors.push(String(error))} />}
</main></>);
