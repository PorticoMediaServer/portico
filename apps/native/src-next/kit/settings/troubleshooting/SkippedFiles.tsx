import React from 'react';
import {Text, View} from 'react-native';
import {useSkippedFiles} from '../../../pages/shared/settings/useTroubleshootingSkippedFiles';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {Button, FormError} from '../../controls';
import {PanelBlock, SectionCard} from '../../frames';
import {SettingsRow} from '../../shell';
import {useTheme} from '../../theme';
import {SettingsHelp} from '../SettingsParts';

export function NativeSkippedFiles({ports, runtime}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime}) {
  const {read, next} = useSkippedFiles(ports), theme = useTheme(), {t} = runtime.text;
  return <View testID="server-troubleshooting-skipped">
    <SectionCard label={t('next.settings.troubleshootingStuckFiles')} help={t('next.settings.troubleshootingStuckFilesHelp')}>
      {read.failed && <PanelBlock><FormError placement="inline">{t('error.unavailable')}</FormError></PanelBlock>}
      {read.loading && !read.items.length && <SettingsRow first label={t('next.settings.loading')}/>}
      {!read.loading && !read.failed && !read.items.length && <SettingsRow first label={t('next.settings.stuckFilesNone')}/>}
      {read.items.map((file, index) => <PanelBlock key={`${file.libraryId}:${file.path}:${index}`}>
        <Text selectable style={[theme.type.rowLabel, {color: theme.colors.text}]}>{file.path}</Text>
        {file.libraryName && <SettingsHelp>{file.libraryName}</SettingsHelp>}
        <SettingsHelp>{file.message}</SettingsHelp>
      </PanelBlock>)}
      {read.next && <PanelBlock><Button size="small" variant="glass" disabled={read.loading} onPress={next}>{t('server.showMore')}</Button></PanelBlock>}
    </SectionCard>
  </View>;
}
