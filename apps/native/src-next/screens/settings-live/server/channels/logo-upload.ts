import {NativeModules} from 'react-native';
import {screenScopeEnded} from '../../lifetime';
export type NativeLogoImage=Readonly<{name:'channel-logo.jpg';type:'image/jpeg';size:number;base64:string}>;
const object=(value:unknown):value is Record<string,unknown>=>!!value&&typeof value==='object'&&!Array.isArray(value);
/** The system picker returns normalized JPEG bytes, never a retained file URI. */
export async function pickNativeChannelLogo(isCurrent:()=>boolean):Promise<NativeLogoImage|undefined>{
  const picker:unknown=NativeModules.PorticoImagePicker;
  if(!isCurrent()||!object(picker)||typeof picker.pick!=='function')throw screenScopeEnded();
  const result:unknown=await picker.pick();
  if(!isCurrent())throw screenScopeEnded();if(result===null)return;
  if(!object(result)||result.contentType!=='image/jpeg'||typeof result.bytes!=='number'||!Number.isSafeInteger(result.bytes)||result.bytes<1||result.bytes>2*1024*1024||typeof result.base64!=='string'||result.base64.length!==4*Math.ceil(result.bytes/3)||!/^[A-Za-z0-9+/]*={0,2}$/.test(result.base64))throw new Error('invalid_channel_logo_image');
  const decoded=globalThis.atob(result.base64);if(decoded.length!==result.bytes||globalThis.btoa(decoded)!==result.base64)throw new Error('invalid_channel_logo_image');
  return Object.freeze({name:'channel-logo.jpg',type:'image/jpeg',size:result.bytes,base64:result.base64});
}
