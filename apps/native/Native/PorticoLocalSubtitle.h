#import <Foundation/Foundation.h>
#import <sys/stat.h>
#import <fcntl.h>
#import <unistd.h>

/** Local captions, companions and fonts belong to the admitted media's download directory.
 * The controller supplies the Downloads-root check. Resolve symlinks before comparing. */
static NSURL *PorticoLocalSubtitleURL(id value,NSString *source,BOOL (^allowed)(NSURL *)) {
 if(![value isKindOfClass:NSString.class]||[value length]>4096||!allowed)return nil;
 NSURL *url=[NSURL URLWithString:value],*media=[NSURL URLWithString:source];
 if(!url.isFileURL||!media.isFileURL||url.host.length||media.host.length||url.query||url.fragment||media.query||media.fragment||!allowed(url)||!allowed(media))return nil;
 NSString *directory=[[[media URLByResolvingSymlinksInPath].path stringByDeletingLastPathComponent] stringByStandardizingPath];
 NSString *path=[[url URLByResolvingSymlinksInPath].path stringByStandardizingPath];
 if(![path hasPrefix:[directory stringByAppendingString:@"/"]]||[path isEqual:[media URLByResolvingSymlinksInPath].path])return nil;
 return [NSURL fileURLWithPath:path];
}
/** Bounded regular-file read. Check the opened descriptor's real path as well as the supplied
 * URL, so a replaced symlink cannot redirect a pending read outside this download. */
static NSData *PorticoReadLocalSubtitle(NSURL *url,NSString *source,NSUInteger limit,BOOL (^allowed)(NSURL *)) {
 NSURL *scoped=PorticoLocalSubtitleURL(url.absoluteString,source,allowed);if(!scoped)return nil;
 int fd=open(scoped.fileSystemRepresentation,O_RDONLY|O_NOFOLLOW|O_NONBLOCK);if(fd<0)return nil;
 struct stat info;char actual[PATH_MAX]={0};NSMutableData *bytes=nil;
 if(fstat(fd,&info)==0&&S_ISREG(info.st_mode)&&info.st_size>0&&(uint64_t)info.st_size<=limit&&fcntl(fd,F_GETPATH,actual)==0&&PorticoLocalSubtitleURL([NSURL fileURLWithFileSystemRepresentation:actual isDirectory:NO relativeToURL:nil].absoluteString,source,allowed)){
  bytes=[NSMutableData new];uint8_t buffer[32768];ssize_t count;
  while((count=read(fd,buffer,MIN(sizeof(buffer),limit-bytes.length+1)))>0){if((NSUInteger)count>limit-bytes.length){bytes=nil;break;}[bytes appendBytes:buffer length:(NSUInteger)count];}
  if(count<0||!bytes.length)bytes=nil;
 }
 close(fd);return bytes;
}
