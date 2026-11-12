#import <Foundation/Foundation.h>
#import <TargetConditionals.h>

#if TARGET_OS_TV
// tvOS has no persistent support-file storage: its ring lasts only for this run.
static NSString *PorticoReadAppLog(void) { return nil; }
static BOOL PorticoWriteAppLog(NSString *text) { (void)text; return YES; }
#else

// These diagnostics stay on this device, in the app's private support folder, outside backups.
static NSURL *PorticoAppLogURL(void) {
  NSURL *root = [[NSFileManager.defaultManager URLsForDirectory:NSApplicationSupportDirectory inDomains:NSUserDomainMask] firstObject];
  NSURL *folder = [root URLByAppendingPathComponent:@"PorticoLogs" isDirectory:YES];
  if (![NSFileManager.defaultManager createDirectoryAtURL:folder withIntermediateDirectories:YES
    attributes:@{NSFileProtectionKey:NSFileProtectionCompleteUntilFirstUserAuthentication} error:nil]) return nil;
  if (![folder setResourceValue:@YES forKey:NSURLIsExcludedFromBackupKey error:nil]) return nil;
  return [folder URLByAppendingPathComponent:@"app.log"];
}
static NSString *PorticoReadAppLog(void) {
  NSURL *url = PorticoAppLogURL(); if (!url) return nil;
  NSNumber *size = nil; [url getResourceValue:&size forKey:NSURLFileSizeKey error:nil];
  if (size.unsignedLongLongValue > 1024 * 1024) { [NSFileManager.defaultManager removeItemAtURL:url error:nil]; return nil; }
  return [NSString stringWithContentsOfURL:url encoding:NSUTF8StringEncoding error:nil];
}
static BOOL PorticoWriteAppLog(NSString *text) {
  NSURL *url = PorticoAppLogURL(); if (!url) return NO;
  if (!text.length) { return ![NSFileManager.defaultManager fileExistsAtPath:url.path] || [NSFileManager.defaultManager removeItemAtURL:url error:nil]; }
  NSData *data = [text dataUsingEncoding:NSUTF8StringEncoding];
  if (!data || data.length > 1024 * 1024) return NO;
  return [data writeToURL:url options:NSDataWritingAtomic | NSDataWritingFileProtectionCompleteUntilFirstUserAuthentication error:nil];
}

#endif
