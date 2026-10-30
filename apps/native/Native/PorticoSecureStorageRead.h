#import <Foundation/Foundation.h>
#import <Security/Security.h>

/** Preserve missing-versus-unreadable across the bridge. Never repair or overwrite stored bytes.
 * JSON/schema validation remains with the credential/recovery consumers. Kept inline so the
 * exact native read boundary can be fault-tested without accessing a user's Keychain.
 */
static inline void PorticoResolveSecureStorageRead(OSStatus status, id result, id missingValue,
    NSString *failureMessage, void (^resolve)(id), void (^reject)(NSString *, NSString *, NSError *)) {
  if (status == errSecItemNotFound) { resolve(missingValue); return; }
  if (status != errSecSuccess || ![result isKindOfClass:NSData.class]) {
    reject(@"secure_storage", failureMessage, nil); return;
  }
  NSString *text = [[NSString alloc] initWithData:result encoding:NSUTF8StringEncoding];
  if (!text) { reject(@"secure_storage", failureMessage, nil); return; }
  resolve(text);
}
