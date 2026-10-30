import {createRoot} from 'react-dom/client';
import {useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import {DownloadsService} from '@core/downloads-service.ts';
import {ServerDownloads} from '../../src/screens/saved/Downloads';
import {SavedOperations} from '../../src/screens/saved/operations';
import {ResourceFields} from '../../src/screens/saved/ResourceFields';
import {SavedDialog} from '../../src/screens/saved/SavedDialog';
import {Button} from '../../src/kit/controls';
import {IconSprite} from '../../src/kit/Icon';
import '../../src/kit/tokens.css';
import '../../src/kit/fonts.css';
import '../../src/bootstrap.css';

// Component-only failure proof: the actual DownloadsService reads through a
// real loopback proxy whose upstream refuses the connection. No rows, native
// store, authority, successful API response or application shell is fabricated.
let current = true;
const operations = new SavedOperations(() => current);
const service = new DownloadsService({async request<T>(path: string, method = 'GET', body?: unknown, signal?: AbortSignal): Promise<T> {
  const response = await fetch(path, {method, body: body === undefined ? undefined : JSON.stringify(body), signal});
  if (!response.ok) throw new Error(`HTTP ${response.status}`);
  return await response.json() as T;
}});
const root = createRoot(document.getElementById('root')!);
const errors: unknown[] = [];
const unexpected = () => { throw new Error('No preparation was offered in this failure state.'); };
function DraftFieldsProof() {
  const [name, setName] = useState(''), [summary, setSummary] = useState(''), [open, setOpen] = useState(true), input = useRef<HTMLInputElement>(null);
  // Only actual local text entry is offered. No captured resource writer exists
  // in this isolated component host, so Create is disabled, with no fake handler.
  return <SavedDialog open={open} onOpenChange={setOpen} title={defaultI18n.t('web.saved.newPlaylist')} initialFocusRef={input}
    footer={<><Button size="small" onClick={() => setOpen(false)}>{defaultI18n.t('action.cancel')}</Button><Button size="small" variant="primary" disabled>{defaultI18n.t('web.saved.create')}</Button></>}>
    <ResourceFields nameRef={input} name={name} summary={summary} onNameChange={setName} onSummaryChange={setSummary} nameMaxLength={200} summaryMaxLength={4000} />
  </SavedDialog>;
}
const proof = {
  service, errors,
  mount() {
    root.render(<><IconSprite /><ServerDownloads service={service} operations={operations} online artwork={() => undefined}
      effects={{openItem: unexpected, saveFile: async () => unexpected(), reportError: error => errors.push(error)}} /></>);
  },
  mountDraftFields() { root.render(<><IconSprite /><DraftFieldsProof /></>); },
  dispose() { current = false; operations.dispose(); service.dispose(); root.unmount(); },
};
Object.assign(window, {savedDownloadsComponentProof: proof});
