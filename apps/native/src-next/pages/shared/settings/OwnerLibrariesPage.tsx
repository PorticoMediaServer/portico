import React from 'react';
import {Button} from '../../../kit/controls';
import {PanelBlock, SectionCard, SettingsPageFrame} from '../../../kit/frames';
import {Menu} from '../../../kit/overlays/Menu';
import {LibraryListRow, RemoveFolderSheet, RemoveLibrarySheet, RenameSheet, SkippedSheet} from '../../../kit/settings/libraries/OwnerLibrarySections';
import {SettingsHelp} from '../../../kit/settings/SettingsParts';
import {SettingsRow} from '../../../kit/shell';
import {ServerHeader} from '../../../kit/shell/ServerIdentity';
import {StateView} from '../../../kit/states';
import {AddFolderPage} from '../../../screens/settings-live/owner-libraries/AddFolderPage';
import {AddLibraryPage} from '../../../screens/settings-live/owner-libraries/AddLibraryPage';
import {libraryListState} from '../../../screens/settings-live/owner-libraries/library-settings-model';
import {ownerLibraryState} from '../../../screens/settings-live/owner-libraries/presentation';
import {OwnerLibraryDetailsPage} from './OwnerLibraryDetailsPage';
import {useOwnerLibraries} from './useOwnerLibraries';

type Props = Parameters<typeof useOwnerLibraries>[0]
  & Pick<React.ComponentProps<typeof SettingsPageFrame>, 'server' | 'back'> & {retry?: React.ReactNode};

