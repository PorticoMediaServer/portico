import {useRef, useState, type Dispatch, type SetStateAction} from 'react';
import {createRoot} from 'react-dom/client';
import type {ReaderSurfaceInput} from '@portico/reader-core';
import '../../src/kit/tokens.css';
import '../../src/kit/fonts.css';
import '../../src/bootstrap.css';
import {IconSprite} from '../../src/kit/Icon';
import {ReaderChrome} from '../../src/kit/reader/ReaderChrome';
import {ReaderDisplaySheet, ReaderDisplaySidePanel, type ReaderDisplayRow} from '../../src/kit/reader/ReaderDisplay';
import {loadReadFonts} from '../../src/screens/read/fonts';
import {ReaderFirstRunHint} from '../../src/screens/read/ReaderFirstRunHint';

/** Verbatim tablet-read6b controls. This isolated fixture proves no canonical
 * Reading preference, server session, real book, Settings write or full frame. */
type Probe = {changes: {key: string; value: string | number | boolean}[]; dismiss: string[]; turns: string[];
  cleanup?: {listeners: number; DOM: number; fontFaces: number}};
declare global {
  interface Window {
    readerDisplayProof: Probe;
    readerDisplayInput(input: ReaderSurfaceInput): void;
    disposeReaderDisplay(): void;
  }
}
const probe: Probe = {changes: [], dismiss: [], turns: []};
window.readerDisplayProof = probe;
const listeners = new Set<(input: ReaderSurfaceInput) => void>();
window.readerDisplayInput = input => { for (const listener of listeners) listener(input); };
const subscribe = (listener: (input: ReaderSurfaceInput) => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; };
const fonts = await loadReadFonts({document, signal: new AbortController().signal});
const phone = new URLSearchParams(location.search).get('presentation') === 'phone';
const firstHint = new URLSearchParams(location.search).get('hint') === '1';
document.documentElement.style.setProperty('--reader-publisher-font', 'Georgia, serif');
function DisplayFixture() {
  const anchor = useRef<HTMLButtonElement>(null), [open, setOpen] = useState(!firstHint), [hint, setHint] = useState(firstHint);
  const [theme, setTheme] = useState('sepia'), [font, setFont] = useState('literata'), [size, setSize] = useState(5), [twoPages, setTwoPages] = useState(true);
  const [values, setValues] = useState<Record<string, string>>({spacing: 'normal', margins: 'normal', alignment: 'justified', layout: 'pages'});
  const change = <T extends string | number | boolean,>(key: string, value: T, set: Dispatch<SetStateAction<T>>) => { probe.changes.push({key, value}); set(value); };
  const dismiss = (value: boolean, reason: string) => { probe.dismiss.push(reason); setOpen(value); };
  const options = [
    {id: 'spacing', label: 'Line spacing', options: [{value: 'tight', label: 'Tight'}, {value: 'normal', label: 'Normal'}, {value: 'loose', label: 'Loose'}]},
    {id: 'margins', label: 'Margins', options: [{value: 'narrow', label: 'Narrow'}, {value: 'normal', label: 'Normal'}, {value: 'wide', label: 'Wide'}]},
    {id: 'alignment', label: 'Alignment', options: [{value: 'left', label: 'Left'}, {value: 'justified', label: 'Justified'}]},
    {id: 'layout', label: 'Layout', options: [{value: 'pages', label: 'Pages', icon: 'i-page'}, {value: 'scrolling', label: 'Scrolling', icon: 'i-vscroll'}]},
  ] as const;
  const rows: ReaderDisplayRow[] = options.map(row => ({...row, value: values[row.id], onChange: value => {
    probe.changes.push({key: row.id, value}); setValues(old => ({...old, [row.id]: value}));
  }}));
  const Panel = phone ? ReaderDisplaySheet : ReaderDisplaySidePanel;
  const panel = <Panel label="Display" open={open} onOpenChange={dismiss} anchorRef={anchor}
    themes={{label: 'Page', value: theme, options: [{value: 'dark', label: 'Dark', tone: 'dark'}, {value: 'black', label: 'Black', tone: 'black'}, {value: 'sepia', label: 'Sepia', tone: 'sepia'}, {value: 'light', label: 'Light', tone: 'light'}], onChange: value => change('theme', value, setTheme)}}
    fonts={{label: 'Font', value: font, options: [{value: 'publisher', label: 'Publisher’s', compactLabel: 'Publisher’s', face: 'publisher'}, {value: 'literata', label: 'Literata', compactLabel: 'Literata', face: 'literata'}, {value: 'manrope', label: 'Manrope', compactLabel: 'Manrope', face: 'manrope'}], onChange: value => change('font', value, setFont)}}
    size={{label: 'Size', sliderLabel: 'Text size', smallerLabel: 'Smaller', largerLabel: 'Larger', value: size, min: 1, max: 7, step: 1, valueText: `${size} of 7`, summary: phone ? `${size} of 7` : undefined, ticks: phone ? [1, 2, 3, 4, 5, 6, 7] : undefined, onChange: value => change('size', value, setSize)}} rows={rows}
    twoPages={phone ? undefined : {label: 'Two pages side by side in landscape', description: 'Held upright, a book always shows one page.', checked: twoPages, onChange: value => change('twoPages', value, setTwoPages)}}
    copy={{sample: 'Aa', sizeSample: 'A', sessionNote: 'Changes here last until you close this book.', defaultsNote: 'Your defaults are in Settings, Reading.', openSettings: phone ? undefined : 'Open Settings'}} onOpenSettings={phone ? undefined : () => probe.changes.push({key: 'settings', value: true})} />;
  return <div style={{width: '100vw', height: '100dvh', position: 'relative'}}>
    <ReaderChrome copy={{back: 'Back', previous: 'Previous page', next: 'Next page', progress: 'Position in book', showControls: 'Show controls'}} direction="ltr" tapTurnEnabled pageTurnsEnabled
      onBack={() => {}} onTurn={turn => probe.turns.push(turn)} actions={[]} showPageArrows={false} panel={open ? panel : null} panelReflows={open && !phone}
      onPanelDismiss={reason => dismiss(false, reason)} subscribeSurfaceInput={subscribe} initiallyVisible hint={hint ? <ReaderFirstRunHint onDone={() => setHint(false)}/> : undefined}>
      <div style={{height: '100%', background: '#f1e6d1'}} />
    </ReaderChrome>
    <button ref={anchor} aria-label="Display: theme, font, size and layout" onClick={() => setOpen(value => !value)}
      style={{position: 'absolute', right: 16, top: 44, width: 48, height: 48, background: 'transparent', border: 0, color: 'white'}}>Aa</button>
  </div>;
}
const root = createRoot(document.getElementById('root')!);
root.render(<><IconSprite /><DisplayFixture /></>);
window.disposeReaderDisplay = () => {
  root.unmount(); fonts.dispose(); document.documentElement.style.removeProperty('--reader-publisher-font'); probe.cleanup = {listeners: listeners.size, DOM: document.getElementById('root')!.childNodes.length,
    fontFaces: [...document.fonts].filter(face => face.family === 'Literata').length};
};
