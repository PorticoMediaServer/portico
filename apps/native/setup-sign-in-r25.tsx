// Separate, temporary sign-in setup entry. Never imported by index.js or App.
import './src-next/platform/device-kind';
import './src-next/platform/legacy-events';
import './src-next/platform/random-id';
import '@bacons/text-decoder/install';
import 'react-native-url-polyfill/auto';
import { AppRegistry } from 'react-native';
import { SignInOnly } from './setup/SignInOnly';
import { name as appName } from './app.json';

AppRegistry.registerComponent(appName, () => SignInOnly);
