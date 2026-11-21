import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {SavedNameSheet} from '../../../kit/saved/SavedForms';
import {SavedTVDirectoryBody} from '../../../kit/saved/SavedTVDirectoryParts';
import {useTVSavedDirectory, type NativeSavedTVDirectoryProps} from '../../shared/saved/useTVSavedDirectory';

export function SavedTVDirectory(props: NativeSavedTVDirectoryProps) {
  const {view, regionId} = props, {t} = defaultI18n;
  const model = useTVSavedDirectory(props);
  const {creating, personal, busy, draft, problem, returnFocusRef, closeCreate, create} = model;
  return <>
    <SavedTVDirectoryBody {...props} model={model}/>
    <SavedNameSheet open={creating} title={t(view === 'playlists' ? 'web.saved.newPlaylist' : 'web.saved.newCollection')}
      action={t(personal?.retryPending ? 'action.tryAgain' : 'web.saved.create')} withSummary withVisibility={view === 'collections'} busy={busy}
      locked={personal?.retryPending} initialName={draft?.name} initialSummary={draft?.summary} initialVisibility={draft?.visibility}
      error={problem} returnFocusRef={returnFocusRef} testID={`${regionId}.create`} onClose={closeCreate} onSave={create}/>
  </>;
}
