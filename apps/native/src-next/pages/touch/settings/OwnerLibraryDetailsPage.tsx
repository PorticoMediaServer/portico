import React from 'react';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button, FormError} from '../../../kit/controls';
import {Tabs} from '../../../kit/controls/Tabs';
import {PanelBlock, SectionCard, SettingsPageFrame} from '../../../kit/frames';
import {OverlayFootRow} from '../../../kit/overlays/surface';
import {AdvancedTab, AttentionTab, FoldersTab, GeneralTab, MetadataTab, ScanningTab} from '../../../kit/settings/libraries/OwnerLibrarySections';
import {SettingsRow} from '../../../kit/shell';
import {StateView} from '../../../kit/states';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {LibraryTrashGroup} from '../../../kit/settings/libraries/LibraryTrashGroup';
import {ownerLibraryCounts, ownerLibraryScan, ownerLibraryState} from '../../../screens/settings-live/owner-libraries/presentation';
import type {useOwnerLibraries} from '../../shared/settings/useOwnerLibraries';

type Model = ReturnType<typeof useOwnerLibraries>;
type Props = {
  model: Model; library: NonNullable<Model['library']>; runtime: NativeShellRuntime;
  server: React.ReactNode; onMenu: () => void;
};

export function OwnerLibraryDetailsPage({model, library, runtime, server, onMenu}: Props) {
  const {api, controller, state, document, t, context, authority, viewerRole, parentCurrent, tab, setTab} = model;
  const active = ownerLibraryScan(library);
  const status = ownerLibraryState(library, {t, locale: runtime.text.locale});
  const tabs = [
    {value: 'general', label: t('next.ownerLibraries.tab.general')},
    {value: 'folders', label: t('next.ownerLibraries.folders')},
    {value: 'metadata', label: t('next.ownerLibraries.tab.metadata')},
    {value: 'scanning', label: t('next.ownerLibraries.tab.scanning')},
    {value: 'advanced', label: t('next.ownerLibraries.tab.advanced')},
  ];
  const save: React.ComponentProps<typeof GeneralTab>['save'] = patch => {void controller.save(patch);};
  const dashboard = () => context.navigation.open({kind: 'server-dashboard'}, authority);
  const cancel = () => {void controller.cancelScan();};
  const scanAll = () => {void controller.scanAll();};

  return <SettingsPageFrame title={library.name} server={server}
    back={{label: t('next.ownerLibraries.backLibraries'), onPress: () => model.setOpenId(undefined)}}>
    <SectionCard>
      <SettingsRow first label={active ? t('next.ownerLibraries.scanning') : status.text} description={ownerLibraryCounts(library, t)}/>
      <PanelBlock><OverlayFootRow layout="halves">
        <Button size="compact" align="stretch" icon="i-refresh" variant={active ? 'glass' : 'primary'}
          disabled={Boolean(active) || !library.actions.includes('scan')} loading={state.busy}
          onPress={() => {void controller.scanNow();}}>{t(active ? 'next.ownerLibraries.scanning' : 'web.libraries.scanNow')}</Button>
        <Button variant="glass" size="compact" icon="i-more" accessibilityLabel={t('next.ownerLibraries.libraryMenu')} onPress={onMenu}/>
      </OverlayFootRow></PanelBlock>
    </SectionCard>
    <Tabs testID="owner-libraries-tabs" value={tab} onValueChange={setTab} accessibilityLabel={library.name} items={tabs}/>
    {state.detail === 'loading' && !document && <SectionCard><SettingsRow first label={t('web.libraries.loadingOne')}/></SectionCard>}
    {state.detail === 'error' && <StateView variant="inline" {...runtime.error(state.detailError, () => {void controller.load(library.id);}, 'server-console')}/>}
    {state.error !== undefined && <PanelBlock><FormError placement="inline">{presentError(state.error, 'library', {operation: 'save'}).body}</FormError></PanelBlock>}
    {tab === 'general' && <GeneralTab library={library} document={document} t={t} busy={state.busy} save={save}
      renameLibrary={() => model.setRename(true)} removeLibrary={() => model.setLeaving(true)}/>}
    {tab === 'folders' && <FoldersTab t={t} state={state} retry={() => {void controller.reloadInventory();}}
      addFolder={() => model.setFolderForm({})} folderMenu={model.setFolderMenu}/>}
    {tab === 'metadata' && <>
      <MetadataTab document={document} metadata={state.metadata} busy={state.busy} t={t} save={save} state={state}
        parentCurrent={parentCurrent} saveAgent={id => {void controller.saveAgent(id);}}
        fetchLyrics={(value, current) => controller.fetchLyrics(value, current)}/>
      <AttentionTab state={state} t={t} more={() => {void controller.loadUnmatchedMore();}}/>
    </>}
    {tab === 'scanning' && <ScanningTab api={api} library={library} document={document} state={state} t={t}
      locale={runtime.text.locale ?? defaultI18n.region.formatLocale} skipped={() => model.setSkipped(true)}
      cancel={cancel} dashboard={dashboard} save={save} scanAll={scanAll} parentCurrent={parentCurrent}
      sonicToggle={(enabled, value, sonic, lease) => {void controller.setSonicEnabled(enabled, value, sonic, lease);}}
      sonicRetry={(value, sonic, lease) => {void controller.retrySonic(value, sonic, lease);}}
      sonicReload={(value, sonic, lease) => {void controller.reloadSonic(value, sonic, lease);}}/>}
    {tab === 'scanning' && viewerRole === 'owner' && <LibraryTrashGroup api={api} libraryId={library.id} parentCurrent={parentCurrent} t={t}/>}
    {tab === 'advanced' && <AdvancedTab document={document} t={t} owner={viewerRole === 'owner'} busy={state.busy} save={save}/>}
  </SettingsPageFrame>;
}
