/** A playback position, or an exact track/chapter length. Never wraps at an hour. */
export function elapsedTime(seconds:number):string {
 const total=Number.isFinite(seconds)?Math.max(0,Math.floor(seconds)):0;
 const hours=Math.floor(total/3600),minutes=Math.floor(total%3600/60),rest=String(total%60).padStart(2,'0');
 return hours?`${hours}:${String(minutes).padStart(2,'0')}:${rest}`:`${minutes}:${rest}`;
}
/** Concise duration for media facts. Unknown/zero is omitted rather than presented as a zero-length film. */
export function durationLabel(seconds:number):string {
 if(!Number.isFinite(seconds)||seconds<=0)return '';
 const total=Math.max(1,Math.floor(seconds)),hours=Math.floor(total/3600),minutes=Math.floor(total%3600/60);
 if(hours)return `${hours} hr${minutes?` ${minutes} min`:''}`;
 return minutes?`${minutes} min`:`${total} sec`;
}
