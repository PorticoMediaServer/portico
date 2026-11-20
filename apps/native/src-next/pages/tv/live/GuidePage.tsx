import React from 'react';
import {LiveFrame} from '../../../kit/frames';
import {guideFrame} from '../../../kit/live/GuideContent';
import {useLiveGuide, type GuidePageProps} from '../../shared/live/useLiveGuide';
import {TVManagementHandoff} from '../../../tv/auth';
import {RecordingsPage} from './RecordingsPage';

export function GuidePage(props: GuidePageProps) {
  const model = useLiveGuide(props);
  if (model.managementActive && model.management) return <TVManagementHandoff url={model.management.url} onBack={model.closeManagement}/>;
  if (model.recordingsOpen) return <RecordingsPage controller={props.recordings} ports={props.ports} runtime={props.runtime}
    onGuide={() => {model.setRecordingsReturn(value => value + 1); model.setRecordingsOpen(false);}}/>;
  return <LiveFrame {...guideFrame(props, model)}/>;
}