export function OwnerLibrariesPage({ports, runtime, initialLibraryId, lifetime, server, back, retry}: Props) {
  const model = useOwnerLibraries({ports, runtime, initialLibraryId, lifetime});
  const {t, api, controller, state, context, viewerRole, libraries, library, document, open, libraryMenu, folderMenu, removingFolder, folderForm} = model;
  const serverHeader = server ?? <ServerHeader serverName={context.serverName} viewerRole={viewerRole}
    audience={viewerRole === 'administrator' ? 'administrators' : 'owner'} variant="phone"/>;

  if (model.adding) return <AddLibraryPage api={api} serverName={context.serverName} ports={ports} runtime={runtime} busy={state.busy} error={state.error}
    onCancel={() => model.setAdding(false)}
    onCreate={input => {void controller.create(input).then(id => {if (id) {model.setAdding(false); open(id);}});}}/>;

  if (folderForm && document) return <AddFolderPage api={api} serverName={context.serverName} ports={ports} runtime={runtime} source={folderForm.source}
    expectedRevision={state.inventory?.revision} busy={state.busy} error={state.error} onCancel={() => model.setFolderForm(undefined)}
    onSave={input => {void controller.saveFolder(folderForm.source?.id, input).then(ok => {if (ok) model.setFolderForm(undefined);});}}/>;

  const phase = libraryListState(state.directory.phase, libraries.length);
  const menu = libraryMenu && <Menu open onOpenChange={() => model.setLibraryMenu(undefined)} title={libraryMenu.name}
    items={model.menuOnPage ? [
      {id: 'rename', label: t('next.ownerLibraries.rename'), icon: 'i-pencil', onSelect: () => model.setRename(true)},
      {id: 'scanAll', label: t('next.ownerLibraries.scanAll'), icon: 'i-refresh', description: t('next.ownerLibraries.scanAllHelp'),
        disabled: !libraryMenu.actions.includes('scan'), onSelect: () => {void controller.scanAll();}},
    ] : [
      {id: 'edit', label: t('next.ownerLibraries.rename'), icon: 'i-sliders', onSelect: () => open(libraryMenu.id)},
      {id: 'scan', label: t('web.libraries.scanNow'), icon: 'i-refresh',
        disabled: !libraryMenu.actions.includes('scan'), onSelect: () => {void controller.scanLibrary(libraryMenu.id);}},
      {id: 'attention', label: t('next.ownerLibraries.tab.attention'), icon: 'i-link', onSelect: () => {open(libraryMenu.id); model.setTab('metadata');}},
      {id: 'more-separator', separator: true},
      {id: 'moveUp', label: t('server.libraries.moveUp'), icon: 'i-up', onSelect: () => {void controller.move(libraryMenu.id, -1);}},
      {id: 'moveDown', label: t('server.libraries.moveDown'), icon: 'i-down', onSelect: () => {void controller.move(libraryMenu.id, 1);}},
    ]}/>;

  return <>
    {library ? <OwnerLibraryDetailsPage model={model} library={library} runtime={runtime} server={serverHeader}
      onMenu={() => {model.setMenuOnPage(true); model.setLibraryMenu(library);}}/>
      : <SettingsPageFrame title={t('settings.server.libraries')} testID="owner-libraries-page" server={serverHeader} back={back} intro={t('next.ownerLibraries.lede')}>
        {retry}
        <SectionCard>
          <PanelBlock><Button size="compact" icon="i-plus" onPress={() => model.setAdding(true)}>{t('web.empty.addLibrary')}</Button></PanelBlock>
        </SectionCard>
        {state.directory.phase === 'error' && <StateView variant="inline" {...runtime.error(state.directory.error, () => {void controller.refresh();}, 'server-console')}/>}
        {(libraries.length > 0 || phase === 'loading' || phase === 'empty') && <SectionCard>
          {phase === 'loading' && <SettingsRow first label={t('next.ownerLibraries.loading')}/>}
          {phase === 'empty' && <SettingsRow first label={t('next.ownerLibraries.empty')} description={t('next.ownerLibraries.emptyHelp')}/>}
          {libraries.map((item, index) => <LibraryListRow key={item.id} library={item} first={index === 0} t={t} runtime={runtime}
            state={ownerLibraryState(item, {t, locale: runtime.text.locale, short: true})}
            onOpen={() => open(item.id)} onMenu={() => {model.setMenuOnPage(false); model.setLibraryMenu(item);}}/>)}
        </SectionCard>}
        <SettingsHelp>{t('next.ownerLibraries.moveHint')}</SettingsHelp>
      </SettingsPageFrame>}
    {menu}
    {library && <>
      <SkippedSheet open={model.skipped} onOpenChange={model.setSkipped} state={state} t={t}
        scan={() => {void controller.scanNow();}} more={() => {void controller.loadSkippedMore();}}/>
      <RemoveLibrarySheet open={model.leaving} onOpenChange={model.setLeaving} library={library} busy={state.busy} error={state.error} t={t}
        remove={confirmation => {
          void controller.remove(confirmation).then(ok => {if (ok) {model.setLeaving(false); model.setOpenId(undefined);}});
        }}/>
      <RenameSheet open={model.rename} onOpenChange={model.setRename} library={library} busy={state.busy} error={state.error} t={t}
        save={name => {void controller.rename(name).then(ok => {if (ok) model.setRename(false);});}}/>
      <RemoveFolderSheet open={Boolean(removingFolder)} onOpenChange={() => model.setRemovingFolder(undefined)}
        source={removingFolder} busy={state.busy} error={state.error} t={t}
        remove={() => {if (removingFolder) void controller.removeFolder(removingFolder.id).then(ok => {if (ok) model.setRemovingFolder(undefined);});}}/>
      <Menu open={Boolean(folderMenu)} onOpenChange={() => model.setFolderMenu(undefined)} title={folderMenu?.name || folderMenu?.path}
        items={folderMenu ? [
          {id: 'scan', label: t('web.libraries.scanFolder'), icon: 'i-refresh', onSelect: () => {void controller.scanFolder(folderMenu.id);}},
          {id: 'check', label: t('web.libraries.checkAvailability'), icon: 'i-disk', onSelect: () => {void controller.checkFolder(folderMenu.id);}},
          {id: 'edit', label: t('web.libraries.editFolder'), icon: 'i-pencil', onSelect: () => model.setFolderForm({source: folderMenu})},
          {id: 'remove', label: t('web.libraries.removeFolder'), icon: 'i-trash', destructive: true, onSelect: () => model.setRemovingFolder(folderMenu)},
        ] : []}/>
    </>}
  </>;
}
