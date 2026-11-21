import React, {type ReactNode} from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button, Chip, Select, Switch, TextField, Tabs} from '../../../kit/controls';
import {SectionCard, SettingsPageFrame} from '../../../kit/frames';
import {BottomSheet, Menu} from '../../../kit/overlays';
import {OverlayFootRow} from '../../../kit/overlays/surface';
import {PageActions, PageColumn, PageHeading, PageList, PageNote, SheetBody} from '../../../kit/settings/PageText';
import {DetailWindowCard, FactCard, LiveMark, MessageLine, MessageLog, ReportText, TroubleshootingRow} from '../../../kit/settings/TroubleshootingParts';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {ServerBroadcastSheet} from '../../shared/settings/ServerBroadcastSheet';
import {useFeedback, useMessages, useMessagesGroup, useReports, useSkippedFiles, useSupport, useTroubleshooting,
  type FeedbackGroup, type MessagesGroup, type ReportsGroup, type SupportGroup, type TroubleshootingTab} from '../../shared/settings/useServerTroubleshooting';

type Frame = Pick<React.ComponentProps<typeof SettingsPageFrame>, 'back' | 'server'>;

/** Settings › Server › Troubleshooting (owner-server.html frame 33, in the settings kit): tabs for Messages and the files
 * Portico couldn't prepare, and for the owner Feedback, Reports and Support. */
export function ServerTroubleshootingPage({ports, runtime, retry, initialTab, ...frame}: Frame & {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime;
  retry?: ReactNode; initialTab?: TroubleshootingTab}) {
  const view = useTroubleshooting(ports, runtime, initialTab), {t, tab, owner} = view;
  return <SettingsPageFrame {...frame} title={t('settings.server.troubleshooting')} testID="server-troubleshooting" scrollRef={view.scrollRef} onScrollY={view.setScrollY}
    tabs={<Tabs testID="server-troubleshooting-tabs" value={tab} onValueChange={view.choose} accessibilityLabel={t('settings.server.troubleshooting')} items={view.tabs}/>}>
    {retry}
    {tab === 'skipped' && <SkippedFiles ports={ports} runtime={runtime}/>}
    {tab === 'messages' && <MessagesTab ports={ports} runtime={runtime} pageScrollY={view.scrollY} scrollToY={view.scrollTo}/>}
    {owner && tab === 'feedback' && view.feedback.group && <Feedback group={view.feedback.group} ports={ports} runtime={runtime}/>}
    {owner && tab === 'reports' && view.reports && <Reports group={view.reports}/>}
    {owner && tab === 'support' && view.support && <Support group={view.support}/>}
  </SettingsPageFrame>;
}

function MessagesTab({ports, runtime, pageScrollY, scrollToY}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; pageScrollY: number; scrollToY: (y: number) => void}) {
  const group = useMessagesGroup(ports, runtime);
  return group ? <Messages group={group} runtime={runtime} pageScrollY={pageScrollY} scrollToY={scrollToY}/> : null;
}

