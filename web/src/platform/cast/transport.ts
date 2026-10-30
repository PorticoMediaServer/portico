import {CAST_NAMESPACE, type CastEvent, type CastTransport} from '@core/cast/index.ts';
import type {CastContext, CastFramework, CastMessageListener, CastSession, CastSessionEvent} from './sdk';

export type ConfirmedCastDevice = Readonly<{name: string; id?: string}>;
export type CastReceiverStatus = Extract<CastEvent, {type: 'status'}>;

/** Only a selected SDK session supplies a receiver's name and id. */
export function confirmedCastDevice(session: CastSession): ConfirmedCastDevice | undefined {
  const device = session.getCastDevice();
  const name = typeof device.friendlyName === 'string' ? device.friendlyName.trim().slice(0, 64) : '';
  if (!name) return undefined;
  return Object.freeze({name, ...(typeof device.label === 'string' && device.label ? {id: device.label} : {})});
}

/** Portico's custom channel only. No CAF media load or media/grant URL is accepted here. */
export function castTransportFor(session: CastSession, context: CastContext, framework: CastFramework): CastTransport {
  const device = session.getCastDevice();
  const current=()=>context.getCurrentSession()===session;
  return {
    deviceName: typeof device.friendlyName === 'string' ? device.friendlyName.trim().slice(0, 64) : '',
    ...(typeof device.label === 'string' && device.label ? {deviceId: device.label} : {}),
    send: command => current()?session.sendMessage(CAST_NAMESPACE, command).then(() => undefined):Promise.reject(new Error('cast_session_replaced')),
    onMessage: receive => {
      const listener: CastMessageListener = (_namespace, raw) => {
        if(!current())return;
        receive(raw);
      };
      session.addMessageListener(CAST_NAMESPACE, listener);
      return () => session.removeMessageListener(CAST_NAMESPACE, listener);
    },
    onSessionEnd: ended => {
      const listener = (event: CastSessionEvent) => {
        if(context.getCurrentSession()&&context.getCurrentSession()!==session){ended();return;}
        if (event.sessionState !== framework.SessionState.SESSION_ENDED && event.sessionState !== framework.SessionState.SESSION_START_FAILED) return;
        const current = context.getCurrentSession();
        if (!current || current === session) ended();
      };
      const type = framework.CastContextEventType.SESSION_STATE_CHANGED;
      context.addEventListener(type, listener);
      return () => context.removeEventListener(type, listener);
    },
    end: stopReceiver => {
      try { if(!current())return;void Promise.resolve(session.endSession(stopReceiver)).catch(() => {}); } catch { /* The SDK may have already ended it. */ }
    },
  };
}
