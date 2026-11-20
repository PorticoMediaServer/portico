import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {ReadingGuestView} from '../../../kit/reading/ReadingGuestView';
import {ReaderSurface, ReaderLoading} from '../../../kit/reading/ReaderSurface';
import {useTheme} from '../../../kit/theme';
import {useReaderGuest, type ReaderGuestProps} from '../../shared/reading/useReaderGuest';
import {ReaderStatusPage} from './ReaderStatusPage';

export function ReaderGuestPage(props: ReaderGuestProps) {
  const {owner, active} = props, theme = useTheme(), t = defaultI18n.t;
  const {state, view, currentReader, onFailure, back, retryOriginal} = useReaderGuest(props);
  return <ReaderSurface active={active}>
    {state.url && state.reader && !state.failed ? <ReaderLoading ready>
      <ReadingGuestView url={state.url} view={view} reader={state.reader} currentReader={currentReader}
        backgroundColor={theme.colors.ink} observeBridge={() => {}} onFailure={onFailure}/>
    </ReaderLoading> : <ReaderStatusPage back={{testID: 'native-read-host-back', label: t('action.back'), onPress: back}}
      state={state.failed ? {title: t('next.states.pageFailed'), body: t('next.states.pageFailedBody'),
        primaryAction: {id: 'retry', label: t('next.states.tryAgain'), disabled: !active || !owner.current(), onPress: retryOriginal}} : undefined}>
      <ReaderLoading ready={false}/>
    </ReaderStatusPage>}
  </ReaderSurface>;
}
