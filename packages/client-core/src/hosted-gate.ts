/** One shared circuit per Portico Account origin.
 *
 * Every part of an app that talks to the account service reports the outcome here, and every
 * AUTOMATIC request asks here first. So once anything learns the service is down or shedding
 * load, the whole app backs off together instead of each feature discovering it separately,
 * and a service that is recovering meets a trickle rather than every client at once.
 *
 * A person is a natural rate limit: a request made because someone tapped something always
 * goes through (and still reports its outcome). Only work the app starts by itself waits:
 * renewing a sign-in ahead of time, asking for fresh routes, retrying a sign-out.
 *
 * State is per process and deliberately not persisted: relaunching the app is a person. */
import {ApiError} from './index.ts';

export type HostedGateSnapshot=Readonly<{failures:number;notBefore:number}>;
export type HostedRequestKind='interactive'|'automatic';
type Clock={now:()=>number;random:()=>number};

const UNIT_MS=5000,MAX_SHIFT=8,MAX_RETRY_AFTER_MS=86400000;

export class HostedGate{
 private failures=0;private notBefore=0;private flights=new Map<string,Promise<unknown>>();
 private clock:Clock;
 constructor(clock:Clock={now:()=>Date.now(),random:()=>Math.random()}){this.clock=clock;}
 getSnapshot():HostedGateSnapshot{return Object.freeze({failures:this.failures,notBefore:this.notBefore});}
 /** Milliseconds an automatic request should still wait; 0 when it may go now. */
 blockedFor():number{return Math.max(0,this.notBefore-this.clock.now());}
 succeeded(){this.failures=0;this.notBefore=0;}
 /** Exponential backoff with jitter proportional to the delay. A Retry-After is a floor that is
  * never undercut, and the same spread is added beyond it, so clients told the same thing do
  * not come back in the same second. */
 failed(retryAfterSeconds?:number){
  this.failures=Math.min(this.failures+1,MAX_SHIFT);
  const delay=UNIT_MS*2**this.failures,spread=Math.floor(this.clock.random()*delay/2),now=this.clock.now();
  const floor=retryAfterSeconds&&retryAfterSeconds>0?Math.min(retryAfterSeconds*1000,MAX_RETRY_AFTER_MS):0;
  this.notBefore=Math.max(this.notBefore,now+Math.max(delay,floor)+spread);
 }
 /** Reports an error's meaning: only "the service is unavailable or asked us to slow down"
  * opens the circuit. A refusal of THIS request (401, 403, 404, 409, 422) is an answer. */
 observe(error:unknown){
  if(error instanceof ApiError){
   if(error.status===429||error.status>=500)this.failed(error.retryAfterSeconds);
   else this.succeeded();
   return;
  }
  if(error instanceof Error&&(error.name==='AbortError'||/cancel|changed|retired/i.test(error.message)))return;
  this.failed();
 }
 /** Runs a request and records its outcome. `automatic` work is refused locally while the
  * circuit is open; a `key` shares one in-flight request between identical callers. */
 async run<T>(kind:HostedRequestKind,request:()=>Promise<T>,key?:string,protocolAnswers:readonly string[]=[]):Promise<T>{
  if(kind==='automatic'){const wait=this.blockedFor();if(wait>0)throw new ApiError(503,'account_service_recovering','Your Portico Account service is temporarily unavailable. Portico will try again shortly.',true,Math.ceil(wait/1000));}
  if(key){const existing=this.flights.get(key);if(existing)return existing as Promise<T>;}
  const flight=(async()=>{try{const value=await request();this.succeeded();return value;}catch(e){
   // Pending/slowdown answers from activation are protocol progress, even
   // when slowdown uses 429. They still reach the activation scheduler.
   if(e instanceof ApiError&&e.status<500&&protocolAnswers.includes(e.code))this.succeeded();else this.observe(e);
   throw e;
  }})();
  if(key){this.flights.set(key,flight);void flight.then(()=>{},()=>{}).then(()=>{if(this.flights.get(key)===flight)this.flights.delete(key);});}
  return flight;
 }
}

const gates=new Map<string,HostedGate>();
/** The shared gate for an account service origin. */
export function hostedGate(origin:string):HostedGate{
 let key=origin;try{key=new URL(origin).origin;}catch{}
 let gate=gates.get(key);if(!gate){gate=new HostedGate();gates.set(key,gate);}return gate;
}
