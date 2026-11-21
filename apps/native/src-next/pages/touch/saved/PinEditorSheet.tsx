import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {StateRow} from '../../../kit/frames';
import {Banner, BottomSheet} from '../../../kit/overlays';
import {PinGroup} from '../../../kit/saved/PinGroup';
import {SettingsHelp} from '../../../kit/settings/SettingsParts';
import {usePinEditor, type PinEditorProps} from '../../shared/saved/usePinEditor';

const {t} = defaultI18n;

export function PinEditorSheet(props: PinEditorProps) {
  const pins = usePinEditor(props);
  return <BottomSheet open testID="native-pin-editor" title={t('pins.editTitle')} description={t('pins.librariesHint')} showClose={false}
    dismissOnBack={!pins.busy} dismissOnOutsidePress={!pins.busy && !pins.dragging} dismissOnGesture={!pins.busy && !pins.dragging}
    onOpenChange={pins.close}>
    {pins.error !== undefined && <Banner testID="pins-error" variant="notice" tone="error" placement="inline" message={pins.error}/>}
    <PinGroup id="pin-libraries" title={t('pins.libraries')} rows={pins.libraryRows} busy={pins.busy || !pins.hasLibraries}
      onDragging={pins.setDragging} place={pins.placeLibrary} empty={pins.loading ? <StateRow kind="loading"/> : undefined}/>
    <PinGroup id="pin-resources" title={t('pins.saved')} rows={pins.resourceRows} busy={pins.busy}
      onDragging={pins.setDragging} place={pins.placeResource} empty={pins.ready ? <SettingsHelp>{t('pins.noneSaved')}</SettingsHelp> : undefined}/>
  </BottomSheet>;
}
