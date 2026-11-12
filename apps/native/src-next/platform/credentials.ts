import {clearNativeAppLog} from './app-log';
import {nativeNetworkFetch} from './server-connections';
import { NativeModules } from 'react-native';
import {parseNativeCredentialRecord,parseNativeCredentialJSON} from '../../../../packages/client-core/src/credential-format';
import {CredentialSessionService} from '../../../../packages/client-core/src/index';
import {PorticoServicesCredentialApi} from '../../../../packages/client-core/src/portico-services-native';
/** The Portico Account sign-in this device keeps, renewed and ended at Portico Services. */
export function createCredentials(origin: string) {
  let hadAccount = false;
  return new CredentialSessionService(
    new PorticoServicesCredentialApi(origin,nativeNetworkFetch),
    {
      load: async () => {
        const json = await NativeModules.PorticoRuntime.loadCredentials(origin);
        const record = parseNativeCredentialJSON(json); hadAccount = !!record?.active; return record;
      },
      save: async value => {
        if (hadAccount && !value.active) await clearNativeAppLog();
        hadAccount = !!value.active;
        await NativeModules.PorticoRuntime.saveCredentials(
          origin,
          JSON.stringify(parseNativeCredentialRecord(value)),
        );
      },
    },
    () => NativeModules.PorticoRuntime.newRequestId(),
  );
}
