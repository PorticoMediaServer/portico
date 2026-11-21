import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button, TextField} from '../../../kit/controls';
import {EnrolmentCode, PasswordMeter, SecuritySheet, SecurityText} from '../../../kit/settings/SecurityParts';
import {SettingsGroup, SettingsRow} from '../../../kit/shell';
import {StateView} from '../../../kit/states';
import {useTheme} from '../../../kit/theme';
import {NativeHostedSignInPanel} from '../../../screens/settings-live/NativeHostedSignInPanel';
import {useLocalAccountSecurity, type LocalAccountSecurityProps} from './useLocalAccountSecurity';

export function AccountSecurityPanel(props: LocalAccountSecurityProps) {
  const {kind, t, runtime} = props;
  const theme = useTheme();
  const model = useLocalAccountSecurity(props);
  const {allowed, hosted, showing, label, busy, error, notice, factor, enrolment, codes, copied, mode,
    password, nextPassword, confirmation, code, strength, matching, canSave, canVerify, devices, device, deviceName} = model;
  if (!allowed || theme.isTV && kind !== 'devices') return null;
  if (kind !== 'devices' && hosted) return <NativeHostedSignInPanel kind={kind} context={props.context} lifetime={props.lifetime} t={t} runtime={runtime} openRoute={props.openRoute}/>;
  const passwordField = <TextField testID="account-security-current-password" label={kind === 'password' ? t('account.currentPassword') : t('auth.password')} value={password} onChangeText={model.setPassword} secureTextEntry autoCapitalize="none" autoCorrect={false} autoComplete="current-password" textContentType="password" editable={!busy}/>;
  const codeField = <TextField testID="account-security-code" label={t('auth.verificationCode')} description={enrolment ? undefined : t('web.twoStep.orRecoveryCode')} value={code} onChangeText={model.setCode} autoCapitalize="none" autoCorrect={false} autoComplete="one-time-code" textContentType="oneTimeCode" editable={!busy}/>;
  let title = label, description: string | undefined, body: React.ReactNode = null, actions: React.ReactNode = null;
  if (codes) {
    title = t('confirm.recoveryCodes.title'); description = t('confirm.recoveryCodes.body');
    body = <SecurityText testID="account-security-recovery-codes" secret>{codes.join('\n')}</SecurityText>;
    actions = <><Button testID="account-security-copy-codes" variant="glass" onPress={model.copyCodes}>{t('web.twoStep.copy')}</Button><Button testID="account-security-codes-saved" onPress={model.codesSaved}>{t('confirm.recoveryCodes.action')}</Button></>;
  } else if (kind === 'password' && factor) {
    title = t('web.reset.submit');
    body = <>{passwordField}
      <TextField testID="account-security-new-password" label={t('accounts.settings.changeOwnerLocalPassword')} value={nextPassword} onChangeText={model.setNextPassword} secureTextEntry autoCapitalize="none" autoCorrect={false} autoComplete="new-password" textContentType="newPassword" editable={!busy}/>
      {nextPassword && <PasswordMeter {...strength}/>}
      <TextField testID="account-security-confirm-password" label={t('accounts.signIn.confirmPassword')} value={confirmation} onChangeText={model.setConfirmation} secureTextEntry autoCapitalize="none" autoCorrect={false} autoComplete="new-password" textContentType="newPassword" editable={!busy} error={!matching ? t('account.passwordMismatch') : undefined}/>
      {factor.enabled && codeField}</>;
    actions = <Button testID="account-security-save-password" loading={busy} disabled={busy || !canSave} onPress={model.savePassword}>{t('action.save')}</Button>;
  } else if (kind === 'twoStep' && factor) {
    if (mode === 'confirm-disable') {
      title = t('web.twoStep.turnOffConfirmTitle'); description = t('security.twoStep.turnOffBody');
      actions = <Button testID="account-security-confirm-disable" variant="danger" loading={busy} disabled={busy} onPress={model.disable}>{t('web.hostedSecurity.turnOff')}</Button>;
    } else if (mode === 'disable' || mode === 'replace') {
      title = mode === 'disable' ? t('web.twoStep.turnOffTitle') : t('web.twoStep.replaceCodes');
      description = mode === 'replace' ? t('web.twoStep.replaceCodesBody') : t('web.twoStep.confirmPasswordShort');
      body = <>{passwordField}{codeField}</>;
      actions = <Button testID="account-security-factor-submit" loading={busy} disabled={busy || !password || !code.trim()} onPress={model.submitFactor}>
        {mode === 'disable' ? t('action.continue') : t('web.twoStep.replaceCodesAction')}
      </Button>;
    } else if (enrolment) {
      title = t('web.twoStep.scanTitle'); description = t('web.twoStep.scanBody');
      body = <><EnrolmentCode uri={enrolment.uri} secret={enrolment.secret} help={t('web.twoStep.cantScan')}/>{codeField}</>;
      actions = <Button testID="account-security-verify" loading={busy} disabled={busy || !canVerify} onPress={model.verifyFactor}>{t('security.twoStep.verify')}</Button>;
    } else if (!factor.enabled) {
      title = t('web.twoStep.turnOnTitle'); description = t('security.twoStep.confirmPassword'); body = passwordField;
      actions = <Button testID="account-security-enrol" loading={busy} disabled={busy || !password} onPress={model.enrol}>{t('action.continue')}</Button>;
    } else {
      body = <><SecurityText>{t('web.hostedSecurity.on')}</SecurityText><SecurityText>{t('web.hostedSecurity.codesLeft', {count: factor.recoveryCodesRemaining})}</SecurityText></>;
      actions = <><Button testID="account-security-disable" variant="glass" onPress={() => model.go('disable')}>{t('web.hostedSecurity.turnOff')}</Button><Button testID="account-security-replace-codes" variant="glass" onPress={() => model.go('replace')}>{t('web.twoStep.replaceCodes')}</Button></>;
    }
  } else if (kind === 'devices' && devices) {
    if (mode === 'confirm-all') {
      title = t('account.signOutEverywhere.title'); description = t('web.devices.everywhereBody');
      actions = <Button testID="account-security-confirm-all" variant="danger" loading={busy} disabled={busy} onPress={model.signOutAll}>{t('account.signOutEverywhere.action')}</Button>;
    } else if (device) {
      title = mode === 'confirm-remove' ? t('confirm.removeDevice.title', {device: device.name}) : device.name;
      description = mode === 'confirm-remove' ? t('confirm.removeDevice.body') : t('web.devices.firstSeen', {date: defaultI18n.date(device.firstSeen)});
      if (mode === 'confirm-remove') actions = <Button testID="account-security-confirm-remove" variant="danger" loading={busy} disabled={busy} onPress={model.removeDevice}>{t('web.devices.removeAction')}</Button>;
      else {
        body = <TextField testID="account-security-device-name" label={t('profile.name')} value={deviceName} onChangeText={model.setDeviceName} maxLength={120} editable={!busy}/>;
        actions = <><Button testID="account-security-sign-out-device" variant="glass" disabled={busy} onPress={model.signOutDevice}>{device.current ? t('device.signOutThis') : t('device.signOutOtherAction')}</Button>
          {!device.current && <Button testID="account-security-remove-device" variant="danger" disabled={busy} onPress={() => model.go('confirm-remove')}>{t('web.devices.removeAction')}</Button>}
          <Button testID="account-security-save-device" loading={busy} disabled={busy || !deviceName.trim() || deviceName.trim() === device.name}
            onPress={model.renameDevice}>{t('web.devices.saveName')}</Button></>;
      }
    } else {
      body = devices.length ? <SettingsGroup>{devices.map((row, index) => <SettingsRow key={row.id} first={index === 0} testID={`account-security-device-${row.id}`} label={row.name}
        value={row.current ? t('settings.thisDevice') : undefined} description={[row.platform, row.app, defaultI18n.date(row.lastSeen), row.sessions ? t('web.devices.sessions', {count: row.sessions}) : 'Signed out'].filter(Boolean).join(' · ')}
        onPress={() => model.pickDevice(row)}/>)}</SettingsGroup> : <SecurityText>{t('web.devices.noneSignedIn')}</SecurityText>;
      actions = <Button testID="account-security-sign-out-all" variant="glass" disabled={busy} onPress={() => model.go('confirm-all')}>{t('next.settingsSpec.server.people.panels.person.tabs.3.groups.0.rows.1.control.label')}</Button>;
    }
  }
  return <>
    <SettingsRow first testID={`settings-panel-${kind}`} label={label} onPress={model.open}/>
    {notice && <SecurityText announce>{notice}</SecurityText>}
    <SecuritySheet id={`account-security-${kind}`} open={showing} onClose={model.dismiss} title={title} description={description} locked={!!codes}
      actions={<>{!codes && <Button testID="account-security-cancel" variant="glass" disabled={busy} onPress={model.cancel}>{t(device ? 'action.back' : 'action.cancel')}</Button>}{actions}</>}>
      {error !== undefined && <StateView testID="account-security-error" {...runtime.error(error, model.clearError, 'account')}/>}
      {busy && !factor && !devices && <SecurityText>{t('status.loading')}</SecurityText>}
      {!busy && error !== undefined && !factor && !devices && <Button testID="account-security-retry" onPress={model.retry}>{t('action.tryAgain')}</Button>}
      {body}
      {copied && <SecurityText>{t('api.credential.copied')}</SecurityText>}
    </SecuritySheet>
  </>;
}