export function Messages({group, runtime, pageScrollY = 0, scrollToY}: {group: MessagesGroup; runtime: NativeShellRuntime; pageScrollY?: number; scrollToY?: (y: number) => void}) {
  const view = useMessages(group, runtime, pageScrollY), {state} = view, t = runtime.text.t;
  return <SectionCard plain><PageColumn testID="server-troubleshooting-messages">
    <PageHeading title={t('web.logs.messages')} lede={t('web.logs.messagesLede')}/>
    <DetailWindowCard title={view.window.title} body={view.window.body} action={view.window.action && <Button size="compact" loading={view.window.action.busy}
      disabled={view.window.action.busy} onPress={view.window.action.onPress}>{view.window.action.label}</Button>}/>
    <PageActions onPage>
      <Chip variant="pill" trailingIcon="i-down" onPress={() => view.openFilter('level')} accessibilityLabel={t('web.logs.level')}>{view.levelLabel}</Chip>
      <Chip variant="pill" trailingIcon="i-down" onPress={() => view.openFilter('category')} accessibilityLabel={t('feedback.category')}>{view.kindLabel}</Chip>
    </PageActions>
    {/* The labelled switch would take the whole row; here it sits beside its word, Following and Refresh. */}
    <PageActions onPage>
      <Switch checked={state.live} accessibilityLabel={t('next.cards.live')} onCheckedChange={view.setLive}/>
      <PageNote strong>{t('next.cards.live')}</PageNote>
      {state.live && <LiveMark label={t('web.logs.following')}/>}
      <Button size="small" variant="text" disabled={state.phase === 'loading'} onPress={view.refresh}>{t('web.selection.refresh')}</Button>
    </PageActions>
    <Menu open={view.filterOpen} onOpenChange={open => {if (!open) view.closeFilter();}} presentation="sheet" title={view.filterTitle} items={view.filterItems}/>
    {view.failure && <PageNote alert onPage>{t('error.unavailable')}</PageNote>}
    {state.phase === 'loading' && !state.lines.length && <PageNote onPage>{t('web.logs.loadingMessages')}</PageNote>}
    {state.phase === 'ready' && !state.lines.length && <PageNote onPage>{t('web.logs.noMessages')}</PageNote>}
    {state.waiting > 0 && <PageActions onPage><Button size="small" variant="glass" testID="server-messages-new-lines" onPress={() => {view.showWaiting(); scrollToY?.(view.rootY + view.logY);}}>
      {t('web.logs.newLines', {count: state.waiting})}</Button></PageActions>}
    <MessageLog onLayout={event => view.setLogY(event.nativeEvent.layout.y)}>
      {view.lines.map((line, index) => <MessageLine key={line.key} first={index === 0} time={line.time} level={line.level} tone={line.tone} kind={line.kind} message={line.message} detail={line.detail}/>)}
    </MessageLog>
    {view.detailLength && <Select label={view.detailLength.label} value={view.detailLength.value} options={view.detailLength.options} disabled={state.changing} onValueChange={view.detailLength.onValueChange}/>}
    {view.keeping && <Select label={view.keeping.label} value={view.keeping.value} options={view.keeping.options} disabled={state.changing} onValueChange={view.keeping.onValueChange}/>}
  </PageColumn></SectionCard>;
}

function SkippedFiles({ports, runtime}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime}) {
  const {read, more} = useSkippedFiles(ports), t = runtime.text.t;
  return <SectionCard plain><PageColumn testID="server-troubleshooting-skipped">
    <PageHeading title={t('next.settings.troubleshootingStuckFiles')} lede={t('next.settings.troubleshootingStuckFilesHelp')}/>
    {read.failed && <PageNote alert onPage>{t('error.unavailable')}</PageNote>}
    {read.loading && !read.items.length && <PageNote onPage>{t('next.settings.loading')}</PageNote>}
    {!read.loading && !read.failed && !read.items.length && <PageNote onPage>{t('next.settings.stuckFilesNone')}</PageNote>}
    {read.items.map((file, index) => <FactCard key={`${file.libraryId}:${file.path}:${index}`} titleMono title={file.path} lines={[...(file.libraryName ? [file.libraryName] : []), file.message]}/>)}
    {read.next && <PageActions onPage><Button size="small" variant="glass" disabled={read.loading} onPress={more}>{t('server.showMore')}</Button></PageActions>}
  </PageColumn></SectionCard>;
}

