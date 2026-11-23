import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {ReadingGuestView} from '../../../kit/reading/ReadingGuestView';
import {ReaderFrame} from '../../../kit/frames/ReaderFrame';
import {useTheme} from '../../../kit/theme';
import {useReaderGuest, type ReaderGuestProps} from '../../shared/reading/useReaderGuest';

export function ReaderGuestPage(props: ReaderGuestProps) {
  const {owner, active} = props, theme = useTheme(), t = defaultI18n.t;
  const {state, view, currentReader, onFailure, back, retryOriginal, safeTop} = useReaderGuest(props);
  if (state.url && state.reader && !state.failed) return <ReaderFrame active={active}>
    <ReadingGuestView url={state.url} view={view} reader={state.reader} currentReader={currentReader}
      backgroundColor={theme.colors.ink} observeBridge={() => {}} onFailure={onFailure} safeTop={safeTop}/>
  </ReaderFrame>;
  return <ReaderFrame active={active} back={{testID: 'native-read-host-back', label: t('action.back'), onPress: back}}
    state={state.failed ? {title: t('next.states.pageFailed'), body: t('next.states.pageFailedBody'),
      primaryAction: {id: 'retry', label: t('next.states.tryAgain'), disabled: !active || !owner.current(), onPress: retryOriginal}} : undefined}/>;
}
