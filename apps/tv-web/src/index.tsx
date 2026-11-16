import './fonts.css';
import './shims/keys';
import {AppRegistry} from 'react-native';
import {setLiteEffects} from '../../native/src-next/platform/motion-policy';
import {startDevice} from './platform/device';
import {Root} from './Root';

startDevice();
// LG and Samsung sets are treated as low-powered: the kit's lite effects tier is the rule here,
// not a fallback (no live blur, glows, soft shadows, drift or arrival fades; the owner's ruling).
setLiteEffects(true);
AppRegistry.registerComponent('Portico', () => Root);
AppRegistry.runApplication('Portico', {rootTag: document.getElementById('root')});
