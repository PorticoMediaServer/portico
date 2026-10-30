import {nativeNetworkFetch} from './server-connections';
import { NativeModules } from 'react-native';
import {parseNativeCredentialRecord,parseNativeCredentialJSON} from '../../../../packages/client-core/src/credential-format';
import {
  CredentialSessionService,
  HttpCredentialApi,
} from '../../../../packages/client-core/src/index';
export function createCredentials(origin: string) {
  return new CredentialSessionService(
    new HttpCredentialApi(origin,nativeNetworkFetch),
    {
      load: async () => {
        const json = await NativeModules.PorticoRuntime.loadCredentials(origin);
        return parseNativeCredentialJSON(json);
      },
      save: async value => {
        await NativeModules.PorticoRuntime.saveCredentials(
          origin,
          JSON.stringify(parseNativeCredentialRecord(value)),
        );
      },
    },
    () => NativeModules.PorticoRuntime.newRequestId(),
  );
}
