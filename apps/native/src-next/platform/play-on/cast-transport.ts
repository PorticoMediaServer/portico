import {NativeEventEmitter, NativeModules, Platform} from 'react-native';

/**
 * IOS-PLAYER-03: the Google Cast transport on iPhone/iPad (`Native/PorticoCast.m`). It moves JSON
 * on Portico's Cast channel and reports the session; the sender protocol itself (pair with a
 * one-use code from `POST /v1/cast/bootstrap`, then load/play/seek, takeover and replaced) is
 * client-core's, shared with web (`packages/client-core/src/cast/`, F-core), and is plugged in
 * through this interface. Television never casts.
 */
export type CastSessionEvent = {state: 'started' | 'ended' | 'failed'; deviceName: string; deviceId?:string; attachmentId?:string;error: string};

type Native = {
  available(): Promise<boolean>;
  current(): Promise<{deviceName:string;deviceId?:string;attachmentId:string}|null>;
  choose(): void;
  send(json:string,attachmentId:string):Promise<boolean>;
  end(stopReceiver:boolean,attachmentId:string):void;
};

const native = !Platform.isTV ? (NativeModules.PorticoCast as Native | undefined) : undefined;
const emitter = native ? new NativeEventEmitter(NativeModules.PorticoCast) : null;

/** Attachment ids are native-issued transport witnesses, not viewer authority. */
export const validCastAttachment=(value:unknown):value is string=>typeof value==='string'&&/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(value);
const retired=()=>Object.assign(new Error('The Cast attachment changed.'),{code:'cast_attachment_retired'});
/** No legacy/default target. Every active operation carries its original attachment. */
export const castTransport = {
  send:(message:unknown,attachmentId?:string)=>native&&validCastAttachment(attachmentId)?native.send(JSON.stringify(message),attachmentId).then(()=>undefined):Promise.reject(retired()),
  onMessage:(listener:(message:unknown)=>void,attachmentId?:string)=>{
    if(!native||!validCastAttachment(attachmentId))return()=>{};
    let live=true;
    const sub=emitter?.addListener('PorticoCastMessage',(event:{message:string;attachmentId?:string})=>{
      if(!live||event.attachmentId!==attachmentId||typeof event.message!=='string'||event.message.length>65536)return;
      // Native checked before emit; check the actual attachment again when a
      // queued bridge event arrives. Never relabel it with the latest target.
      void native.current().then(current=>{
        if(!live||current?.attachmentId!==attachmentId)return;
        try{listener(JSON.parse(event.message));}catch{/* malformed payload */}
      },()=>{});
    });
    return()=>{live=false;sub?.remove();};
  },
  onSessionEnd:(listener:()=>void,attachmentId?:string)=>{
    if(!validCastAttachment(attachmentId))return()=>{};
    const sub=emitter?.addListener('PorticoCastSession',(event:CastSessionEvent)=>{if(event.attachmentId===attachmentId&&event.state!=='started')listener();});
    return()=>sub?.remove();
  },
  end:(stopReceiver=false,attachmentId?:string)=>{if(native&&validCastAttachment(attachmentId))native.end(stopReceiver,attachmentId);},
};

/** Internal: the one native bridge instance the old hook and chooser share. */
export const castBridge = {native, emitter};
