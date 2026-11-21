import React, {useSyncExternalStore} from 'react';
import {DownloadChoiceSheet} from '../../../kit/saved/DownloadChoiceSheet';
import {NativeReadingDownloadSheet} from '../../../screens/downloads/ReadingDownloadSheet';
import {getNativeDownloadSnapshot, nativeDownloadRequestKey, subscribeNativeDownload, type NativeDownloadRequest} from '../../../screens/downloads/request';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import {useDownloadChoice} from '../../shared/downloads/useDownloadChoice';

export function NativeDownloadSheet({context}: {context: NativeShellBootstrapContext}) {
  const request = useSyncExternalStore(subscribeNativeDownload, getNativeDownloadSnapshot);
  if (!request) return null;
  return request.reading ? <NativeReadingDownloadSheet context={context} request={request}/>
    : <DownloadOptions context={context} request={request} key={nativeDownloadRequestKey(request)}/>;
}

function DownloadOptions({context, request}: {context: NativeShellBootstrapContext; request: NativeDownloadRequest}) {
  return <DownloadChoiceSheet title={request.title} model={useDownloadChoice({context, request})}/>;
}
