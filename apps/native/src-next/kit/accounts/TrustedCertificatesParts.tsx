import React, {type ReactNode} from 'react';
import {Text, View} from 'react-native';
import {TVAccountRow} from '../controls/TVAccountRow';
import {Button, FormError} from '../controls';
import {Dialog} from '../overlays';
import {useTheme} from '../theme';

/** The trusted certificates group: its heading, an error, the rows (each with its own Forget) and Refresh. */
export function TrustedCertificatesGroup({title, error, children, after, refreshLabel, busy, onRefresh, refreshRef, refreshDown}: {
  title: string; error?: string; children: ReactNode; /** Drawn after Refresh (the Forget dialog). */ after?: ReactNode; refreshLabel: string; busy: boolean; onRefresh: () => void;
  refreshRef?: React.Ref<View>; refreshDown?: number;
}) {
  const theme = useTheme();
  return <View testID="tv-trusted-certificates" style={{gap: 12}}>
    <Text accessibilityRole="header" style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{title}</Text>
    {error !== undefined && <FormError placement="inline">{error}</FormError>}
    {children}
    <Button ref={refreshRef} focusId="certificates-refresh" variant="link" disabled={busy} nextFocusDown={refreshDown} onPress={onRefresh}>{refreshLabel}</Button>
    {after}
  </View>;
}

/** One trusted certificate: its address and fingerprint, and Forget. */
export function TrustedCertificateRow({address, fingerprint, forgetLabel, busy, onForget}: {address: string; fingerprint: string; forgetLabel: string; busy: boolean; onForget: () => void}) {
  return <TVAccountRow title={address} detail={fingerprint} icon="i-lock">
    <Button focusId={`certificate:${address}`} variant="glass" size="small" disabled={busy} onPress={onForget}>{forgetLabel}</Button>
  </TVAccountRow>;
}

/** Forget asks first: what forgetting means for this address, and its fingerprint. Cancel starts focused. */
export function ForgetCertificateDialog({open, title, body, fingerprintLabel, fingerprint, cancelLabel, forgetLabel, busy, cancelRef, onClose, onConfirm}: {
  open: boolean; title: string; body: string; fingerprintLabel: string; fingerprint?: string; cancelLabel: string; forgetLabel: string; busy: boolean;
  cancelRef: React.RefObject<View | null>; onClose: () => void; onConfirm: () => void;
}) {
  const theme = useTheme();
  return <Dialog testID="tv-trusted-certificate-forget" open={open} onOpenChange={next => {if (!next) onClose();}} title={title}
    initialFocusRef={cancelRef} dismissOnOutsidePress={!busy} dismissOnBack={!busy} showClose={!busy}
    footer={<View style={{gap: 12}}>
      <Button focusId="cancel" ref={cancelRef} variant="glass" disabled={busy} onPress={onClose}>{cancelLabel}</Button>
      <Button focusId="forget" variant="text" disabled={busy} textStyle={{color: theme.colors.danger}} onPress={onConfirm}>{forgetLabel}</Button>
    </View>}>
    <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{body}</Text>
    <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{fingerprintLabel}</Text>
    <Text selectable style={[theme.type.body, {color: theme.colors.text}]}>{fingerprint}</Text>
  </Dialog>;
}
