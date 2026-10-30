import {useRef, useState} from 'react';
import {createRoot} from 'react-dom/client';
import type {ReaderSurfaceInput} from '@portico/reader-core';
import '../../src/kit/tokens.css';
import '../../src/kit/fonts.css';
import '../../src/bootstrap.css';
import {IconSprite} from '../../src/kit/Icon';
import {ReaderChrome} from '../../src/kit/reader/ReaderChrome';
import {ReaderPanel} from '../../src/kit/reader/ReaderPanel';
import {ReaderDisplaySheet} from '../../src/kit/reader/ReaderDisplay';
import {ReadBookFacts, ReadBookHero, ReadBookSections} from '../../src/kit/reader/ReadBookHero';

const inputs = new Set<(input: ReaderSurfaceInput) => void>(), readers = new Set<() => void>();
const read = () => {for (const listener of readers) listener();};
const activity = {subscribe(listener: () => void) {readers.add(listener); return () => {readers.delete(listener);};}, scroll: read};
const subscribe = (listener: (input: ReaderSurfaceInput) => void) => {inputs.add(listener); return () => {inputs.delete(listener);};};
declare global {
  interface Window {
    readerAccessibility: {read(): void; input(input: ReaderSurfaceInput): void; turns: string[]};
  }
}
window.readerAccessibility = {read, input(input) {for (const listener of inputs) listener(input);}, turns: []};

/** Actual kit components with controlled data; no server, reader-engine or native-asset claim. */
function ReaderFixture() {
  const [panel, setPanel] = useState<'contents' | 'search' | 'display'>(), anchor = useRef<HTMLButtonElement>(null);
  const close = () => setPanel(undefined);
  const content = panel === 'display' ? <ReaderDisplaySheet label="Display" open onOpenChange={open => {if (!open) close();}} anchorRef={anchor}
    themes={{label: 'Page', value: 'dark', options: [{value: 'dark', label: 'Dark', tone: 'dark'}], onChange() {}}}
    fonts={{label: 'Font', value: 'publisher', options: [{value: 'publisher', label: 'Publisher’s', compactLabel: 'Publisher’s', face: 'publisher'}], onChange() {}}}
    size={{label: 'Size', sliderLabel: 'Text size', smallerLabel: 'Smaller', largerLabel: 'Larger', value: 4, min: 1, max: 7, step: 1, valueText: '4 of 7', onChange() {}}}
    rows={[]} copy={{sample: 'Aa', sizeSample: 'A', sessionNote: 'Changes here last until you close this book.', defaultsNote: 'Your defaults are in Settings, Reading.'}}/>
    : panel ? <ReaderPanel label={panel === 'contents' ? 'Contents' : 'Search'} selected={panel} tabs={[{id: panel, label: panel === 'contents' ? 'Contents' : 'Search'}]}
      onSelect={() => {}} onClose={close} returnFocusRef={anchor}>
      {panel === 'contents' ? <button type="button" onClick={close}>Chapter one</button> : <input aria-label="Search in the book"/>}
    </ReaderPanel> : undefined;
  return <div style={{width: '100vw', height: '100dvh'}}>
    <ReaderChrome title="A controlled book" copy={{back: 'Back', previous: 'Previous page', next: 'Next page', progress: 'Position', showControls: 'Show controls'}}
      direction="ltr" tapTurnEnabled pageTurnsEnabled showPageArrows={false} onBack={() => {}}
      onTurn={turn => {window.readerAccessibility.turns.push(turn); read();}} readActivity={activity} subscribeSurfaceInput={subscribe}
      actions={(['contents', 'search', 'display'] as const).map(id => ({id, label: id === 'contents' ? 'Contents' : id === 'search' ? 'Search' : 'Display', text: id,
        expanded: panel === id, onPress: (element: HTMLButtonElement) => {anchor.current = element; setPanel(panel === id ? undefined : id);}}))}
      panel={content} onPanelDismiss={close}>
      <div data-fixture-page tabIndex={-1}>The reading surface</div>
    </ReaderChrome>
  </div>;
}

function BookFixture() {
  const choice = new URLSearchParams(location.search).get('title');
  const title = choice === 'long' ? 'Little Nemo in Slumberland' : choice === 'short' ? 'Dune' : 'The Time Machine';
  return <>
    <ReadBookHero title={title} facts={[]} actions={<button type="button">Read</button>} opening={{label: 'The opening lines', text: 'The Time Traveller was expounding a recondite matter to us.'}}/>
    <ReadBookSections labels={{contents: 'Contents', details: 'Details'}} contents={<><h2>Contents</h2><button type="button">Chapter one</button></>}
      details={<ReadBookFacts heading="Details" facts={[{id: 'format', label: 'Format', value: 'EPUB'}]}/>}/>
  </>;
}
createRoot(document.getElementById('root')!).render(<><IconSprite/>{new URLSearchParams(location.search).get('surface') === 'book' ? <BookFixture/> : <ReaderFixture/>}</>);
