import {createNativeAccountClient as createCoreClient} from '../../../../packages/client-core/src/query/native-account-client';
import {nativeNetworkFetch} from './server-connections';

export function createNativeAccountClient(origin: string, binding: string, isCurrent: () => boolean = () => true, ownsResult: () => boolean = () => false) {
  return createCoreClient(origin, binding, nativeNetworkFetch, isCurrent, ownsResult);
}