export function Feedback({group, ports, runtime}: {group: FeedbackGroup; ports: NativeSettingsLivePorts; runtime: NativeShellRuntime}) {
  const view = useFeedback(group), {state, shown, sheet} = view, t = runtime.text.t;
  return <SectionCard plain><PageColumn>
    <PageActions onPage><Button variant="glass" size="compact" icon="i-mail" onPress={view.openMessage}>{t('server.messageEveryone')}</Button></PageActions>
    <ServerBroadcastSheet ports={ports} runtime={runtime} open={view.messageOpen} onOpenChange={view.setMessageOpen}/>
    {state.phase === 'error' && <><PageNote alert onPage>{t('error.unavailable')}</PageNote>
      <PageActions onPage><Button variant="glass" size="compact" onPress={view.retry}>{t('action.tryAgain')}</Button></PageActions></>}
    {state.phase === 'loading' && <PageNote onPage>{t('next.settings.loading')}</PageNote>}
    {state.phase === 'ready' && !state.reports.length && <><PageNote strong onPage>{t('web.feedback.inboxEmpty')}</PageNote><PageNote onPage>{t('web.feedback.inboxEmptyBody')}</PageNote></>}
    {!!view.rows.length && <PageList>{view.rows.map((row, index) => <TroubleshootingRow key={row.key} first={index === 0} title={row.title} detail={row.detail} status={row.status} onPress={row.open}/>)}</PageList>}
    {state.next && <PageActions onPage><Button size="compact" variant="glass" disabled={state.phase === 'loading'} onPress={view.more}>{t('action.showMore')}</Button></PageActions>}
    <BottomSheet open={view.held} onOpenChange={value => {if (!value) view.close();}} title={sheet?.title} description={sheet?.description}
      footer={<OverlayFootRow layout="halves"><Button variant="glass" onPress={view.close}>{t('action.cancel')}</Button>
        <Button disabled={!shown || view.loading || view.busy} loading={view.busy} onPress={view.save}>{t('web.feedback.updateReport')}</Button></OverlayFootRow>}>
      <SheetBody>
        {view.loading && <PageNote>{t('next.settings.loading')}</PageNote>}
        {shown && sheet && <>
          <PageNote strong>{shown.report.message}</PageNote>
          {sheet.sentFromDevice && <PageNote>{sheet.sentFromDevice}</PageNote>}
          {sheet.sentFrom && <PageNote>{sheet.sentFrom}</PageNote>}
          <Select label={t('web.feedback.status')} value={shown.status} disabled={view.busy} options={sheet.statuses} onValueChange={sheet.setStatus}/>
          <TextField label={t('web.feedback.replyLabel')} description={t('web.feedback.replyHelp')} value={shown.reply} maxLength={2000} editable={!view.busy} multiline onChangeText={sheet.setReply}/>
        </>}
        {view.error && <PageNote alert>{t('error.unavailable')}</PageNote>}
      </SheetBody>
    </BottomSheet>
  </PageColumn></SectionCard>;
}

function Reports({group}: {group: ReportsGroup}) {
  const view = useReports(group), {state, records} = view, t = defaultI18n.t;
  return <SectionCard plain><PageColumn>
    <PageHeading title={t('web.diagnostics.clientReports')} lede={t('server.reports.lede')}/>
    {state.phase === 'error' && <PageNote alert onPage>{t('error.unavailable')}</PageNote>}
    <PageActions onPage><Button size="compact" variant="glass" disabled={state.phase === 'loading'} onPress={view.refresh}>{t('web.selection.refresh')}</Button></PageActions>
    {state.phase === 'loading' && <PageNote onPage>{t('next.settings.loading')}</PageNote>}
    {state.phase === 'ready' && !state.reports.length && <PageNote onPage>{t('server.reports.none')}</PageNote>}
    {!!view.rows.length && <PageList>{view.rows.map((row, index) => <TroubleshootingRow key={row.key} first={index === 0} title={row.title} detail={row.detail}
      disabled={state.phase !== 'ready'} onPress={row.open}/>)}</PageList>}
    {state.next && <PageActions onPage><Button size="compact" disabled={state.phase !== 'ready'} onPress={view.more}>{t('action.showMore')}</Button></PageActions>}
    {records && <>
      <PageHeading title={t('settings.server.records')} lede={t('next.settings.recordsHelp')}/>
      <Select label={t('settings.server.records')} value={records.lane} options={records.lanes} onValueChange={records.chooseLane}/>
      {records.items.length ? <PageList>{records.items.map((record, index) => <TroubleshootingRow key={record.key} first={index === 0} title={record.title} detail={record.detail}/>)}</PageList>
        : <PageNote onPage>{t('settings.server.records.none')}</PageNote>}
      <PageActions onPage><Button size="compact" variant="glass" onPress={records.latest}>{t('next.settings.recordsLatest')}</Button>
        {records.hasOlder && <Button size="compact" variant="glass" onPress={records.older}>{t('next.settings.recordsOlder')}</Button>}</PageActions>
    </>}
    <BottomSheet open={!!view.held} onOpenChange={value => {if (!value) view.close();}} title={view.title}
      footer={<OverlayFootRow layout="halves"><Button variant="glass" onPress={view.close}>{t('action.close')}</Button>
        {view.canSave && <Button variant="glass" disabled={view.busy || state.phase !== 'ready'} onPress={view.save}>{t('action.save')}</Button>}
        <Button variant="glass" disabled={view.busy || state.phase !== 'ready'} onPress={view.ask}>{t('action.delete')}</Button></OverlayFootRow>}>
      <SheetBody>
        {view.busy && view.body === undefined && <PageNote>{t('next.settings.loading')}</PageNote>}
        {view.body !== undefined && <ReportText>{view.body || t('server.reports.empty')}</ReportText>}
        {view.error && <PageNote alert>{t('error.unavailable')}</PageNote>}
      </SheetBody>
    </BottomSheet>
    <BottomSheet open={view.heldConfirmation} onOpenChange={value => {if (!value) view.closeConfirmation();}} title={t('server.reports.deleteTitle')} description={t('server.reports.deleteBody')}
      footer={<OverlayFootRow layout="halves"><Button variant="glass" onPress={view.closeConfirmation}>{t('action.cancel')}</Button>
        <Button variant="glass" disabled={view.busy || state.phase !== 'ready'} loading={view.busy} onPress={view.remove}>{t('action.delete')}</Button></OverlayFootRow>}>{null}</BottomSheet>
  </PageColumn></SectionCard>;
}

