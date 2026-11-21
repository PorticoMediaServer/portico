import React from 'react';
import {Button} from '../../../kit/controls';
import {SettingsPageFrame} from '../../../kit/frames';
import {LicenceRows} from '../../../kit/settings/LicenceRows';
import {StateView} from '../../../kit/states';
import {useTheme} from '../../../kit/theme';
import {useLicences} from '../../shared/settings/useLicences';

export function LicencesPage(props: Parameters<typeof useLicences>[0]) {
  const {t, title, data, selected, rows, select, back} = useLicences(props);
  const {isTV} = useTheme();
  return <SettingsPageFrame title={title} testID="native-licenses" back={{label: t('action.back'), onPress: back}}
    actions={isTV ? <Button variant="glass" icon="i-left" onPress={back}>{t('action.back')}</Button> : undefined}
    panel={data ? <LicenceRows key={selected ?? 'list'} rows={rows} onSelect={select}/>
      : <StateView title={t(data === undefined ? 'licenses.loading' : 'licenses.unavailable')}/>}/>;
}
