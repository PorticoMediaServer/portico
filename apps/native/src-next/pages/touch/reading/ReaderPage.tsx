import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {useNativeStackPlacement} from '../../../kit/navigation/stack-slot';
import {ReaderSurface, ReaderLoading} from '../../../kit/reading/ReaderSurface';
import {useReaderSession, type ReaderPageProps} from '../../shared/reading/useReaderSession';
import {ReaderGuestPage} from './ReaderGuestPage';
import {ReaderStatusPage} from './ReaderStatusPage';

export function ReaderPage(props: ReaderPageProps) {
  const {context, target, active, preferences, onBack, onOpenSettings, resident = true, binding, onGuestState} = props;
  const place = useNativeStackPlacement(target), t = defaultI18n.t;
  const {owner, snapshot, failed, busy, ended, tooLarge, retryOriginal, reopenEnded, back} = useReaderSession(props);
  if (!resident) return null;
  if (snapshot.phase === 'ready') return place(<ReaderGuestPage owner={owner} active={active} preferences={preferences}
    onBack={onBack} onSettings={onOpenSettings} contentsTarget={target.contentsTarget} onGuestState={onGuestState} activeCurrent={binding?.activeCurrent}/>);
  return place(<ReaderSurface active={active}>
    <ReaderStatusPage back={{testID: 'native-read-host-back', label: t('action.back'), onPress: back}}
      state={ended ? {title: t('next.reader.sessionEndedTitle'), body: t('next.reader.sessionEndedBody'),
        primaryAction: {id: 'reopen', label: t('next.reader.openAgain'), disabled: !active || !context.current(), onPress: reopenEnded}}
        : tooLarge ? {title: t('next.reader.publicationTooLargeTitle'), body: t('next.reader.publicationTooLargeBody')}
        : failed ? {title: t('next.states.pageFailed'), body: t(busy ? 'error.busy' : 'next.states.pageFailedBody'),
          primaryAction: {id: 'retry', label: t('next.states.tryAgain'), disabled: !active || snapshot.phase === 'retired' || !!snapshot.session || !context.current(), onPress: retryOriginal}}
          : undefined}>
      <ReaderLoading ready={false}/>
    </ReaderStatusPage>
  </ReaderSurface>);
}
