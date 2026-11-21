import React, {useSyncExternalStore} from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {DownloadEpisodes} from '../../../../../../packages/client-core/src/download-requests';
import {Button} from '../../../kit/controls';
import {SectionCard, SheetFrame} from '../../../kit/frames';
import {RetainedOverlay} from '../../../kit/overlays/SurfacePaintHost';
import {DownloadChoiceRow, DownloadQualityRow} from '../../../kit/saved/DownloadChoiceRows';
import {StateView} from '../../../kit/states';
import {ReadingDownloadSheet} from './ReadingDownloadSheet';
import {getNativeDownloadSnapshot, nativeDownloadRequestKey, subscribeNativeDownload, type NativeDownloadRequest}
  from '../../../screens/downloads/request';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import {useDownloadChoice} from '../../shared/downloads/useDownloadChoice';

const t = defaultI18n.t;
const policies = [
  {id: 'all', label: 'downloads.policyAll'},
  {id: 'unwatched', label: 'downloads.policyUnwatched'},
  {id: 'next', label: 'downloads.policyNext'},
] as const;
const keepCounts = [1, 3, 5] as const;

/** The Download sheet opened from a title's Download (collections 31, 32). Books and comics keep their own sheet. */
export function NativeDownloadSheet({context}: {context: NativeShellBootstrapContext}) {
  const request = useSyncExternalStore(subscribeNativeDownload, getNativeDownloadSnapshot);
  if (!request) return null;
  if (request.reading) return <ReadingDownloadSheet context={context} request={request}/>;
  return <DownloadChoice context={context} request={request} key={nativeDownloadRequestKey(request)}/>;
}

/** Which episodes, Keep the next, then Quality, each one card of rows; once added, "Downloads" with View downloads. */
function DownloadChoice({context, request}: {context: NativeShellBootstrapContext; request: NativeDownloadRequest}) {
  const view = useDownloadChoice({context, request});
  const foot = view.done ? <>
    <Button variant="glass" onPress={view.close}>{t('action.close')}</Button>
    <Button onPress={view.viewDownloads}>{t('downloads.view')}</Button>
  </> : undefined;
  return <RetainedOverlay permit={view.permit} allowedTypes={[SheetFrame, SectionCard, DownloadChoiceRow, DownloadQualityRow,
    StateView, Button, React.Fragment]}>
    <SheetFrame kind="task" task="details" testID="native-download-sheet" open onOpenChange={open => {if (!open) view.close();}}
      title={t('action.download')} subtitle={request.title} foot={foot}>
      {view.problem && <StateView {...view.problem} tone="error"/>}
      {view.done ? <StateView icon="i-check" title={view.acceptedSingle ? t('downloads.addedMany', {count: 1}) : t('action.download')}
        body={view.done}/> : <>
        {view.refused && <StateView title={view.refused}/>}
        {view.missingDevice && <StateView title={t('downloads.noDevice')}/>}
        {view.episodic && <SectionCard label={t('downloads.whichEpisodes')}>
          {policies.map(item => <DownloadChoiceRow key={item.id} testID={`download-policy-${item.id}`} label={t(item.label)}
            selected={item.id === view.policy} disabled={!view.editable}
            onPress={() => view.changePolicy(item.id as DownloadEpisodes)}/>)}
        </SectionCard>}
        {view.episodic && view.policy === 'next' && <SectionCard label={t('downloads.keepTheNext')}>
          {keepCounts.map(count => <DownloadChoiceRow key={count} label={t('downloads.keepNext', {count})}
            selected={view.keepNext === count} disabled={!view.editable} onPress={() => view.changeKeepNext(count)}/>)}
        </SectionCard>}
        {!!view.options.length && <SectionCard label={t('downloads.qualityHeading')} help={view.checking ? t('downloads.checking') : view.storage}>
          {view.options.map(option => <DownloadQualityRow key={option.quality} testID={`download-quality-${option.quality}`}
            label={option.label} size={option.size} note={option.meta} description={option.notes?.join(' ')}
            available={option.available} disabled={!view.editable} onPress={() => {void view.choose(option.quality);}}/>)}
        </SectionCard>}
        {!view.options.length && view.checking && <StateView title={t('downloads.checking')}/>}
      </>}
    </SheetFrame>
  </RetainedOverlay>;
}
