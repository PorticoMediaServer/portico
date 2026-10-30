import React, {useLayoutEffect, useState, useSyncExternalStore} from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../packages/i18n/src';
import {canResetSecureRecords, resetSecureRecords, restartApp} from '../platform/secure-records';
import {StateLayout} from '../kit/states';
import {Button} from '../kit/controls';
import {Dialog} from '../kit/overlays';
import {useTheme} from '../kit/theme';
import {createSecureRecordsRecovery, secureRecordsRecoveryIdle} from './secure-records-recovery';

type OriginalRoot = Readonly<{current?: () => boolean; recoveryCurrent?: () => boolean; restart?: () => void}>;
const noSubscription = () => () => {};
const idleSnapshot = () => secureRecordsRecoveryIdle;

export function SecureRecordsRecovery({opening}: {opening: OriginalRoot}) {
  const theme = useTheme(), t = defaultI18n.t;
  const [recovery, setRecovery] = useState<ReturnType<typeof createSecureRecordsRecovery>>();
  useLayoutEffect(() => {
    const owned = createSecureRecordsRecovery({current: () => opening.current?.() === true,
      ownsRoot: () => opening.recoveryCurrent?.() === true, retry: () => opening.restart?.(), reset: resetSecureRecords, restart: restartApp});
    setRecovery(owned);
    return () => owned.dispose();
  }, [opening]);
  const state = useSyncExternalStore(recovery?.subscribe ?? noSubscription, recovery?.getSnapshot ?? idleSnapshot);
  const busy = state.phase === 'working', reopen = state.phase === 'reopen';
  return <>
    <StateLayout testID="native-secure-records-recovery" title={t('signIn.reset.title')} body={t('signIn.reset.body')} tone="warning"
      beforeActions={busy || reopen ? <Text accessibilityLiveRegion="polite" style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t(reopen ? 'signIn.reset.reopen' : 'signIn.reset.working')}</Text> : undefined}
      primaryAction={!busy && !reopen && canResetSecureRecords() ? {id: 'reset', label: t('signIn.reset.action'), disabled: !recovery, onPress: () => recovery?.open(), hasTVPreferredFocus: theme.isTV} : undefined}
      secondaryAction={!busy && !reopen ? {id: 'retry', label: t('action.tryAgain'), disabled: !recovery, onPress: () => recovery?.retry()} : undefined}/>
    <Dialog open={state.phase === 'confirming' || busy} testID="native-secure-records-confirm" title={t('signIn.reset.confirmTitle')}
      description={t('signIn.reset.consequence')} onOpenChange={open => {if (!open) recovery?.cancel();}}
      dismissOnBack={!busy} dismissOnOutsidePress={!busy} dismissOnGesture={!busy} showClose={!busy}
      footer={<View style={{flexDirection: 'row', gap: 12}}>
        <Button testID="secure-records-cancel" variant="text" disabled={busy} onPress={() => recovery?.cancel()}>{t('action.cancel')}</Button>
        <Button testID="secure-records-reset-confirm" disabled={busy || !recovery} loading={busy} textStyle={{color: theme.colors.danger}}
          onPress={() => {void recovery?.confirm();}}>{t('signIn.reset.confirm')}</Button>
      </View>}>
      {state.failed ? <Text accessibilityRole="alert" style={[theme.type.body, {color: theme.colors.danger}]}>{t('signIn.reset.failed')}</Text> : null}
      {busy ? <Text accessibilityLiveRegion="polite" style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('signIn.reset.working')}</Text> : null}
    </Dialog>
  </>;
}
