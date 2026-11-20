import React from 'react';
import {LiveFrame} from '../../../kit/frames';
import {recordingsFrame} from '../../../kit/live/RecordingsContent';
import {useRecordings, type RecordingsProps} from '../../shared/live/useRecordings';

export function RecordingsPage(props: RecordingsProps) {
  const model = useRecordings(props);
  return <LiveFrame {...recordingsFrame(props, model)}/>;
}
