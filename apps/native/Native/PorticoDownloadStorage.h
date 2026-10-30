#import <Foundation/Foundation.h>
#include <sys/stat.h>
#include <fcntl.h>
#include <dirent.h>
#include <unistd.h>
#include <errno.h>
#include <string.h>

typedef struct {uint64_t stored,allocated;BOOL storedKnown,allocatedKnown;__unsafe_unretained NSMutableSet *seen;} PorticoStorageCount;
static const uint64_t PorticoStorageMaxSafe=9007199254740991ULL;
static inline void PorticoStorageUnknown(PorticoStorageCount *count){count->storedKnown=count->allocatedKnown=NO;}
static inline BOOL PorticoStorageAdd(PorticoStorageCount *count,const struct stat *attributes){
 NSString *identity=[NSString stringWithFormat:@"%llu:%llu",(unsigned long long)attributes->st_dev,(unsigned long long)attributes->st_ino];
 if([count->seen containsObject:identity])return NO;[count->seen addObject:identity];
 if(S_ISREG(attributes->st_mode)&&count->storedKnown){
  if(attributes->st_size<0||(uint64_t)attributes->st_size>PorticoStorageMaxSafe-count->stored)PorticoStorageUnknown(count);
  else count->stored+=(uint64_t)attributes->st_size;
 }
 if(count->allocatedKnown){
  if(attributes->st_blocks<0||(uint64_t)attributes->st_blocks>(PorticoStorageMaxSafe-count->allocated)/512)PorticoStorageUnknown(count);
  else count->allocated+=(uint64_t)attributes->st_blocks*512;
 }return YES;
}
/* URL resource getters may resolve a link. Descriptor-relative attributes are required here:
 * count a link's own inode allocation, and never open its target, even during replacement. */
static inline void PorticoStorageDirectory(int fd,PorticoStorageCount *count,unsigned depth){
 struct stat own;
 if(depth>64||fstat(fd,&own)||!S_ISDIR(own.st_mode)){close(fd);PorticoStorageUnknown(count);return;}
 if(!PorticoStorageAdd(count,&own)){close(fd);return;}
 DIR *directory=fdopendir(fd);if(!directory){close(fd);PorticoStorageUnknown(count);return;}
 while(count->storedKnown||count->allocatedKnown){
  errno=0;struct dirent *entry=readdir(directory);
  if(!entry){if(errno)PorticoStorageUnknown(count);break;}
  if(!strcmp(entry->d_name,".")||!strcmp(entry->d_name,".."))continue;
  struct stat attributes;
  if(fstatat(fd,entry->d_name,&attributes,AT_SYMLINK_NOFOLLOW)){PorticoStorageUnknown(count);break;}
  if(S_ISDIR(attributes.st_mode)){
   int child=openat(fd,entry->d_name,O_RDONLY|O_DIRECTORY|O_NOFOLLOW|O_CLOEXEC);struct stat opened;
   if(child<0){PorticoStorageUnknown(count);break;}
   if(fstat(child,&opened)||opened.st_dev!=attributes.st_dev||opened.st_ino!=attributes.st_ino){close(child);PorticoStorageUnknown(count);break;}
   PorticoStorageDirectory(child,count,depth+1);
  }else PorticoStorageAdd(count,&attributes);
 }
 closedir(directory);
}
/** Read-only measurements of this app's entire Downloads root, across all viewers. */
static inline NSDictionary *PorticoDownloadStorage(NSURL *root){
 if(!root.isFileURL)return @{};
 int fd=open(root.fileSystemRepresentation,O_RDONLY|O_DIRECTORY|O_NOFOLLOW|O_CLOEXEC);if(fd<0)return @{};
 NSMutableSet *seen=[NSMutableSet new];PorticoStorageCount count={0,0,YES,YES,seen};PorticoStorageDirectory(fd,&count,0);
 NSMutableDictionary *result=[NSMutableDictionary new];
 if(count.storedKnown)result[@"storedBytes"]=@(count.stored);
 if(count.allocatedKnown)result[@"allocatedBytes"]=@(count.allocated);
 return result;
}
