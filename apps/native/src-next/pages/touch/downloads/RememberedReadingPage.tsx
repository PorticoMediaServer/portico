import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button} from '../../../kit/controls';
import {ReaderFrame} from '../../../kit/frames/ReaderFrame';
import {Dialog} from '../../../kit/overlays/surface';
import {useRememberedReading, type RememberedReadingProps} from '../../shared/downloads/useRememberedReading';
import {ReaderGuestPage} from '../reading/ReaderGuestPage';

const t = defaultI18n.t;

/** A downloaded book or comic read while the phone can't reach its server: the reader as online, with reading settings
 * answered by a notice that they aren't available here; while it opens, the reader's loading picture with Back; if it
 * can't open, the reader's failed state with Back. */
export function RememberedReadingPage(props: RememberedReadingProps) {
  const {reading, noticeCopy} = props, view = useRememberedReading(props), notice = view.notice;
  if (view.snapshot.phase === 'ready') return <>
    <ReaderGuestPage owner={reading.owner} active={!notice} preferences={reading.preferences} onBack={view.back} onSettings={view.openNotice}/>
    {notice ? <Dialog open onOpenChange={open => {if (!open) view.closeNotice(notice);}} title={noticeCopy.title}
      description={noticeCopy.body} showClose={false} dismissOnOutsidePress={false} dismissOnBack={false}
      footer={<Button onPress={() => view.closeNotice(notice)}>{t('action.close')}</Button>}>{null}</Dialog> : null}
  </>;
  return <ReaderFrame active back={{testID: 'native-read-host-back', label: t('action.back'), onPress: view.back}}
    state={view.loading ? undefined : {title: t('next.states.pageFailed'), body: t('next.states.pageFailedBody'),
      primaryAction: {id: 'back', label: t('action.back'), onPress: view.back}}}/>;
}
