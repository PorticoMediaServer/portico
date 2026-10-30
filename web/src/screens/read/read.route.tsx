import {defineWebScreen} from '../../route-registry';
import {ReadHost, ReadRouteMarker} from './ReadHost';
import {readSearch} from './route-search';

export default defineWebScreen({id: 'read', component: ReadRouteMarker, retainedHost: ReadHost, chrome: 'standalone', validateSearch: readSearch});
