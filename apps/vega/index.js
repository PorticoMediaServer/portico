import '@bacons/text-decoder/install';
import 'react-native-url-polyfill/auto';
import './src/shims/request-ids';
import {AppRegistry} from 'react-native';
import {Root} from './src/Root';
import '../native/src-next/platform/error-capture';
import {name as appName} from './app.json';

AppRegistry.registerComponent(appName, () => Root);
