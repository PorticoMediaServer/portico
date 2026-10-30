import type { AtomicCommandStore, JournalTransaction, LaneState, PreparedCommand } from './command-journal.ts';
import type { ID } from './protocol.ts';

type CommandRow = { key: string; requestKey: string; sequenceKey: string; command: PreparedCommand };
const compound = (lane: string, id: string) => JSON.stringify([lane, id]);

/** Browser persistence only. Native owns its equivalent atomic transaction while JS is suspended. */
export class IndexedDBCommandStore implements AtomicCommandStore {
  private database: Promise<IDBDatabase> | null = null;
  constructor(private readonly factory: IDBFactory, private readonly name = 'portico-playback-v2') {}

  private open(): Promise<IDBDatabase> {
    if (this.database) return this.database;
    this.database = new Promise((resolve, reject) => {
      const request = this.factory.open(this.name, 1);
      let refused = false;
      request.onupgradeneeded = () => {
        const db = request.result;
        db.createObjectStore('lanes', { keyPath: 'key' });
        const commands = db.createObjectStore('commands', { keyPath: 'key' });
        commands.createIndex('request', 'requestKey', { unique: true });
        commands.createIndex('sequence', 'sequenceKey', { unique: true });
      };
      request.onblocked = () => { refused = true; reject(new Error('Playback journal upgrade blocked')); };
      request.onerror = () => reject(request.error ?? new Error('Playback journal open failed'));
      request.onsuccess = () => {
        const db = request.result;
        if (refused) { db.close(); return; }
        db.onversionchange = () => { db.close(); this.database = null; };
        resolve(db);
      };
    });
    // Permit an explicit later attempt after failure without creating timers or deleting storage.
    this.database.catch(() => { this.database = null; });
    return this.database;
  }

  async transaction<T>(key: string, work: (tx: JournalTransaction) => T,
    lookup?: Readonly<{ mutationId: ID; requestId: ID }>): Promise<T> {
    const db = await this.open();
    return new Promise<T>((resolve, reject) => {
      // Strict durability is requested: resolve only after the browser commits both stores.
      const transaction = db.transaction(['lanes', 'commands'], 'readwrite', { durability: 'strict' });
      if (transaction.durability !== 'strict') {
        transaction.abort(); reject(new Error('Strict playback journal durability is unavailable')); return;
      }
      const lanes = transaction.objectStore('lanes'), commands = transaction.objectStore('commands');
      const laneRequest = lanes.get(key);
      const mutationRequest = lookup ? commands.get(compound(key, lookup.mutationId)) : null;
      const requestRequest = lookup ? commands.index('request').get(compound(key, lookup.requestId)) : null;
      let remaining = lookup ? 3 : 1, result: T, failure: unknown;
      transaction.oncomplete = () => resolve(result);
      transaction.onabort = () => reject(failure ?? transaction.error ?? new Error('Playback journal transaction aborted'));
      transaction.onerror = () => { /* Abort is the single failure completion boundary. */ };
      const ready = () => {
        if (--remaining !== 0) return;
        try {
          const lane: LaneState | null = laneRequest.result?.lane ?? null;
          const mutation: PreparedCommand | null = (mutationRequest?.result as CommandRow | undefined)?.command ?? null;
          const request: PreparedCommand | null = (requestRequest?.result as CommandRow | undefined)?.command ?? null;
          const tx: JournalTransaction = {
            lane,
            findMutation: id => {
              if (id !== lookup?.mutationId) throw new Error('Mutation lookup was not declared');
              return mutation;
            },
            findRequest: id => {
              if (id !== lookup?.requestId) throw new Error('Request lookup was not declared');
              return request;
            },
            putLane: value => { lanes.put({ key, lane: value }); },
            addCommand: value => {
              commands.add({ key: compound(key, value.localMutationId), requestKey: compound(key, value.requestId),
                sequenceKey: compound(key, value.sequence), command: value } satisfies CommandRow);
            },
          };
          result = work(tx);
          if (result && typeof (result as { then?: unknown }).then === 'function')
            throw new Error('Playback journal callback must be synchronous');
        } catch (error) { failure = error; transaction.abort(); }
      };
      laneRequest.onsuccess = ready;
      if (mutationRequest) mutationRequest.onsuccess = ready;
      if (requestRequest) requestRequest.onsuccess = ready;
    });
  }

  async close(): Promise<void> {
    const pending = this.database;
    this.database = null;
    if (pending) (await pending).close();
  }
}
