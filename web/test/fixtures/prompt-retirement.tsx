import {useLayoutEffect, useMemo, useRef, useState} from 'react';
import {createRoot} from 'react-dom/client';
import {WatchPromptFocus, WatchPromptRetirement, useWatchPromptControls, type PromptFocusHost} from '../../src/screens/watch/prompt-focus';

type Observation = Readonly<{target: 'seek' | 'region'; promptInert: boolean; promptPhase: string | undefined}>;
type Harness = Readonly<{
  withdraw: () => void; replaceOffer: () => void; replaceRoot: () => void;
  removeSeekAndWithdraw: () => void; blockAndWithdraw: () => void; retireAndWithdraw: () => void;
  observations: () => readonly Observation[]; committed: () => number;
}>;
declare global {interface Window {promptRetirementHarness?: Harness; disposePromptRetirement?: () => void;}}

/** Real React before-mutation/DOM focus fixture, not a playback Session or runtime fixture. */
export function mountPromptRetirementHarness() {
  const node = document.createElement('div');document.body.append(node);const root = createRoot(node);root.render(<HarnessView/>);
  return () => {root.unmount();node.remove();window.promptRetirementHarness = undefined;};
}
function HarnessView() {
  const root = useRef<HTMLDivElement | null>(null), seek = useRef<HTMLInputElement | null>(null), prompt = useRef<HTMLDivElement | null>(null);
  const [phase, setPhase] = useState<'in' | 'out'>('in'), [offer, setOffer] = useState(1), [rootKey, setRootKey] = useState(1);
  const [seekPresent, setSeekPresent] = useState(true), [blocked, setBlocked] = useState(false);
  const live = useRef(true), source = useRef({}), commits = useRef(0), observations = useRef<Observation[]>([]);
  const host = useMemo<PromptFocusHost>(() => ({root, seek, source: source.current, current: () => live.current, seekPresent, focusAllowed: true, controlsVisible: true}), [seekPresent]);
  useLayoutEffect(() => {
    commits.current++;
    window.promptRetirementHarness = {
      withdraw: () => setPhase('out'), replaceOffer: () => setOffer(value => value + 1), replaceRoot: () => {setRootKey(value => value + 1);setPhase('out');},
      removeSeekAndWithdraw: () => {setSeekPresent(false);setPhase('out');}, blockAndWithdraw: () => {setBlocked(true);setPhase('out');},
      retireAndWithdraw: () => {live.current = false;setPhase('out');}, observations: () => observations.current, committed: () => commits.current,
    };
  });
  return <><button type="button" data-prompt-outside>Outside</button><div key={rootKey} ref={root} tabIndex={-1} data-prompt-region onFocusCapture={event => {
    if (event.target !== seek.current && event.target !== root.current) return;
    observations.current.push({target: event.target === seek.current ? 'seek' : 'region', promptInert: prompt.current?.inert ?? false, promptPhase: prompt.current?.dataset.phase});
  }}>
    {seekPresent && <input ref={seek} type="range" tabIndex={0} data-prompt-seek aria-label="Position"/>}
    <WatchPromptFocus.Provider value={blocked ? undefined : host}>
      <ControlsWitness/>
      <WatchPromptRetirement active={phase === 'in'} paintKey={String(offer)} element={prompt}>
        <div key={offer} ref={prompt} data-prompt-fixture data-phase={phase} inert={phase === 'out'} aria-hidden={phase === 'out' || undefined}>
          <button type="button" data-prompt-action>Offer action</button>
        </div>
      </WatchPromptRetirement>
    </WatchPromptFocus.Provider>
  </div></>;
}
function ControlsWitness() {return <span data-prompt-controls={useWatchPromptControls(false)}/>;}
