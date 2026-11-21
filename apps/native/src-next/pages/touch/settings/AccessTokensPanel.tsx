import React from 'react';
import {apiScopes, type ApiScope} from '../../../../../../packages/client-core/src/api-access';
import {EXPIRY_DAYS, expiryLabel} from '../../../../../../packages/client-core/src/server-admin/api-words';
import {defaultI18n, type MessageId} from '../../../../../../packages/i18n/src';
import {Button, Select, Switch, TextField} from '../../../kit/controls';
import {SecuritySheet, SecurityText} from '../../../kit/settings/SecurityParts';
import {SettingsGroup, SettingsRow} from '../../../kit/shell';
import {StateView} from '../../../kit/states';
import {useTheme} from '../../../kit/theme';
import {useAccessTokens, type AccessTokensProps} from '../../shared/settings/useAccessTokens';

export function AccessTokensPanel(props: AccessTokensProps) {
  const theme = useTheme();
  const {t, runtime} = props;
  const model = useAccessTokens(props);
  const {allowed, showing, busy, error, notice, page, options, view, revoking, name, ticked, days, issued, copied, receipt, canCreate} = model;
  if (!allowed || theme.isTV) return null;
  const canIssue = page?.tokensAllowed !== false && options?.tokensAllowed !== false;
  const title = receipt ? t('api.tokens.copyTitle') : view === 'create' ? t('api.tokens.createTitle')
    : view === 'revoke' ? t('web.access.revokeTitle', {name: revoking?.name ?? ''}) : t('api.tokens.title');
  const scopeRow = (scope: ApiScope) => <Switch key={scope} testID={`access-token-scope-${scope}`} checked={ticked.has(scope)} disabled={busy}
    description={t(`api.scope.${scope}.help` as MessageId)} onCheckedChange={() => model.toggleScope(scope)}>{t(`api.scope.${scope}` as MessageId)}</Switch>;
  const content = view === 'list' ? <>
    {!canIssue && <SecurityText>{t('api.tokens.off')}</SecurityText>}
    {busy && !page && <SecurityText>{t('status.loading')}</SecurityText>}
    {page && !page.items.length ? <SecurityText>{t('api.tokens.none')}</SecurityText> : null}
    {page?.items.length ? <SettingsGroup>{page.items.map((token, index) => <SettingsRow key={token.id} first={index === 0}
      testID={`access-token-${token.id}`} label={token.name}
      description={[token.scopes.map(scope => t(`api.scope.${scope}` as MessageId)).join(', '),
        t('api.credential.created', {when: defaultI18n.date(token.createdAt)}),
        token.lastUsedAt ? t('api.credential.lastUsed', {when: defaultI18n.date(token.lastUsedAt)}) : t('api.credential.neverUsed')].join(' · ')}
      onPress={() => model.pickToken(token)}/>)}</SettingsGroup> : null}
    {page?.nextCursor && <Button testID="access-token-more" size="compact" variant="glass" disabled={busy} onPress={model.more}>{t('action.showMore')}</Button>}
  </> : view === 'create' ? <>
    <TextField testID="access-token-name" label={t('builder.name')} description={t('api.tokens.nameHelp')}
      value={name} onChangeText={model.setName} maxLength={80} editable={!busy}/>
    {apiScopes.filter(scope => !options || options.scopes.includes(scope)).map(scopeRow)}
    <Select testID="access-token-expiry" label={t('api.credential.expiryLabel')} value={String(days)} disabled={busy}
      options={EXPIRY_DAYS.map(value => ({value: String(value), label: expiryLabel(t, value)}))} onValueChange={model.setExpiry}/>
  </> : view === 'revoke' ? <SecurityText>{t('api.tokens.revokeBody')}</SecurityText> : <>
    {issued?.secret ? <SecurityText testID="access-token-secret" secret>{issued.secret}</SecurityText>
      : <SecurityText>{t('api.credential.secretNotShown')}</SecurityText>}
    {copied && <SecurityText>{t('api.credential.copied')}</SecurityText>}
  </>;
  const actions = receipt ? <>
    <Button testID="access-token-copy" variant="glass" disabled={!issued?.secret} onPress={model.copySecret}>{t('web.twoStep.copy')}</Button>
    <Button testID="access-token-copied" onPress={model.close}>{t('web.access.copiedIt')}</Button>
  </> : view === 'create' ? <>
    <Button testID="access-token-back" variant="glass" disabled={busy} onPress={model.back}>{t('action.back')}</Button>
    <Button testID="access-token-create" loading={busy} disabled={busy || !canCreate} onPress={model.submit}>{t('api.tokens.create')}</Button>
  </> : view === 'revoke' ? <>
    <Button testID="access-token-cancel-revoke" variant="glass" disabled={busy} onPress={model.cancelRevoke}>{t('action.cancel')}</Button>
    <Button testID="access-token-revoke" variant="danger" loading={busy} disabled={busy} onPress={model.revoke}>{t('web.access.revoke')}</Button>
  </> : <>
    <Button testID="access-token-close-list" variant="glass" onPress={model.close}>{t('action.close')}</Button>
    {canIssue && <Button testID="access-token-add" icon="i-plus" disabled={busy || !page} onPress={model.begin}>{t('api.tokens.create')}</Button>}
  </>;
  return <>
    <SettingsRow first testID="settings-panel-access-tokens" label={t('api.tokens.title')} onPress={model.open}/>
    {notice && <SecurityText announce>{notice}</SecurityText>}
    <SecuritySheet id="access-tokens" open={showing} onClose={model.dismiss} title={title} locked={receipt} actions={actions}>
      {error !== undefined && <StateView testID="access-token-error" {...runtime.error(error, model.retry, 'account')}/>}
      {content}
    </SecuritySheet>
  </>;
}