function Support({group}: {group: SupportGroup}) {
  const view = useSupport(group), {state} = view, t = defaultI18n.t;
  return <SectionCard plain><PageColumn>
    {state.phase === 'error' && <><PageNote alert onPage>{t('error.unavailable')}</PageNote>
      <PageActions onPage><Button size="compact" variant="glass" onPress={view.load}>{t('action.tryAgain')}</Button></PageActions></>}
    {state.phase === 'loading' && !state.facts && <PageNote onPage>{t('next.settings.loading')}</PageNote>}
    {view.permissions && <>
      <PageHeading title={t('web.maintenance.permissionsTitle')} lede={view.permissions.body}/>
      {view.permissions.fixable && <PageActions onPage><Button size="compact" variant="glass" loading={view.fixing} disabled={view.fixing} onPress={view.fix}>{t('web.maintenance.fixPermissions')}</Button></PageActions>}
    </>}
    {view.permissionsFixed && <PageNote onPage>{t('web.maintenance.permissionsFixed')}</PageNote>}
    {view.capabilities && <>
      <PageHeading title={t('web.capabilities.title')} lede={t('web.capabilities.help')}/>
      {view.capabilities.map(capability => <FactCard key={capability.key} title={capability.name} state={capability.state} lines={capability.lines} path={capability.path}/>)}
    </>}
    <PageHeading title={t('settings.server.skippedParts')} lede={t('settings.server.skippedPartsLede')}/>
    {view.skipped.length ? <PageList>{view.skipped.map((part, index) => <TroubleshootingRow key={part.key} first={index === 0} title={part.title} detail={part.detail}/>)}</PageList>
      : <PageNote onPage>{t('settings.server.skippedPartsNone')}</PageNote>}
    {view.skipped.length > 0 && <PageActions onPage><Button size="compact" variant="glass" onPress={view.clearSkipped}>{t('action.clearAll')}</Button></PageActions>}
    <PageHeading title={t('web.logs.exportTitle')} lede={t('web.logs.exportLede')}/>
    <Select label={t('web.logs.timeWindow')} value={view.window.value} disabled={view.exporting} options={view.window.options} onValueChange={view.window.onValueChange}/>
    {view.components.map(component => <Switch key={component.key} checked={component.checked} disabled={view.exporting} onCheckedChange={component.onCheckedChange}>{component.label}</Switch>)}
    <Switch checked={view.paths} disabled={view.exporting || !view.parts.includes('snapshot')} onCheckedChange={view.setPaths}>{t('next.settingsSpec.server.troubleshooting.groups.1.rows.0.children.0.label')}</Switch>
    <PageNote onPage>{t('web.logs.pathsHelp')}</PageNote>
    {view.canExport ? <PageActions onPage><Button testID="server-support-export" size="compact" disabled={view.exporting || !view.parts.length} loading={view.exporting} onPress={view.exportNow}>{t('web.logs.prepare')}</Button></PageActions>
      : <PageNote onPage>{t('error.unavailable')}</PageNote>}
    {view.failed && <PageNote alert onPage>{t('error.unavailable')}</PageNote>}
  </PageColumn></SectionCard>;
}
