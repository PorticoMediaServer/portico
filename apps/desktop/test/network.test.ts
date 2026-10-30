import {test} from 'node:test';
import assert from 'node:assert/strict';
import {DesktopNetworkMonitor} from '../src/network.ts';

test('native network facts expire and failure/unknown never grants unmetered',async()=>{
 let now=0,cost:unknown='unmetered',failed=false,calls=0;
 const monitor=new DesktopNetworkMonitor({async read(){calls++;if(failed)throw new Error('private native diagnostic');return cost;}},()=>now,{set:()=>1,clear(){}});
 assert.equal(monitor.current(),'unknown');monitor.start();await new Promise(resolve=>setImmediate(resolve));
 assert.equal(monitor.current(),'unmetered');monitor.start();assert.equal(calls,1);
 now=10001;assert.equal(monitor.current(),'unknown');cost='metered';await monitor.refresh();assert.equal(monitor.current(),'metered');
 cost='guessed_unmetered';await monitor.refresh();assert.equal(monitor.current(),'unknown');
 cost='unmetered';await monitor.refresh();failed=true;await monitor.refresh();assert.equal(monitor.current(),'unknown');monitor.dispose();
});

test('late native fact after disposal is ignored and leaves no timer/subscriber',async()=>{
 let resolve!:(value:unknown)=>void,scheduled=0,notifications=0;
 const monitor=new DesktopNetworkMonitor({read:()=>new Promise(done=>{resolve=done;})},()=>0,{set(){scheduled++;return 1},clear(){}});
 monitor.subscribe(()=>notifications++);monitor.start();monitor.dispose();resolve('unmetered');await new Promise(done=>setImmediate(done));
 assert.equal(monitor.current(),'unknown');assert.equal(scheduled,0);assert.equal(notifications,0);
});
