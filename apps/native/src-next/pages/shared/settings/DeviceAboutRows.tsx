import React, {useRef} from 'react';
import {Button} from '../../../kit/controls';
import {AboutNote, PanelBlock} from '../../../kit/frames';
import {Banner, Dialog} from '../../../kit/overlays';
import {OverlayFootRow} from '../../../kit/overlays/surface';
import {SettingsRow} from '../../../kit/shell';
import {StateView} from '../../../kit/states';
import {useTheme} from '../../../kit/theme';
import {BUILD_ID} from '../../../platform/build-id';
import {deviceHelpAddress, useDeviceAbout, useDeviceReport} from './useDeviceAbout';

export function DeviceAboutRows(props: Parameters<typeof useDeviceAbout>[0]) {
  const {context, t, runtime} = props;
  const about = useDeviceAbout(props), report = useDeviceReport(props);
  const {isTV} = useTheme(), cancel = useRef<React.ComponentRef<typeof Button>>(null);
  return <>
    <PanelBlock><AboutNote text={t('about.legal')} source={t('about.datasetCredit')}/></PanelBlock>
    <SettingsRow first testID="settings-app-version" label={t('about.appVersion')} value={context.appVersion}/>
    <SettingsRow testID="settings-app-build" label={t('web.about.build')} value={BUILD_ID}/>
    <SettingsRow testID="settings-server-name" label={t('about.server')} value={context.serverName}/>
    {about.serverVersion !== undefined && <SettingsRow testID="settings-server-version" label={t('about.serverVersion')} value={about.serverVersion}/>}
    {about.serverApi !== undefined && <SettingsRow testID="settings-server-api" label={t('web.about.serverApi')} value={about.serverApi}/>}
    {about.versionError !== undefined && <StateView variant="inline" {...runtime.error(about.versionError, about.retry)}/>}
    <SettingsRow testID="settings-open-licenses" label={t('licenses.title')} value={t('next.settingsSpec.account.device.groups.5.rows.3.control.label')} onPress={about.openLicences}/>
    {!isTV && <SettingsRow testID="settings-device-help" label={t('next.settingsSpec.account.device.groups.5.rows.4.label')} value={deviceHelpAddress} onPress={about.help}/>}
    <SettingsRow testID="settings-send-app-log" label={t('next.settingsSpec.account.device.groups.5.rows.5.label')}
      description={t('next.settingsSpec.account.device.groups.5.rows.5.help')} onPress={report.open}/>
    {report.sent && <Banner testID="settings-send-app-log-receipt" variant="notice" placement="inline" tone="success" message={t('about.report.sent', {server: context.serverName})}/>}
    <Dialog testID="settings-send-app-log-confirm" open={report.active} initialFocusRef={cancel} onOpenChange={next => {if (!next) report.close();}}
      title={t('about.report.confirmTitle', {server: context.serverName})} description={t('about.report.confirmBody')}
      footer={<OverlayFootRow layout="end">
        <Button ref={cancel} testID="settings-send-app-log-cancel" variant="glass" disabled={report.busy} onPress={report.close}>{t('action.cancel')}</Button>
        <Button testID="settings-send-app-log-submit" variant="glass" loading={report.busy} onPress={report.send}>{t('about.report.send')}</Button>
      </OverlayFootRow>}>
      {report.error !== undefined && <StateView variant="inline" {...runtime.error(report.error, report.send)}/>}
    </Dialog>
  </>;
}
