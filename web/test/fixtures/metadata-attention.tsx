import {useEffect} from 'react';
import {createRoot} from 'react-dom/client';
import {IconSprite} from '../../src/kit/Icon';
import {NeedsAttention} from '../../src/screens/metadata/NeedsAttention';
import '../../src/kit/tokens.css';
import '../../src/kit/fonts.css';
import '../../src/bootstrap.css';

// owner-metadata frame 2 presentation only: the drawn titles, reasons and matches as DEV
// fixture rows. No account, server, API call, match or dismissal is installed.
if (!import.meta.env.DEV) throw new Error('Metadata specimens require development.');
const key = (seed: number) => seed.toString(16).padStart(64, '0');
const candidate = (seed: number, title: string, year: number, reasons: string[], orders: {id: string; name: string}[], provider: 'tmdb' | 'tvdb' | 'anilist' = 'tmdb') => ({
  key: key(seed), identity: {provider, type: 'show' as const, id: String(100 + seed)}, title, year, confidence: 0.8,
  reasons, orders});
const entries = [
  {libraryId: 'library', entityId: 'one-piece', kind: 'show' as const, itemKind: 'show', parentId: '', actions: ['accept', 'search', 'id', 'retry', 'dismiss'],
    title: 'One Piece', revision: 1, reason: 'needs_order', confidence: 0.6, candidates: [
      candidate(1, 'One Piece', 1999, ['exact_title_or_alias', 'release_year_agrees'], [{id: 'official', name: 'Official'}, {id: 'seasonal', name: 'Seasonal'}]),
      candidate(2, 'One Piece', 2023, ['exact_title_or_alias', 'release_year_conflicts'], [{id: 'official', name: 'Official'}])]},
  {libraryId: 'library', entityId: 'hunter', kind: 'show' as const, itemKind: 'show', parentId: '', actions: ['accept', 'search', 'id', 'retry', 'dismiss'],
    title: 'Hunter x Hunter', revision: 1, reason: 'needs_selection', confidence: 0.4, candidates: [
      candidate(3, 'Hunter x Hunter', 1999, ['exact_title_or_alias', 'release_year_unknown'], [{id: 'official', name: 'Official'}]),
      candidate(4, 'Hunter x Hunter', 2011, ['exact_title_or_alias', 'release_year_unknown'], [{id: 'official', name: 'Official'}])]},
  {libraryId: 'library', entityId: 'naruto', kind: 'show' as const, itemKind: 'show', parentId: '', actions: ['search', 'id', 'retry', 'dismiss'],
    title: 'Naruto Shippūden', revision: 1, reason: 'unmatched', confidence: 0, candidates: []},
  {libraryId: 'library', entityId: 'episode', kind: 'item' as const, itemKind: 'episode', parentId: 'parent-show', parentTitle: 'A show with no match yet',
    actions: ['parent', 'dismiss'], title: 'The episode', revision: 1, reason: 'needs_parent_match', confidence: 0, candidates: []},
];
const total = entries.length;
// A read-only attention page; every command is inert so the specimen cannot pretend to work.
// ?empty draws frame 4's done state; the drawn pending row above already draws its working state.
const empty = new URLSearchParams(location.search).has('empty');
const working = new URLSearchParams(location.search).has('working');
const shown = working ? [{...entries[0], reason: 'pending', candidates: [], actions: []}, {...entries[2], reason: 'searching', candidates: [], actions: []}, entries[3]] : entries;
const api = {request: async <T,>() => ({libraryId: 'library', total: empty ? 0 : shown.length, entries: empty ? [] : shown, nextCursor: ''}) as T};
// ?dialog=search or ?dialog=id clicks the shipped row action after paint, so the drawn
// dialogs open through the production path; ?number=1 types the bare number that asks for
// the provider picker. No dialog markup is duplicated here.
function Specimen() {
  const params = new URLSearchParams(location.search), dialog = params.get('dialog'), number = params.get('number') === '1';
  useEffect(() => {
    if (!dialog) return;
    const label = dialog === 'search' ? 'Search by hand' : 'Enter a provider ID';
    const open = () => {
      const button = [...document.querySelectorAll('button')].find(item => (item.textContent || '').trim() === label);
      if (!button) return false;
      button.click();
      return true;
    };
    if (open()) return;
    const observer = new MutationObserver(() => {if (open()) observer.disconnect();});
    observer.observe(document.getElementById('root')!, {childList: true, subtree: true});
    return () => observer.disconnect();
  }, [dialog]);
  useEffect(() => {
    if (!dialog || !number) return;
    const timer = setTimeout(() => {
      const input = document.querySelector('input');
      if (!input) return;
      const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set;
      set?.call(input, '280');
      input.dispatchEvent(new Event('input', {bubbles: true}));
    }, 400);
    return () => clearTimeout(timer);
  }, [dialog, number]);
  return <div style={{padding: '32px', maxWidth: 1000}}><IconSprite /><NeedsAttention libraryId="library" libraryName="Anime" api={api} /></div>;
}
createRoot(document.getElementById('root')!).render(<Specimen />);
