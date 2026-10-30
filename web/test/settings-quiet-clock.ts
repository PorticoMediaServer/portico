export const tick = async () => {for(let i=0;i<50;i++)await Promise.resolve();};
export const deferred = <T>() => {let resolve!:(value:T)=>void;let reject!:(error:unknown)=>void;const promise=new Promise<T>((yes,no)=>{resolve=yes;reject=no;});return {promise,resolve,reject};};
export function quietClock() {
  let now=0,next=0;const tasks=new Map<number,{at:number;fn:()=>void}>();
  return {env:{now:()=>now,random:()=>0,setTimer(fn:()=>void,ms:number){const id=++next;tasks.set(id,{at:now+ms,fn});return id;},clearTimer(id:unknown){tasks.delete(id as number);}},
    pending:()=>tasks.size,
    async advance(ms:number){const end=now+ms;for(;;){const due=[...tasks].filter(([,t])=>t.at<=end).sort((a,b)=>a[1].at-b[1].at)[0];if(!due)break;tasks.delete(due[0]);now=due[1].at;due[1].fn();await tick();}now=end;await tick();}};
}
