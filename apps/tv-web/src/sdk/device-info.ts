import {deviceFacts} from '../platform/device';

/** `@amazon-devices/react-native-device-info` in a TV browser: the set's own name, if the platform says it. */
export default {
  getDeviceName: (): Promise<string> => deviceFacts().then(facts => facts.deviceName ?? ''),
};
