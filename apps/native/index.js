import './src-next/platform/device-kind';
import './src-next/platform/legacy-events';
import './src-next/platform/random-id';
import '@bacons/text-decoder/install';
import 'react-native-url-polyfill/auto';
import './src-next/platform/error-capture';
import {AppRegistry} from 'react-native';
import App from './App';
import {name as appName} from './app.json';

AppRegistry.registerComponent(appName, () => App);
