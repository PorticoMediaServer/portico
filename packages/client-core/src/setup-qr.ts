/** QR Model 2, version 5-L, byte mode, mask 0. One RS block (108 data +
 * 26 parity codewords). A bounded canonical verification URL is the only input;
 * callers never include credentials. The surrounding UI always prints the URL.
 * Fixed version/mask keeps rendering atomic and independent of a network service. */
export function setupQR(value:string):readonly (readonly boolean[])[] {
 const bytes=new TextEncoder().encode(value);
 if(bytes.length>106)throw new Error('The verification address is too long for the setup QR. Use the printed address.');
 const bits:number[]=[];
 const append=(value:number,n:number)=>{for(let i=n-1;i>=0;i--)bits.push(value>>>i&1);};
 append(4,4);append(bytes.length,8);for(const byte of bytes)append(byte,8);
 append(0,Math.min(4,864-bits.length));while(bits.length%8)bits.push(0);
 const data:number[]=[];for(let i=0;i<bits.length;i+=8)data.push(bits.slice(i,i+8).reduce((v,b)=>v*2+b,0));
 for(let i=0;data.length<108;i++)data.push(i%2?0x11:0xec);
 const mul=(a:number,b:number)=>{let value=0;for(let i=0;i<8;i++){if(b&1)value^=a;b>>>=1;a<<=1;if(a&256)a^=0x11d;}return value;};
 let generator=[1],root=1;
 for(let i=0;i<26;i++){const next=new Array<number>(generator.length+1).fill(0);for(let j=0;j<generator.length;j++){next[j]^=generator[j];next[j+1]^=mul(generator[j],root);}generator=next;root=mul(root,2);}
 const divided=[...data,...new Array<number>(26).fill(0)];
 for(let i=0;i<data.length;i++){const factor=divided[i];for(let j=0;j<generator.length;j++)divided[i+j]^=mul(factor,generator[j]);}
 const codewords=[...data,...divided.slice(108)],n=37;
 const matrix=Array.from({length:n},()=>new Array<boolean>(n).fill(false)),reserved=Array.from({length:n},()=>new Array<boolean>(n).fill(false));
 const set=(x:number,y:number,dark:boolean)=>{if(x>=0&&x<n&&y>=0&&y<n){matrix[y][x]=dark;reserved[y][x]=true;}};
 const finder=(cx:number,cy:number)=>{for(let dy=-4;dy<=4;dy++)for(let dx=-4;dx<=4;dx++){const d=Math.max(Math.abs(dx),Math.abs(dy));set(cx+dx,cy+dy,d!==2&&d!==4);}};
 finder(3,3);finder(n-4,3);finder(3,n-4);
 for(let i=0;i<n;i++){if(!reserved[6][i])set(i,6,i%2===0);if(!reserved[i][6])set(6,i,i%2===0);}
 for(let dy=-2;dy<=2;dy++)for(let dx=-2;dx<=2;dx++)set(30+dx,30+dy,Math.max(Math.abs(dx),Math.abs(dy))!==1);
 const format=0x77c4,bit=(i:number)=>!!(format>>>i&1);
 for(let i=0;i<=5;i++)set(8,i,bit(i));set(8,7,bit(6));set(8,8,bit(7));set(7,8,bit(8));
 for(let i=9;i<15;i++)set(14-i,8,bit(i));
 for(let i=0;i<8;i++)set(n-1-i,8,bit(i));for(let i=8;i<15;i++)set(8,n-15+i,bit(i));set(8,n-8,true);
 let cursor=0;
 for(let right=n-1;right>=1;right-=2){if(right===6)right=5;for(let v=0;v<n;v++){const y=((right+1)&2)===0?n-1-v:v;for(let side=0;side<2;side++){const x=right-side;if(reserved[y][x])continue;const dark=cursor<codewords.length*8?!!(codewords[cursor>>>3]>>>(7-(cursor&7))&1):false;cursor++;matrix[y][x]=dark!==((x+y)%2===0);}}}
 return matrix;
}
