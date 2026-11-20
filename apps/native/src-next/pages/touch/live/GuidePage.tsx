import React from 'react';
import {LiveFrame} from '../../../kit/frames';
import {guideFrame} from '../../../kit/live/GuideContent';
import {useLiveGuide, type GuidePageProps} from '../../shared/live/useLiveGuide';
import {ServerLiveOwnerPage} from '../settings/ServerLiveOwnerPage';
import {RecordingsPage} from './RecordingsPage';

export function GuidePage(props: GuidePageProps) {
  const model = useLiveGuide(props);
  if (model.ownerOpen) return <ServerLiveOwnerPage controller={props.owner} backLabel={props.runtime.text.t('channels.tabGuide')} ports={props.ports} runtime={props.runtime}
    initialAction={model.initialOwnerAction} onClose={() => {
      model.setInitialOwnerAction(undefined); model.setOwnerOpen(false); model.setRecordingsReturn(value => value + 1);
    }}/>;
  if (model.recordingsOpen) return <RecordingsPage controller={props.recordings} ports={props.ports} runtime={props.runtime} owner={props.owner}
    onGuide={() => {model.setRecordingsReturn(value => value + 1); model.setRecordingsOpen(false);}}/>;
  return <LiveFrame {...guideFrame(props, model)}/>;
}
