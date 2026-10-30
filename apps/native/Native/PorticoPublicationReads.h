#import <Foundation/Foundation.h>
#import <CommonCrypto/CommonDigest.h>
#import <React/RCTBridgeModule.h>
#import <sys/stat.h>
#import <fcntl.h>
#import <unistd.h>
#import <zlib.h>
#import <math.h>
#import <string.h>

// Included by the existing Downloads module: no new native target/package or
// Reader engine. The only authority is a ready owned private download handle.
@interface PorticoPublicationTicket : NSObject
@property(atomic) BOOL closed;
@property BOOL busy;
@property(atomic) double operation;
@property(atomic) double cancelledThrough;
@property int fd;
@property long long bytes;
@property(nonatomic,strong) NSData *member;
@property(nonatomic,strong) dispatch_queue_t authorityQueue;
@property(copy) BOOL (^current)(void);
@end
@implementation PorticoPublicationTicket
- (instancetype)init {if((self=[super init]))_fd=-1;return self;}
@end

@interface PorticoPublicationReads : NSObject
- (void)open:(NSString *)handle file:(NSURL *)file bytes:(long long)bytes digest:(NSString *)digest queue:(dispatch_queue_t)queue current:(BOOL(^)(void))current resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject;
- (void)range:(NSString *)handle operation:(double)operation offset:(double)offset length:(double)length member:(BOOL)member resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject;
- (void)member:(NSString *)handle operation:(double)operation offset:(double)offset compressed:(double)compressed size:(double)size method:(double)method digest:(NSString *)digest resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject;
- (void)cancel:(NSString *)handle operation:(double)operation;
- (void)close:(NSString *)handle;
- (void)invalidate;
@end
@implementation PorticoPublicationReads {
 NSMutableDictionary<NSString *,PorticoPublicationTicket *> *_tickets;
 NSMutableSet<NSString *> *_retired;
 dispatch_queue_t _io;
 BOOL _closed;
}
- (instancetype)init {if((self=[super init])){_tickets=[NSMutableDictionary new];_retired=[NSMutableSet new];_io=dispatch_queue_create("tv.getportico.publication-read",DISPATCH_QUEUE_SERIAL);}return self;}
static BOOL PorticoPublicationLive(PorticoPublicationTicket *ticket){return !ticket.closed && ticket.cancelledThrough<ticket.operation;}
static NSString *PorticoPublicationHex(NSData *data){unsigned char digest[CC_SHA256_DIGEST_LENGTH];CC_SHA256(data.bytes,(CC_LONG)data.length,digest);NSMutableString *out=[NSMutableString new];for(int i=0;i<CC_SHA256_DIGEST_LENGTH;i++)[out appendFormat:@"%02x",digest[i]];return out;}
static BOOL PorticoPublicationDigest(NSString *value){if(![value isKindOfClass:NSString.class]||value.length!=64)return NO;return [value rangeOfCharacterFromSet:[[NSCharacterSet characterSetWithCharactersInString:@"0123456789abcdef"] invertedSet]].location==NSNotFound;}
static BOOL PorticoPublicationNumber(double value,long long max){return isfinite(value)&&floor(value)==value&&value>=0&&value<=max;}
static NSData *PorticoPublicationRead(PorticoPublicationTicket *ticket,long long offset,NSUInteger length){
 if(!PorticoPublicationLive(ticket)||ticket.fd<0||offset<0||length>65536||offset>ticket.bytes-(long long)length)return nil;
 NSMutableData *out=[NSMutableData dataWithLength:length];NSUInteger read=0;
 while(read<length){if(!PorticoPublicationLive(ticket))return nil;ssize_t n=pread(ticket.fd,(char *)out.mutableBytes+read,length-read,(off_t)(offset+read));if(n<=0)return nil;read+=(NSUInteger)n;}
 return PorticoPublicationLive(ticket)?out:nil;
}
- (void)cancel:(NSString *)handle operation:(double)operation {
 if(!PorticoPublicationNumber(operation,9007199254740991LL)||operation<1)return;
 @synchronized(self){PorticoPublicationTicket *ticket=_tickets[handle];if(ticket)ticket.cancelledThrough=MAX(ticket.cancelledThrough,operation);}
}
- (void)close:(NSString *)handle {
 PorticoPublicationTicket *ticket;
 @synchronized(self){if(_retired.count>=4096)_closed=YES;[_retired addObject:handle];ticket=_tickets[handle];[_tickets removeObjectForKey:handle];ticket.closed=YES;}
 if(ticket)dispatch_async(_io,^{ticket.member=nil;if(ticket.fd>=0){close(ticket.fd);ticket.fd=-1;}});
}
- (void)invalidate {NSArray *handles;@synchronized(self){_closed=YES;handles=_tickets.allKeys;}for(NSString *handle in handles)[self close:handle];}
- (void)run:(NSString *)handle operation:(double)operation resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject work:(id(^)(PorticoPublicationTicket *))work {
 PorticoPublicationTicket *ticket;
 @synchronized(self){ticket=_tickets[handle];if(_closed||!ticket||ticket.closed||ticket.busy||!PorticoPublicationNumber(operation,9007199254740991LL)||operation<1||operation<ticket.operation||operation<=ticket.cancelledThrough){reject(@"reading_unavailable",@"reading_unavailable",nil);return;}ticket.busy=YES;ticket.operation=operation;}
 dispatch_async(ticket.authorityQueue,^{
  if(ticket.closed||!ticket.current()){ @synchronized(self){ticket.busy=NO;}[self close:handle];reject(@"reading_retired",@"reading_retired",nil);return;}
  dispatch_async(self->_io,^{
   id result=PorticoPublicationLive(ticket)?work(ticket):nil;
   dispatch_async(ticket.authorityQueue,^{
    BOOL live;@synchronized(self){ticket.busy=NO;live=!self->_closed&&self->_tickets[handle]==ticket&&PorticoPublicationLive(ticket);}
    if(!result||!live||!ticket.current()){if(ticket.cancelledThrough<operation)[self close:handle];reject(@"reading_unavailable",@"reading_unavailable",nil);}else resolve(result);
   });
  });
 });
}
- (void)open:(NSString *)handle file:(NSURL *)file bytes:(long long)bytes digest:(NSString *)digest queue:(dispatch_queue_t)queue current:(BOOL(^)(void))current resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject {
 PorticoPublicationTicket *ticket=[PorticoPublicationTicket new];ticket.bytes=bytes;ticket.authorityQueue=queue;ticket.current=current;
 @synchronized(self){
  NSCharacterSet *bad=[[NSCharacterSet characterSetWithCharactersInString:@"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-"] invertedSet];
  if(_closed||[_retired containsObject:handle]||_tickets.count>=2||_tickets[handle]||handle.length<8||handle.length>128||[handle rangeOfCharacterFromSet:bad].location!=NSNotFound||!PorticoPublicationDigest(digest)||bytes<1||bytes>2147483648LL){reject(@"reading_unavailable",@"reading_unavailable",nil);return;}
  _tickets[handle]=ticket;
 }
 [self run:handle operation:1 resolve:resolve reject:reject work:^id(PorticoPublicationTicket *t){
  int fd=open(file.fileSystemRepresentation,O_RDONLY|O_NOFOLLOW);if(fd<0)return nil;t.fd=fd;
  struct stat st;if(fstat(fd,&st)!=0||!S_ISREG(st.st_mode)||st.st_size!=bytes)return nil;
  CC_SHA256_CTX hash;CC_SHA256_Init(&hash);long long offset=0;
  while(offset<bytes){NSData *chunk=PorticoPublicationRead(t,offset,(NSUInteger)MIN(65536LL,bytes-offset));if(!chunk)return nil;CC_SHA256_Update(&hash,chunk.bytes,(CC_LONG)chunk.length);offset+=chunk.length;}
  unsigned char sum[CC_SHA256_DIGEST_LENGTH];CC_SHA256_Final(sum,&hash);NSMutableString *actual=[NSMutableString new];for(int i=0;i<CC_SHA256_DIGEST_LENGTH;i++)[actual appendFormat:@"%02x",sum[i]];
  return [actual isEqual:digest]?@(bytes):nil;
 }];
}
- (void)range:(NSString *)handle operation:(double)operation offset:(double)offset length:(double)length member:(BOOL)member resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject {
 [self run:handle operation:operation resolve:resolve reject:reject work:^id(PorticoPublicationTicket *t){
  if(!PorticoPublicationNumber(offset,2147483648LL)||!PorticoPublicationNumber(length,49152))return nil;
  NSData *data;
  if(member){NSData *cached=t.member;if(!cached||offset>(double)cached.length-length)return nil;data=[cached subdataWithRange:NSMakeRange((NSUInteger)offset,(NSUInteger)length)];}
  else data=PorticoPublicationRead(t,(long long)offset,(NSUInteger)length);
  return data?[data base64EncodedStringWithOptions:0]:nil;
 }];
}
- (void)member:(NSString *)handle operation:(double)operation offset:(double)offset compressed:(double)compressed size:(double)size method:(double)method digest:(NSString *)digest resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject {
 [self run:handle operation:operation resolve:resolve reject:reject work:^id(PorticoPublicationTicket *t){
  t.member=nil;
  if(!PorticoPublicationNumber(offset,2147483648LL)||!PorticoPublicationNumber(compressed,2147483648LL)||!PorticoPublicationNumber(size,33554432)||offset>t.bytes-compressed||(method!=0&&method!=8)||!PorticoPublicationDigest(digest))return nil;
  NSMutableData *out=[NSMutableData dataWithLength:(NSUInteger)size];NSUInteger written=0;long long consumed=0;BOOL valid=YES,ended=NO;z_stream stream={0};
  if(method==8&&inflateInit2(&stream,-MAX_WBITS)!=Z_OK)return nil;
  while(consumed<(long long)compressed&&valid){
   NSData *input=PorticoPublicationRead(t,(long long)offset+consumed,(NSUInteger)MIN(65536LL,(long long)compressed-consumed));if(!input){valid=NO;break;}consumed+=input.length;
   if(method==0){if(written>out.length||input.length>out.length-written){valid=NO;break;}memcpy((char *)out.mutableBytes+written,input.bytes,input.length);written+=input.length;}
   else {
    stream.next_in=(Bytef *)input.bytes;stream.avail_in=(uInt)input.length;
    while(stream.avail_in>0&&!ended&&valid){
     if(!PorticoPublicationLive(t)){valid=NO;break;}unsigned char extra;NSUInteger room=MIN((NSUInteger)65536,out.length-written);stream.next_out=room?(Bytef *)out.mutableBytes+written:&extra;stream.avail_out=(uInt)(room?:1);
     uInt before=stream.avail_in,capacity=stream.avail_out;int status=inflate(&stream,Z_NO_FLUSH);NSUInteger produced=capacity-stream.avail_out;
     if(!room&&produced){valid=NO;break;}written+=produced;ended=status==Z_STREAM_END;
     if((status!=Z_OK&&!ended)||(!produced&&before==stream.avail_in)){valid=NO;break;}
    }
    if(ended&&((long long)compressed!=consumed||stream.avail_in!=0))valid=NO;
   }
  }
  if(method==8){
   while(valid&&!ended){
    if(!PorticoPublicationLive(t)){valid=NO;break;}unsigned char extra;NSUInteger room=MIN((NSUInteger)65536,out.length-written);stream.next_in=Z_NULL;stream.avail_in=0;stream.next_out=room?(Bytef *)out.mutableBytes+written:&extra;stream.avail_out=(uInt)(room?:1);
    uInt capacity=stream.avail_out;int status=inflate(&stream,Z_FINISH);NSUInteger produced=capacity-stream.avail_out;if(!room&&produced){valid=NO;break;}written+=produced;ended=status==Z_STREAM_END;
    if(!ended&&(!produced||(status!=Z_OK&&status!=Z_BUF_ERROR))){valid=NO;break;}
   }
   if(stream.total_in!=(uLong)compressed)valid=NO;
   inflateEnd(&stream);
  }
  if(!valid||!PorticoPublicationLive(t)||written!=out.length||(method==8&&!ended)||![PorticoPublicationHex(out) isEqual:digest])return nil;
  t.member=out;return @(out.length);
 }];
}
@end
