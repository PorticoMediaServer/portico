#import <Foundation/Foundation.h>
#include <math.h>

// What the download manifest restores, independent of the owner, its session and its queue, so the
// restoration can be run as a test (N01, apps/native/test/apple-download-manifest-tests.m).
// Header-only: PorticoDownloads.m includes it.

NS_ASSUME_NONNULL_BEGIN

static inline BOOL PorticoSafeIdentifier(NSString *value) {
  if (![value isKindOfClass:NSString.class] || value.length < 1 || value.length > 160) return NO;
  NSCharacterSet *allowed = [NSCharacterSet characterSetWithCharactersInString:@"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-"];
  return [value rangeOfCharacterFromSet:allowed.invertedSet].location == NSNotFound;
}

static inline BOOL PorticoDownloadDigest(NSString *value) {
  return [value isKindOfClass:NSString.class] && value.length == 64 &&
    [value.lowercaseString rangeOfCharacterFromSet:[NSCharacterSet characterSetWithCharactersInString:@"0123456789abcdef"].invertedSet].location == NSNotFound;
}

static inline BOOL PorticoDownloadValidFiles(NSArray *files) {
  if (![files isKindOfClass:NSArray.class] || files.count < 2) return NO;
  for (NSDictionary *file in files) {
    if (![file isKindOfClass:NSDictionary.class] || !PorticoSafeIdentifier(file[@"sourceId"])) return NO;
    NSString *name=file[@"fileName"], *digest=file[@"sha256"], *url=file[@"url"];
    if (![name isKindOfClass:NSString.class] || !name.length || name.length>160 || [name isEqual:@"."] || [name isEqual:@".."] ||
        [name rangeOfCharacterFromSet:[NSCharacterSet characterSetWithCharactersInString:@"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._-"].invertedSet].location != NSNotFound) return NO;
    if (![digest isKindOfClass:NSString.class] || (digest.length && !PorticoDownloadDigest(digest))) return NO;
    NSNumber *bytes=file[@"bytes"];
    if (![bytes isKindOfClass:NSNumber.class] || CFGetTypeID((__bridge CFTypeRef)bytes)==CFBooleanGetTypeID() ||
        !isfinite(bytes.doubleValue) || bytes.doubleValue<1 || bytes.doubleValue>9007199254740991.0 || floor(bytes.doubleValue)!=bytes.doubleValue) return NO;
    if (!([url isKindOfClass:NSString.class] && ([url hasPrefix:@"http://"] || [url hasPrefix:@"https://"])) && !PorticoDownloadDigest(file[@"actualSha256"])) return NO;
    for (NSString *key in @[@"offsetSeconds",@"durationSeconds",@"startSeconds",@"endSeconds"]) if (file[key]) {
      NSNumber *seconds=file[key];
      if (![seconds isKindOfClass:NSNumber.class] || !isfinite(seconds.doubleValue) || seconds.doubleValue<0) return NO;
    }
  }
  return YES;
}

static inline BOOL PorticoDownloadValidEntry(NSString *identifier, NSDictionary *entry) {
  if (!PorticoSafeIdentifier(identifier) || ![entry isKindOfClass:NSDictionary.class] || ![entry[@"id"] isEqual:identifier]) return NO;
  if (![@[@"downloading", @"paused", @"failed", @"ready", @"removed"] containsObject:entry[@"state"] ?: NSNull.null]) return NO;
  NSString *digest = entry[@"sha256"];
  if (!PorticoDownloadDigest(digest) && !([digest isKindOfClass:NSString.class] && !digest.length &&
      ![entry[@"state"] isEqual:@"ready"] && PorticoSafeIdentifier(entry[@"sourceId"]))) return NO;
  if (entry[@"files"]) {
    if (!PorticoDownloadValidFiles(entry[@"files"])) return NO;
    NSNumber *index=entry[@"fileIndex"];
    if (![index isKindOfClass:NSNumber.class] || index.doubleValue!=index.integerValue || index.integerValue<0 || index.unsignedIntegerValue >= [entry[@"files"] count]) return NO;
  }
  NSNumber *bytes = entry[@"bytes"];
  return [bytes isKindOfClass:NSNumber.class] && CFGetTypeID((__bridge CFTypeRef)bytes) != CFBooleanGetTypeID()
    && isfinite(bytes.doubleValue) && bytes.doubleValue >= 1 && bytes.doubleValue <= 9007199254740991.0 && floor(bytes.doubleValue) == bytes.doubleValue;
}

/** The entries a manifest restores: none when there is no file (`raw` nil), nil when the file
 * exists and cannot be trusted (not UTF-8 JSON, not an object, or any entry invalid). Nothing may
 * then be decided from the manifest or written over it. */
static inline NSMutableDictionary<NSString *, NSMutableDictionary *> *_Nullable PorticoDownloadManifestEntries(NSData *_Nullable raw) {
  NSMutableDictionary<NSString *, NSMutableDictionary *> *entries = [NSMutableDictionary new];
  if (!raw) return entries;
  NSString *text = [[NSString alloc] initWithData:raw encoding:NSUTF8StringEncoding];
  NSDictionary *stored = text ? [NSJSONSerialization JSONObjectWithData:raw options:NSJSONReadingMutableContainers error:nil] : nil;
  if (![stored isKindOfClass:NSDictionary.class]) return nil;
  for (NSString *key in stored) {
    if (!PorticoDownloadValidEntry(key, stored[key])) return nil;
    entries[key] = [stored[key] mutableCopy];
  }
  return entries;
}

NS_ASSUME_NONNULL_END
