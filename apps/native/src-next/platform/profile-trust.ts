import {NativeModules} from 'react-native';
import {parseSelectedViewerRecord,type SelectedViewerStorage} from '../../../../packages/client-core/src/viewer-selection';
const runtime=NativeModules.PorticoRuntime;
export const nativeViewerStorage:SelectedViewerStorage={
 async read(){const raw=await runtime.loadSelectedViewer();if(raw===null)return null;return parseSelectedViewerRecord(JSON.parse(raw));},
 async write(record){await runtime.saveSelectedViewer(JSON.stringify(record));const raw=await runtime.loadSelectedViewer();if((record===null?raw!==null:raw!==JSON.stringify(record)))throw new Error('The selected server credential could not be read back from secure storage.');},
};
export async function profileInstallation(required=false):Promise<string|undefined>{try{const id=await runtime.profileInstallation();if(typeof id!=='string'||!id||id.length>128)throw new Error('Invalid installation metadata.');return id;}catch(e){if(required)throw e;return undefined;}}

export {prepareSecureRecords} from './secure-records';
