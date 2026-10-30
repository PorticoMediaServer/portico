// Verification fixture only: real Settings components, explicitly synthetic data boundary.
import {useMemo, useRef, useState} from 'react';
import {createRoot} from 'react-dom/client';
import {defaultI18n} from '@i18n';
import {settingsSpec, settingsRowIndex, type SettingsRow, type SettingsValue} from '../../../packages/client-core/src/presentation/settings-spec';
import {SettingsAdvanced, SettingsGroup, SettingsOrderedList, SettingsPanel, SettingsPageHeading, SettingsRows, SettingsTable, SettingsWorkspace, SettingNumber, type TableQuery} from '../../src/kit/settings';
import {SettingsNav, SettingsRow as Row} from '../../src/kit/shell/SettingsSurfaces';
import {ServerHeader} from '../../src/kit/shell/ServerIdentity';
import {Button, RichSelect, TextField} from '../../src/kit/controls';
import {IconSprite} from '../../src/kit/Icon';
import '../../src/kit/tokens.css';
import '../../src/kit/fonts.css';
import '../../src/bootstrap.css';

const t = defaultI18n.t;
const initialPeople = Array.from({length: 500}, (_, index) => ({id: `fixture-${index}`, label: `Fixture person ${index}`, role: index % 2 ? 'member' : 'admin', cells: {
  person: {title: `fixture_${String(index).padStart(3,'0')}`, meta: `person${index}@example.test`, avatar: 'F'}, role: {title: index % 2 ? 'Member' : 'Administrator'},
  access: {title: 'Synthetic library', meta: '2 streams'}, profiles: {title: '2 profiles'}, active: {title: 'Fixture time'},
}}));
const proof = {queries: [] as TableQuery[], bulk: [] as readonly string[][], changes: [] as unknown[], creates: 0};
Object.assign(window, {settingsProof: proof});
function Fixture() {
  const [page, setPage] = useState('people'), [query, setQuery] = useState<TableQuery>({search: '', filters: {role: 'all'}, sort: {by: 'person', direction: 'asc'}}), [selected, setSelected] = useState<ReadonlySet<string>>(new Set());
  const [panel, setPanel] = useState<string>(), [tab, setTab] = useState('account'), [advanced, setAdvanced] = useState(false), [upload, setUpload] = useState<number | null>(null), [order, setOrder] = useState(['one','two','three']);
  const opener = useRef<HTMLElement | null>(null), [values, setValues] = useState<Record<string, SettingsValue>>({});
  const rows = useMemo(() => initialPeople.filter(row => row.cells.person.title.includes(query.search) && (query.filters.role === 'all' || row.role === query.filters.role)).sort((a,b) => query.sort.direction === 'asc' ? a.id.localeCompare(b.id,undefined,{numeric:true}) : b.id.localeCompare(a.id,undefined,{numeric:true})), [query]);
  const nav = (scope: string) => settingsSpec.filter(section => section.scope === scope).map(section => ({id: section.id, label: t(section.title), icon: 'i-server' as const, cluster: section.navGroup}));
  const pick = (id: string, element: HTMLElement) => {opener.current = element; setTab('account'); setPanel(id);};
  const state = (row: SettingsRow) => {
    const c = row.control, fallback = c.type === 'matrix' ? Object.fromEntries(c.items!.map(item => [item.id!, item.default!])) : c.default;
    const value = values[row.id] ?? fallback;
    return {present: true, value, defaultValue: fallback, changed: JSON.stringify(value) !== JSON.stringify(fallback), displayValue: String(value)};
  };
  return <><IconSprite /><SettingsWorkspace scope={page === 'playback' ? 'account' : 'server'} compactPage onBack={() => setPage('people')} navigation={<SettingsNav accountName="Synthetic profile" serverName="Synthetic server" viewerRole="owner" account={nav('account')} server={nav('server')} selectedId={page} query="" onQueryChange={() => {}} onNavigate={setPage} onSearchResult={() => {}} />}>
    {page !== 'playback' && <ServerHeader serverName="Synthetic server" viewerRole="owner" audience="administrators" health="normal" />}
    <SettingsPageHeading title={page === 'playback' ? 'Playback' : 'People'} />
    {page === 'playback' ? <SettingsRows holder={settingsSpec.find(section => section.id === 'playback')!} state={state} change={(row,value) => {proof.changes.push({id:row.id,value});setValues(old => ({...old,[row.id]:value}));}} reset={row => setValues(old => ({...old,[row.id]:state(row).defaultValue!}))} renderSpecial={() => null} advancedOpen={advanced} onAdvancedChange={setAdvanced} /> : <>
      <SettingsGroup title="People" plain actions={<Button size="compact" variant="primary" onClick={event => pick('new',event.currentTarget)}>Invite someone</Button>}>
        <SettingsTable label="Synthetic people" searchLabel="Search people" countLabel={`${rows.length} of 500 people`} emptyLabel="No one matches." rows={rows} columns={[
          {id:'person',label:'Person',kind:'main',sortable:true,width:'27%'},{id:'role',label:'Role',kind:'badge',sortable:true,width:'18%'},{id:'access',label:'Access',kind:'text',width:'22%'},{id:'profiles',label:'Profiles',kind:'muted',width:'15%'},{id:'active',label:'Last active',sortable:true,width:'18%'}]}
          query={query} onQueryChange={value => {proof.queries.push(value);setQuery(value);}} filters={[{id:'role',label:'Role',options:[{value:'all',label:'Everyone'},{value:'admin',label:'Administrators'},{value:'member',label:'Members'}]}]} matchedCount={rows.length} selected={selected} onSelectionChange={setSelected}
          bulkActions={[{id:'disable',label:'Turn off',onAction:ids => {proof.bulk=[...proof.bulk,ids];setSelected(new Set());}}]} rowAction={{label:'Edit',onAction:(row,element) => pick(row.id,element)}} />
      </SettingsGroup>
      <SettingsGroup title="Upload"><Row label="Upload speed"><SettingNumber label="Upload speed" value={upload} optional min={1} max={100000} unit="Mbps" plain onCommit={setUpload} /></Row></SettingsGroup>
      <SettingsAdvanced open={advanced} onOpenChange={setAdvanced} summary={['One change','A deliberately very long setting value that must be ellipsized in narrow windows','Another change','A fourth changed setting']}><SettingsGroup title="Order" advanced><SettingsOrderedList label="Synthetic libraries" items={order.map(id=>({id,label:id}))} numbered onReorder={ids=>setOrder([...ids])} onError={error=>{throw error;}} /></SettingsGroup></SettingsAdvanced>
      <SettingsGroup title="Menu"><Row label="Quality"><RichSelect aria-label="Quality" value="custom" options={[{value:'original',label:'Original',description:'The file as it is',isDefault:true},{value:'720p',label:'720p High',description:'4 Mbps, about 1.8 GB an hour'},{value:'custom',label:'Custom (set in Advanced)',description:'Shown when Advanced values match none of the choices above',disabled:true}]} /></Row></SettingsGroup>
    </>}
  </SettingsWorkspace>
    <SettingsPanel open={!!panel} onClose={()=>setPanel(undefined)} title={panel==='new'?'Invite someone':initialPeople.find(row=>row.id===panel)?.label??''} audience="Only you and administrators see this" returnFocusRef={opener}
      selectedTab={tab} onTabChange={setTab} create={panel==='new'?{label:'Make invitation',pending:false,onCreate:()=>{proof.creates++;setPanel(undefined);}}:undefined}
      tabs={panel==='new'?undefined:[{id:'account',title:'Account',content:<SettingsGroup title="Account"><Row label="Notes"><TextField aria-label="Notes" /></Row></SettingsGroup>},{id:'access',title:'Access',content:<SettingsGroup title="Access"><Row label="Streams at once"><SettingNumber label="Streams at once" value={2} min={1} max={10} onCommit={()=>{}} /></Row></SettingsGroup>},{id:'profiles',title:'Profiles',content:<p>Synthetic profile boundary</p>},{id:'devices',title:'Devices',content:<p>Synthetic device boundary</p>},{id:'advanced',title:'Advanced',content:<p>Synthetic permission boundary</p>}]}
    >{panel==='new'&&<SettingsGroup title="Invitation"><Row label="Notes"><TextField aria-label="Invitation notes" /></Row></SettingsGroup>}</SettingsPanel>
  </>;
}
createRoot(document.getElementById('root')!).render(<Fixture />);
