import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {SectionCard, SheetFrame} from '../../../kit/frames';
import {RetainedOverlay} from '../../../kit/overlays/SurfacePaintHost';
import {DownloadChoiceRow} from '../../../kit/saved/DownloadChoiceRows';
import {StateView} from '../../../kit/states';
import {useTheme} from '../../../kit/theme';
import type {NativeDownloadRequest} from '../../../screens/downloads/request';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import {useReadingDownloadChoice} from '../../shared/downloads/useReadingDownloadChoice';

const t = defaultI18n.t;

/** Download for a book, comic or magazine (tablet-read 12b): the kit task sheet. On a phone "Cancel · Download" with
 * Download and the chosen version's size at its foot; on a tablet the side panel with Download and Cancel stacked.
 * One card of the versions this device can read, each with its size, one chosen; a version the server can't open says
 * why and offers nothing; the room left on this device over the card. */
export function ReadingDownloadSheet({context, request}: {context: NativeShellBootstrapContext; request: NativeDownloadRequest}) {
  const theme = useTheme(), view = useReadingDownloadChoice({context, request}), chosen = view.selectedVersion;
  const action = chosen ? {label: view.busy ? t('downloads.adding') : `${t('action.download')} · ${view.size(chosen)}`,
    disabled: !view.ready(chosen) || view.busy, onPress: view.download} : undefined;
  return <RetainedOverlay permit={view.permit} allowedTypes={[SheetFrame, SectionCard, DownloadChoiceRow, StateView, React.Fragment]}>
    <SheetFrame kind="task" task="task" testID="native-reading-download-sheet" open title={t('action.download')} subtitle={request.title}
      action={action} onOpenChange={open => {if (!open) view.dismiss();}}>
      {view.loading && <StateView variant="inline" title={t('status.loading')}/>}
      {view.unavailable && <StateView variant="inline" title={t('next.states.pageFailed')} body={t('next.states.pageFailedBody')}/>}
      {view.refusal && <StateView variant="inline" title={view.refusal}/>}
      {view.failed && <StateView variant="inline" title={t('next.states.pageFailed')} body={t('next.states.pageFailedBody')}
        primaryAction={{id: 'retry', label: t('next.states.tryAgain'), onPress: view.retryLoad}}/>}
      {!!view.versions.length && <SectionCard help={view.free === undefined ? undefined
        : t(theme.platform === 'phone' ? 'next.saved.phone.free' : 'downloads.freeOnDevice', {size: view.free})}>
        {view.versions.map(version => <DownloadChoiceRow key={version.sourceId} testID={`reading-download-${version.sourceId}`}
          label={version.format.toUpperCase()} detail={version.unreadable === undefined ? view.size(version) : undefined}
          description={version.unreadable} selected={chosen?.sourceId === version.sourceId} disabled={!view.ready(version)}
          onPress={() => view.select(version.sourceId)}/>)}
      </SectionCard>}
    </SheetFrame>
  </RetainedOverlay>;
}
