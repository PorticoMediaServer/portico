import React, {useEffect, useState} from 'react';
import {Text, View} from 'react-native';
import {defaultI18n, type MessageId} from '../../../../../packages/i18n/src/index';
import {actionNotice, type UserAction, type UserActionSnapshot} from '../../../../../packages/client-core/src/user-action';
import {Button} from '../controls/Button';
import {Icon} from '../Icon';
import {alpha} from '../overlays/surface';
import {nativeType, useTheme} from '../theme';

export type ActionNoticeProps<T, A extends readonly unknown[]> = Readonly<{
  snapshot: UserActionSnapshot<T>;
  action: Pick<UserAction<T, A>, 'retry'>;
  /** The screen's drawn line, when its mock-up has one. */
  title?: string;
  body?: string;
  testID?: string;
}>;

/** The kit's inline failure for a user action (collections.html 24: .phone .nbox, .tv .notice2):
 *  shown for failed and unconfirmed, nothing otherwise. Try again keeps the action's intent, and
 *  waits out a server's Retry-After before it can be pressed. */
export function ActionNotice<T, A extends readonly unknown[]>({snapshot, action, title, body, testID}: ActionNoticeProps<T, A>) {
  const theme = useTheme();
  const notice = actionNotice(snapshot), waitMs = notice?.retryAfterMs ?? 0;
  // Try again is held from the first frame until this snapshot's wait has passed.
  const [released, setReleased] = useState<unknown>(undefined);
  const waiting = waitMs > 0 && released !== snapshot;
  useEffect(() => {
    if (!waitMs) return;
    const timer = setTimeout(() => setReleased(snapshot), waitMs);
    return () => clearTimeout(timer);
  }, [snapshot, waitMs]);
  if (!notice) return null;
  const t = (id: MessageId) => defaultI18n.t(id);
  const line = title ?? t(notice.message), retryable = notice.retryable;
  const tv = theme.isTV, phone = theme.layoutMode === 'phone';
  const size = tv ? 22 : phone ? 13.5 : 14, titleSize = tv ? 24 : phone ? 14.5 : 15;
  return <View testID={testID} accessibilityRole="alert" accessibilityLiveRegion="assertive" style={{flexDirection: 'row', gap: tv ? 16 : phone ? 10 : 12, paddingVertical: tv ? 18 : phone ? 12 : 14, paddingHorizontal: tv ? 20 : phone ? 14 : 16, borderRadius: tv ? 14 : 12, backgroundColor: alpha(theme.colors.danger, 0.1), borderWidth: tv ? 1.5 : 1, borderColor: alpha(theme.colors.danger, tv ? 0.4 : 0.35)}}>
    <View style={{marginTop: tv ? 4 : 2}}><Icon name="i-warn" color={theme.colors.danger} size={tv ? 30 : 20}/></View>
    <View style={{flexShrink: 1}}>
      <Text style={[nativeType({size: titleSize, lineHeight: titleSize * 1.45, weight: 700, tracking: 0}, theme.textScale), {color: theme.colors.text, marginBottom: tv ? 4 : 2}]}>{line}</Text>
      {body ? <Text style={[nativeType({size, lineHeight: size * (tv ? 1.45 : 1.5), weight: 400, tracking: 0}, theme.textScale), {color: theme.colors.textSecondary}]}>{body}</Text> : null}
      {retryable ? <View style={{flexDirection: 'row', gap: 8, marginTop: tv ? 14 : 10}}>
        <Button variant="primary" size={tv ? 'small' : 'xs'} disabled={waiting} onPress={() => action.retry()}>{t('next.states.tryAgain')}</Button>
      </View> : null}
    </View>
  </View>;
}
