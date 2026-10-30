import { IndexedDBCommandStore } from '../../src/playback/indexeddb-command-store.ts';
import { ClientCommandJournal, type LaneScope } from '../../src/playback/command-journal.ts';

/** Explicit browser test entry point; no invocation, transport or database creation on import. */
export async function verifyIndexedDBAtomicity(factory: IDBFactory, uniqueRunId: string): Promise<string[]> {
  if (!/^[a-zA-Z0-9_-]{8,64}$/.test(uniqueRunId)) throw new Error('Unique isolated test ID required');
  const name='portico-playback-review-'+uniqueRunId;
  const first=new IndexedDBCommandStore(factory,name), second=new IndexedDBCommandStore(factory,name);
  const scope:LaneScope={serverOrigin:'http://localhost:19504',accountId:'test',profileId:'test',controllerId:'test',controllerEpoch:'test',commandLaneId:'test'};
  const one=new ClientCommandJournal(first),two=new ClientCommandJournal(second);
  const checks:string[]=[];
  const equal=(a:unknown,b:unknown,message:string)=>{if(JSON.stringify(a)!==JSON.stringify(b))throw new Error(message);};
  try {
    const initial=await one.initialize(scope);
    // Simultaneous different connections must see one committed lane, not duplicate initialization.
    equal(await two.initialize(scope),initial,'Cross-connection initialization mismatch');
    checks.push('cross_connection_lane_initialization');
    const key=JSON.stringify([scope.serverOrigin,scope.accountId,scope.profileId,scope.controllerId,scope.controllerEpoch,scope.commandLaneId]);
    let rejected=false;
    try { await first.transaction(key,tx=>{tx.putLane({...tx.lane!,revision:'1',retired:true});throw new Error('deliberate rollback');}); }
    catch {rejected=true;}
    equal(rejected,true,'Aborted transaction resolved');
    equal(await two.read(scope),initial,'Aborted transaction changed durable lane');
    checks.push('throw_after_write_rolls_back');
    const mutations=await Promise.allSettled([one.retire(scope,'0'),two.retire(scope,'0')]);
    equal(mutations.every(x=>x.status==='fulfilled'),true,'Idempotent retire failed across connections');
    equal((await one.read(scope))?.revision,'1','Two retire transactions allocated twice');
    checks.push('cross_connection_atomic_retirement');
    await first.close();await second.close();
    const reopened=new IndexedDBCommandStore(factory,name);
    try { equal((await new ClientCommandJournal(reopened).read(scope))?.revision,'1','Reopen lost committed watermark'); }
    finally {await reopened.close();}
    checks.push('close_reopen_preserves_watermark');
    return checks;
  } finally {
    await first.close();await second.close();
    // Only this explicitly named isolated test database; never touches the application journal.
    await new Promise<void>((resolve,reject)=>{const request=factory.deleteDatabase(name);request.onsuccess=()=>resolve();request.onerror=()=>reject(request.error);request.onblocked=()=>reject(new Error('Isolated test database cleanup blocked'));});
  }
}
