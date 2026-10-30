import {defaultI18n} from '@i18n';
import {StateLayout} from './State';
/** The server has explicitly answered starting; no invented version, step or ETA. */
export function ServerStarting({onSwitchServer}: {onSwitchServer: () => void}) {
  return <StateLayout backdrop={{kind: 'colour', shape: 'side'}} tone="accent" icon="i-refresh"
    title={defaultI18n.t('next.states.serverStarting')} body={defaultI18n.t('next.states.serverStartingBody')}
    secondary={{id: 'switch', label: defaultI18n.t('next.states.switchServer'), icon: 'i-server', onPress: onSwitchServer}}/>;
}
