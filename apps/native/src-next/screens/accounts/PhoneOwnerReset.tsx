import React, {useState} from 'react';
import {Text, View} from 'react-native';
import type {ResetFileLocation} from '../../../../../packages/client-core/src/account-reset';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {StateLayout} from '../../kit/states';
import {Button, TextField} from '../../kit/controls';
import {useTheme} from '../../kit/theme';
import {PhoneAccountForm} from './PhoneAccountForm';
import {ownerResetFilePaths} from './reset-file-paths';

export function PhoneOwnerReset({phase, serverName, location, code, onCodeChange, busy, error, onBack, onWrite, onVerify, onContinue}: {
  phase: 'intro' | 'code' | 'done'; serverName: string; location?: ResetFileLocation; code: string; onCodeChange: (value: string) => void;
  busy: boolean; error?: React.ReactNode; onBack: () => void; onWrite: () => void; onVerify: () => void; onContinue: () => void;
}) {
  const theme = useTheme(), t = defaultI18n.t, [showSystems, setShowSystems] = useState(false);
  const Layout = theme.platform === 'phone' ? PhoneAccountForm : StateLayout;
  const back = {id: 'back', label: t('action.back'), disabled: busy, onPress: onBack};
  if (phase === 'done') return <Layout testID="accounts-owner-reset-done" title={t('accounts.recovery.ownerSessionTitle')}
    body={t('accounts.recovery.ownerSessionBody')} beforeActions={<>{error}<Text style={[theme.type.body, {color: theme.colors.textSecondary, marginBottom: 18}]}>{t('accounts.recovery.ownerSessionHelp')}</Text></>}
    primaryAction={{id: 'open-server', label: t('accounts.recovery.continueServer', {server: serverName}), disabled: busy, loading: busy, onPress: onContinue}} secondaryAction={back}/>;
  if (phase === 'code') {
    const paths = ownerResetFilePaths(location);
    const systems = paths.alternatives.map(system => system.name);
    const disclosure = defaultI18n.list(systems, 'or');
    return <Layout testID="accounts-owner-reset-code" title={t('accounts.recovery.fileTitle')} body={t('accounts.recovery.fileBody')}
      beforeActions={<View style={{alignSelf: 'stretch', gap: 14, marginBottom: 18}}>{error}
        <View><Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('accounts.recovery.fileLocation')}{' (' + paths.system + ')'}</Text><Text selectable style={[theme.type.caption, {color: theme.colors.text}]}>{paths.path}</Text></View>
        <Button testID="accounts-owner-reset-systems" variant="text" icon={showSystems ? 'i-up' : 'i-down'} size="small"
          accessibilityState={{expanded: showSystems}} onPress={() => setShowSystems(value => !value)}>{disclosure}</Button>
        {showSystems && <View testID="accounts-owner-reset-other-paths" style={{gap: 12, paddingHorizontal: 14}}>
          {paths.alternatives.map(system => <View key={system.kind} style={{gap: 4}}>
            <Text style={[theme.type.captionStrong, {color: theme.colors.textSecondary}]}>{system.name}</Text>
            <Text selectable style={[theme.type.caption, {color: theme.colors.text}]}>{system.path}</Text>
            {'detail' in system && <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{'path' in system.detail ? t(system.detail.key, {path: system.detail.path}) : t(system.detail.key)}</Text>}
          </View>)}
        </View>}
        <TextField appearance={theme.platform === 'phone' ? 'auth' : undefined} label={t('accounts.recovery.code')} value={code} onChangeText={onCodeChange} editable={!busy} autoCapitalize="characters" autoCorrect={false} autoComplete="off"/>
      </View>}
      primaryAction={{id: 'verify-reset-code', label: t('action.continue'), disabled: busy || !code.trim(), loading: busy, onPress: onVerify}} secondaryAction={back}/>;
  }
  return <Layout testID="accounts-owner-reset-intro" title={t('accounts.signIn.cannotUsePortico')} body={t('accounts.recovery.porticoBody')}
    beforeActions={<View style={{alignSelf: 'stretch', gap: 14, marginBottom: 18}}>{error}<Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{serverName}</Text>
      <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('accounts.recovery.fileHelp')}</Text>
      <View style={{gap: 4}}><Text style={[theme.type.body, {color: theme.colors.text}]}>{t('accounts.recovery.notOwner')}</Text>
        <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('accounts.recovery.notOwnerBody')}</Text></View>
    </View>}
    primaryAction={{id: 'write-reset-file', label: t('accounts.recovery.writeFile'), disabled: busy, loading: busy, onPress: onWrite}} secondaryAction={back}/>;
}
