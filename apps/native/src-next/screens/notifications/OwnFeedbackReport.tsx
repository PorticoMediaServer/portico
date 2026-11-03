import React, {useEffect, useState} from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {ownFeedbackReportView, readOwnFeedbackReport} from '../../../../../packages/client-core/src/notification-actions';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';
import {BottomSheet} from '../../kit/overlays';
import {useTheme} from '../../kit/theme';

type Api = Parameters<typeof readOwnFeedbackReport>[0];
type Loaded = Readonly<{phase: 'loading'}> | Readonly<{phase: 'ready'; view: ReturnType<typeof ownFeedbackReportView>}> | Readonly<{phase: 'failed'; error: unknown}>;

/** "Your report has an update" → Open report, read only, as on web: no frame draws a reporter's editor. */
export function OwnFeedbackReport({api, reportId, onClose}: Readonly<{api: Api; reportId: string; onClose: () => void}>) {
  const theme = useTheme(), [loaded, setLoaded] = useState<Loaded>({phase: 'loading'});
  useEffect(() => {
    const controller = new AbortController();
    setLoaded({phase: 'loading'});
    void readOwnFeedbackReport(api, reportId, controller.signal).then(
      report => {if (!controller.signal.aborted) setLoaded({phase: 'ready', view: ownFeedbackReportView(report, defaultI18n)});},
      error => {if (!controller.signal.aborted) setLoaded({phase: 'failed', error});});
    return () => controller.abort();
  }, [api, reportId]);
  const failed = loaded.phase === 'failed' ? presentError(loaded.error, 'feedback') : undefined;
  const title = loaded.phase === 'ready' ? loaded.view.title : failed && !failed.silent ? failed.title : defaultI18n.t('status.loading');
  const caption = [theme.type.caption, {color: theme.colors.textTertiary}];
  return <BottomSheet testID="own-feedback-report" title={title} accessibilityLabel={title} open onOpenChange={open => {if (!open) onClose();}}>
    {loaded.phase === 'loading' && <Text accessibilityRole="progressbar" style={caption}>{defaultI18n.t('status.loading')}</Text>}
    {failed && !failed.silent && <Text accessibilityRole="alert" style={[theme.type.body, {color: theme.colors.danger}]}>{failed.body}</Text>}
    {loaded.phase === 'ready' && <View style={{gap: 12}}>
      <Text style={caption}>{loaded.view.meta}</Text>
      <Text style={[theme.type.body, {color: theme.colors.text}]}>{loaded.view.message}</Text>
      {loaded.view.replies.map((reply, index) => <View key={index} style={{padding: 12, borderRadius: 12, backgroundColor: theme.colors.raised, gap: 6}}>
        <Text style={[theme.type.body, {color: theme.colors.text}]}>{reply.text}</Text>
        <Text style={caption}>{reply.at}</Text>
      </View>)}
    </View>}
  </BottomSheet>;
}
