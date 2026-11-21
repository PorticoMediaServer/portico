import {createSettingsOpen, SettingsOpenContext} from './quiet-refresh';
import {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import {settingsRowIndex, settingsSectionTab, settingsSpec, type SettingsHolder, type SettingsSectionTab, type SettingsRow, type SettingsSection, type SettingsValue} from '@core/presentation/settings-spec.ts';
import {SettingsNav, SettingsProfileCard, ShortcutTiles, type SettingsSearchGroup} from '../../kit/shell/SettingsSurfaces';
import type {ProfileIdentity, ShellAction} from '../../kit/shell';
import {SettingsPageFailure, SettingsPanel, SettingsRows, searchSettings, settingsRoute, type SettingPresentation, type SettingsRoute, type SettingsPanelProps} from '../../kit/settings';
import type {FactPart} from '@core/presentation/kit-model/index.ts';
import {Icon} from '../../kit/Icon';
import {SettingsListFrame, SettingsPageFrame} from '../../kit/frames';
import {Button} from '../../kit/controls';
import {Dialog, useRetained} from '../../kit/overlays';
import {settingsChangeConfirmation} from '@core/presentation/settings-change-confirm.ts';
import {Tabs} from '../../kit/controls/Choices';
import {router, settingsReturnHref} from '../../router';
import {settingsIconName} from '../../kit/settings/icons';
import {ServerHeader, type ServerAudience} from '../../kit/shell/ServerIdentity';
import {useShellWidth} from '../../shell/viewport';
import {configurationFieldEligible} from './eligibility';
import {settingsRowVisible, settingsSectionVisible, type SettingsEnvironment} from '@core/presentation/settings-visibility.ts';
import {useWebAction} from '../../route-registry-view';
import kitStyles from '../../kit/settings/settings.module.css';
import styles from './settings.module.css';

const t = defaultI18n.t;
const noPresentation = () => () => {};
const emptyPresentation = () => undefined;
export type SettingsContext = Readonly<{section: SettingsSection; panelId?: string; tabId?: string; recordId?: string;
  /** The page's own tab (`section.tabs`, owner-server 8, live-owner 1), for a page that draws itself. */
  pageTab?: string}>;
/** The groups one page tab holds. A tab made only of Advanced groups (Limits and retention) draws them as its own
 * sections, open, as owner-server 9 draws it; otherwise they stay under the tab's Advanced. */
function tabHolder(section: SettingsSection, tab: SettingsSectionTab): SettingsHolder {
  const advanced = (tab.advanced ?? []).map(index => section.advanced!.groups![index]!);
  if (!tab.groups.length) return {groups: advanced};
  return {groups: tab.groups.map(index => section.groups![index]!), ...(advanced.length ? {advanced: {groups: advanced}} : {})};
}
export type SettingsPageSnapshot = Readonly<{loading: boolean; error?: unknown; saveState?: 'saving' | 'saved'}>;
export type SettingsConfigurationMatch = Readonly<{id: string; key: string; label: string; current: unknown; effective: unknown; default: unknown; source: string; configOnly: boolean; path: string}>;
/** Session-owned, checked readers and commands. The screen holds navigation and focus only. */
export type SettingsBindings = Readonly<{
  setOpen?: (open: boolean) => void;
  inputActive?: (active: boolean) => void;
  presentation?: Readonly<{subscribe: (listener: () => void) => () => void; getSnapshot: () => unknown}>;
  accountName: string; serverName: string; role: 'owner' | 'admin' | 'member';
  environment: SettingsEnvironment;
  subscribe: (listener: () => void) => () => void; getSnapshot: (section: SettingsSection) => SettingsPageSnapshot;
  sectionVisible: (section: SettingsSection) => boolean;
  eligible: (row: SettingsRow, context: SettingsContext) => boolean;
  audience: (section: SettingsSection) => ServerAudience;
  serverHealth?: () => 'normal' | 'attention' | undefined;
  /** Approved uptime, free-space and database facts, in order, from checked live readers. */
  serverFacts: () => readonly FactPart[];
  rowState: (row: SettingsRow, context: SettingsContext) => SettingPresentation;
  change: (row: SettingsRow, value: SettingsValue, context: SettingsContext) => void;
  reset: (row: SettingsRow, context: SettingsContext) => void;
  special: (row: SettingsRow, state: SettingPresentation, context: SettingsContext, openPanel: (panelId: string, recordId?: string) => void) => ReactNode;
  section?: (context: SettingsContext, openPanel: (panelId: string, recordId?: string) => void, renderRows: (holder: SettingsHolder) => ReactNode) => ReactNode;
  configurationMatches?: (query: string) => readonly SettingsConfigurationMatch[];
  panelIntro?: (context: SettingsContext) => ReactNode;
  panelHeader?: (context: SettingsContext, title: string) => ReactNode;
  panel?: (context: SettingsContext) => Readonly<{title?: string; description?: string; create?: SettingsPanelProps['create']}>;
  load: (section: SettingsSection) => Promise<void>;
  loadContext?: (context: SettingsContext) => Promise<void>;
  openHome: () => void;
}>;

export type SettingsPhoneLead = Readonly<{profile: ProfileIdentity; description: string; shortcuts: readonly ShellAction[]}>;
export function SettingsScreen({bindings, route, navigate, openDetail = false, phoneLead}: {bindings: SettingsBindings; route: SettingsRoute; navigate: (route: SettingsRoute) => void;
  /** At phone width: the profile card and the quick tiles over the list. */
  phoneLead?: SettingsPhoneLead;
  /** A link that names a scope or section asks for that page, not the compact list. */
  openDetail?: boolean}) {
  useSyncExternalStore(bindings.presentation?.subscribe ?? noPresentation, bindings.presentation?.getSnapshot ?? emptyPresentation, emptyPresentation);
  const width = useShellWidth(), compact = width < 600, feedback = useWebAction('feedback');
  const [quietOpen]=useState(createSettingsOpen);
  const [query, setQuery] = useState(''), [advanced, setAdvanced] = useState(false);
  // At phone width the URL decides: a link that names a section shows that page, the bare /settings shows the list.
  const compactPage = compact && openDetail;
  const [panel, setPanel] = useState<{id: string; recordId?: string; tab?: string} | undefined>();
  // O-151: the few remote-access edits that can cut people off ask once; everything else still saves at once.
  const [confirming, setConfirming] = useState<{row: SettingsRow; value: SettingsValue; context: SettingsContext; message: string}>();
  const shownConfirming = useRetained(confirming);
  const shown=!compact || compactPage;
  useLayoutEffect(()=>{quietOpen.setOpen(shown);bindings.setOpen?.(shown);return()=>{quietOpen.setOpen(false);bindings.setOpen?.(false);};},[quietOpen,bindings,shown]);
  const [configurationKey, setConfigurationKey] = useState<string>();
  const opener = useRef<HTMLElement | null>(null);
  const environment = {...bindings.environment, role: bindings.role};
  const sectionVisible = (section: SettingsSection) => settingsSectionVisible(section, environment) && bindings.sectionVisible(section);
  const rowState = (row: SettingsRow, context: SettingsContext): SettingPresentation => settingsRowVisible(row, environment) && bindings.eligible(row, context) ? bindings.rowState(row, context) : {present: false};
  const rowContext = (row: SettingsRow, section: SettingsSection): SettingsContext => {
    const location = settingsRowIndex.get(row.id);
    return {section, panelId: location?.panelId, tabId: location?.tabId,
      recordId: panel?.id === location?.panelId && route.scope === section.scope && route.section === section.id ? panel?.recordId : undefined};
  };
  const selected = settingsRoute(route, sectionVisible, (row, section) => rowState(row, rowContext(row, section)).present);
  const section = settingsSpec.find(section => section.scope === selected.scope && section.id === selected.section)!;
  const snapshot = useSyncExternalStore(bindings.subscribe, () => bindings.getSnapshot(section), () => bindings.getSnapshot(section));
  // A page with tabs opens on the tab holding a found row, else its first; changing page starts at the first again.
  const [pageTabs, setPageTabs] = useState<Readonly<Record<string, string>>>({});
  const sectionKey = `${section.scope}/${section.id}`, foundTab = selected.row ? settingsSectionTab(section, selected.row)?.id : undefined;
  const pageTab = section.tabs ? pageTabs[sectionKey] ?? foundTab ?? section.tabs[0]!.id : undefined;
  useEffect(() => {if (foundTab) setPageTabs(current => ({...current, [sectionKey]: foundTab}));}, [foundTab, sectionKey]);
  const context: SettingsContext = pageTab ? {section, pageTab} : {section};
  const openPanel = (id: string, recordId?: string) => {
    if (!id) {setPanel(undefined);opener.current?.focus();return;}
    if (!section.panels?.[id]) return;
    opener.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    setPanel({id, recordId, tab: section.panels[id].tabs?.[0]?.id});
  };
  // A row of a library's own panel is found while no library is open (W2 F4: "missing" found nothing): the hit names
  // the panel and tab and opens Libraries, where each library has it.
  const libraryPanelRow = (row: SettingsRow, owner: SettingsSection) => owner.scope === 'server' && owner.id === 'libraries' && settingsRowIndex.get(row.id)?.panelId === 'library';
  const matches = searchSettings(query, t, (row, owner) => sectionVisible(owner) && (rowState(row, rowContext(row, owner)).present || libraryPanelRow(row, owner)));
  const configuration = (bindings.configurationMatches?.(query) ?? []).filter(field => configurationFieldEligible(field, (row, context) => settingsRowVisible(row, environment) && bindings.eligible(row, rowContext(row, context.section))));
  const groups: SettingsSearchGroup[] = settingsSpec.filter(sectionVisible).map(section => ({id: `${section.scope}/${section.id}`, label: t(section.title), icon: settingsIconName(section.icon), area: section.scope,
    rows: [...matches.filter(hit => hit.section === section).map(hit => ({id: hit.rowId, label: hit.label, description: hit.context.join(' · ')})),
      ...configuration.filter(fact => fact.configOnly && section.configOnly.some(field => field.key === fact.key)).map(fact => ({id: fact.key, label: fact.label, description: fact.key}))]})).filter(group => group.rows.length);
  const go = (id: string, row?: string) => {
    const [scope, page] = id.split('/');
    if (scope !== 'account' && scope !== 'server') return;
    const location = row && settingsRowIndex.get(row);
    const configuration = row && bindings.configurationMatches?.(query).find(field => field.key === row && field.configOnly);
    navigate({scope, section: page, ...(row && !configuration ? {row} : {})}); setConfigurationKey(configuration ? row : undefined);
    setPanel(current => current && location && location.panelId === current.id && selected.scope === scope && selected.section === page ? {...current, tab: location.tabId} : undefined);
    setAdvanced(false);
  };
  useEffect(() => {void bindings.load(section);}, [bindings, section]);
  useEffect(() => {void bindings.loadContext?.({section, panelId: panel?.id, recordId: panel?.recordId});}, [bindings, section, panel?.id, panel?.recordId]);
  useEffect(() => {
    const found = selected.row && settingsRowIndex.get(selected.row);
    if (!found) return;
    if (found.advanced) setAdvanced(true);
    if (found.panelId) setPanel(current => ({id: found.panelId!, tab: found.tabId, ...(current && current.id === found.panelId ? {recordId: current.recordId} : {})}));
    const frame = requestAnimationFrame(() => document.getElementById(found.row.id)?.scrollIntoView({block: 'center', behavior: 'instant'}));
    return () => cancelAnimationFrame(frame);
  }, [selected.scope, selected.section, selected.row]);
  const rows = (holder: Parameters<typeof SettingsRows>[0]['holder'], context: SettingsContext) => <SettingsRows holder={{...holder, groups: holder.groups?.map(group => ({...group, rows: group.rows.filter(row => !(bindings.panelHeader && context.panelId === 'library' && row.id === 'libraries.scanNow')).map(row => context.panelId === 'library' && row.id === 'libraries.name' ? {...row, control: {...row.control, type: 'action' as const}} : row)}))}} environment={environment} state={row => rowState(row, context)}
    change={(row, value) => {
      const state = rowState(row, context);
      if (!state.present) return;
      const question = settingsChangeConfirmation(row.id, value, state.value);
      if (question) setConfirming({row, value, context, message: t(question)}); else bindings.change(row, value, context);
    }} reset={row => {if (rowState(row, context).present) bindings.reset(row, context);}} renderSpecial={(row, state) => rowState(row, context).present ? bindings.special(row, state, context, openPanel) : null}
    query={query} foundRowId={selected.row} advancedOpen={advanced} onAdvancedChange={setAdvanced} />;
  const navigation = <SettingsNav accountName={bindings.accountName} serverName={bindings.serverName} viewerRole={bindings.role === 'admin' ? 'administrator' : bindings.role}
    account={settingsSpec.filter(section => section.scope === 'account' && sectionVisible(section)).map(section => ({id: `account/${section.id}`, label: t(section.title), icon: settingsIconName(section.icon), cluster: section.navGroup}))}
    server={settingsSpec.filter(section => section.scope === 'server' && (!compact || section.id !== 'dashboard') && sectionVisible(section)).map(section => ({id: `server/${section.id}`, label: t(section.title), icon: settingsIconName(section.icon), cluster: section.navGroup}))}
    selectedId={compact ? "" : `${selected.scope}/${selected.section}`} selectedSearchResult={selected.row ? {pageId: `${selected.scope}/${selected.section}`, rowId: selected.row} : undefined}
    actions={feedback ? [{id: 'feedback', icon: 'i-flag', label: t('next.shell.feedback'), onAction: feedback}] : []}
    query={query} onQueryChange={setQuery} onNavigate={go} matches={groups} onSearchResult={go} layout={compact ? 'phone' : 'desktop'} />;
  const panelSpec = panel && section.panels?.[panel.id], tab = panelSpec?.tabs?.find(tab => tab.id === panel?.tab);
  const panelContext: SettingsContext = {section, panelId: panel?.id, tabId: tab?.id, recordId: panel?.recordId};
  const panelState = bindings.panel?.(panelContext);
  const libraryPage = section.id === 'libraries' && section.scope === 'server' && panel?.id === 'library' && !!panel.recordId && !!panelSpec;
  const nameRow = settingsRowIndex.get('libraries.name')?.row;
  const libraryName = libraryPage && nameRow ? rowState(nameRow, panelContext).value : undefined;
  const libraryTitle = typeof libraryName === 'string' && libraryName ? libraryName : panelState?.title ?? (panelSpec?.title ? t(panelSpec.title) : t(section.title));
  const audience = bindings.audience(section);
  const fact = configuration.find(fact => fact.key === configurationKey);
  const editing = (target: EventTarget | null) => target instanceof HTMLElement && (target.isContentEditable || target instanceof HTMLTextAreaElement || target instanceof HTMLInputElement && !['button','checkbox','radio','range','submit','reset','file','color','hidden'].includes(target.type));
  const done = {label: t('next.settings.done'), onPress: () => {void router.navigate({href: settingsReturnHref()});}};
  const server = selected.scope === 'server' && bindings.role !== 'member' ? bindings.role === 'owner' ? <ServerHeader serverName={bindings.serverName} viewerRole="owner" audience={audience} health={bindings.serverHealth?.()} facts={bindings.serverFacts()} /> : <ServerHeader serverName={bindings.serverName} viewerRole="administrator" audience="administrators" facts={bindings.serverFacts()} /> : undefined;
  const closeLibrary = () => {setPanel(undefined);opener.current?.focus();};
  // A library opened from Libraries is its own page: Back returns to Libraries, and its artwork band is its header.
  const page = <SettingsPageFrame title={libraryPage ? String(libraryTitle) : t(section.title)} status={snapshot.saveState} action={done} server={server}
    back={libraryPage ? {label: t(section.title), onPress: closeLibrary} : {label: t('next.shell.settings'), onPress: () => {setPanel(undefined); void router.navigate({to: '/settings'});}}}
    header={libraryPage && bindings.panelHeader ? bindings.panelHeader(panelContext, libraryTitle) : undefined}>
    {libraryPage && panelSpec?.tabs ? <Tabs aria-label={libraryTitle} value={panel?.tab ?? panelSpec.tabs[0]?.id} onValueChange={value => setPanel(current => current && {...current, tab: value})}
      phone={compact} items={panelSpec.tabs.map(item => ({value: item.id, label: t(item.title), panel: <>{bindings.panelIntro?.({...panelContext, tabId: item.id})}{rows(item, {...panelContext, tabId: item.id})}</>}))}/> : fact ? <ConfigurationFact fact={fact} /> : snapshot.error ? <SettingsPageFailure retry={() => {void bindings.load(section);}} home={bindings.openHome} error={snapshot.error}
      server={section.scope === 'server' ? {name: bindings.serverName, openMySettings: () => go('account/profile')} : undefined} /> : snapshot.loading ? <p role="status">{t('next.settings.loading')}</p> : (section.tabs ? <Tabs aria-label={t(section.title)} value={pageTab} phone={compact} onValueChange={value => setPageTabs(current => ({...current, [sectionKey]: value}))}
        items={section.tabs.map(tab => ({value: tab.id, label: t(tab.title), panel: bindings.section?.({...context, pageTab: tab.id}, openPanel, holder => rows(holder, context)) ?? rows(tabHolder(section, tab), context)}))} />
        : bindings.section?.(context, openPanel, holder => rows(holder, context)) ?? rows(section, context))}
  </SettingsPageFrame>;
  return <SettingsOpenContext.Provider value={quietOpen}><div className={styles.screen} onFocusCapture={event => {if(editing(event.target))bindings.inputActive?.(true);}}
    onBlurCapture={event => {if(editing(event.target) && !editing(event.relatedTarget))bindings.inputActive?.(false);}}>
    <SettingsListFrame bar={{title: t('next.shell.settings'), action: done}} list={navigation}
      profile={phoneLead && <SettingsProfileCard profile={phoneLead.profile} description={phoneLead.description} onPress={() => go('account/profile')} />} shortcuts={phoneLead && <ShortcutTiles compact items={phoneLead.shortcuts} />} page={shown ? page : undefined} />
    {panel && panelSpec && !libraryPage && <SettingsPanel open onClose={() => setPanel(undefined)} audience={t(audience === 'owner' ? 'next.shell.editorAudience.owner' : 'next.shell.serverVisibility.ownerShared')} title={panelState?.title ?? (panelSpec.title ? t(panelSpec.title) : t(section.title))} description={panelState?.description} create={panelState?.create} returnFocusRef={opener}
      saveState={snapshot.saveState} tabs={panelSpec.tabs?.map(tab => ({id: tab.id, title: t(tab.title), content: rows(tab, {...panelContext, tabId: tab.id})}))} selectedTab={panel.tab} onTabChange={value => setPanel(current => current && {...current, tab: value})}>
      {rows(tab ?? panelSpec, panelContext)}
    </SettingsPanel>}
    {shownConfirming && <Dialog footerDismisses open={!!confirming} title={t(shownConfirming.row.label)} onOpenChange={open => {if (!open) setConfirming(undefined);}}
      footer={<><Button onClick={() => setConfirming(undefined)}>{t('action.cancel')}</Button>
        <Button variant="primary" onClick={() => {const pending = shownConfirming; setConfirming(undefined);
          if (rowState(pending.row, pending.context).present) bindings.change(pending.row, pending.value, pending.context);}}>{t('settings.confirm.change')}</Button></>}>
      <p>{shownConfirming.message}</p>
    </Dialog>}
  </div></SettingsOpenContext.Provider>;
}

function ConfigurationFact({fact}: {fact: SettingsConfigurationMatch}) {
  const value = (value: unknown) => value === null ? t('next.settings.notSet') : typeof value === 'string' ? value : JSON.stringify(value);
  return <section className={kitStyles.configurationNote} aria-label={fact.label}><Icon name="i-info" size={20} /><span>{fact.label}{fact.label ? '. ' : ''}{t('next.settings.configOnly.before')}{' '}<code>{fact.key}</code>{' = '}<code>{value(fact.default)}</code>{t('next.settings.configOnly.after')}</span></section>;
}
