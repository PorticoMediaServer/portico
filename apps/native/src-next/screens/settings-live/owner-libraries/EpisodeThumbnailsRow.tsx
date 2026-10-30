import React, {useEffect, useState, useSyncExternalStore} from 'react';
import type {LibraryContentApi} from '../../../../../../packages/client-core/src/library-content';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {settingsRowIndex} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import type {defaultI18n} from '../../../../../../packages/i18n/src';
import {Select} from '../../../kit/controls/Select';
import {LibraryGroup, LibraryRow} from './controls';
import {episodeThumbnailChange, episodeThumbnailLibrary} from '../server/metadata-setting-values';
import {NativeMetadataController} from '../server/metadata-controller';

/** Episode thumbnails have a separate producer with both server and library revisions. */
export function EpisodeThumbnailsRow({api, libraryId, parentCurrent, disabled, t}: {
  api: LibraryContentApi; libraryId: string; parentCurrent: () => boolean; disabled: boolean; t: typeof defaultI18n.t;
}) {
  const [owned, setOwned] = useState<{controller: NativeMetadataController; api: LibraryContentApi; libraryId: string; parentCurrent: () => boolean}>();
  useEffect(() => {
    const controller = new NativeMetadataController(api, parentCurrent); setOwned({controller, api, libraryId, parentCurrent}); void controller.load();
    return () => controller.dispose();
  }, [api, libraryId, parentCurrent]);
  return owned && owned.api === api && owned.libraryId === libraryId && owned.parentCurrent === parentCurrent
    ? <EpisodeRow controller={owned.controller} libraryId={libraryId} disabled={disabled} t={t}/> : null;
}

function EpisodeRow({controller, libraryId, disabled, t}: {controller: NativeMetadataController; libraryId: string; disabled: boolean; t: typeof defaultI18n.t}) {
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot), settings = state.data;
  const row = settingsRowIndex.get('libraries.episodeThumbs')!.row;
  const library = settings ? episodeThumbnailLibrary(settings, libraryId) : undefined;
  if (settings && !library && state.error === undefined) return null;
  return <LibraryGroup style={{marginTop: 12}}><LibraryRow first label={t(row.label)} help={state.error === undefined ? row.help && t(row.help) : presentError(state.error, 'library').body}
    trailing={library ? <Select testID="libraries.episodeThumbs" accessibilityLabel={t(row.label)} disabled={disabled || state.busy || state.needsReload} value={library.episodeThumbnailMode}
      options={(row.control.options ?? []).map(option => ({value: String(option.value), label: t(option.label)}))} onValueChange={value => {
        if (!settings || disabled) return;
        const change = episodeThumbnailChange(settings, libraryId, value); if (change) void controller.save(settings, {libraries: change.libraries});
      }}/>
      : undefined}/>
    {state.error !== undefined && <LibraryRow label={t('action.tryAgain')} onPress={state.busy ? undefined : () => {void controller.load();}}/>}
  </LibraryGroup>;
}
