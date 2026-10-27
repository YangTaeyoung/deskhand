#include "DeskhandKeychain.h"
#include <pthread.h>

static pthread_mutex_t interaction_lock = PTHREAD_MUTEX_INITIALIZER;

// LAContext controls the data-protection keychain. The app also has existing
// file-keychain items; those need the legacy interaction switch. Keep its
// deprecated use here, scoped to a single operation, with the prior state
// restored before another operation may enter.
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
static OSStatus begin(Boolean *previous) {
    pthread_mutex_lock(&interaction_lock);
    OSStatus status = SecKeychainGetUserInteractionAllowed(previous);
    if (status == errSecSuccess) status = SecKeychainSetUserInteractionAllowed(false);
    if (status != errSecSuccess) pthread_mutex_unlock(&interaction_lock);
    return status;
}

static OSStatus end(Boolean previous, OSStatus status) {
    OSStatus restored = SecKeychainSetUserInteractionAllowed(previous);
    pthread_mutex_unlock(&interaction_lock);
    return status == errSecSuccess ? restored : status;
}
#pragma clang diagnostic pop

OSStatus DeskhandKeychainCopyMatching(CFDictionaryRef query, CFTypeRef *result) {
    Boolean previous = false;
    OSStatus status = begin(&previous);
    if (status != errSecSuccess) return status;
    return end(previous, SecItemCopyMatching(query, result));
}

OSStatus DeskhandKeychainAdd(CFDictionaryRef attributes) {
    Boolean previous = false;
    OSStatus status = begin(&previous);
    if (status != errSecSuccess) return status;
    return end(previous, SecItemAdd(attributes, NULL));
}

OSStatus DeskhandKeychainUpdate(CFDictionaryRef query, CFDictionaryRef attributes) {
    Boolean previous = false;
    OSStatus status = begin(&previous);
    if (status != errSecSuccess) return status;
    return end(previous, SecItemUpdate(query, attributes));
}

OSStatus DeskhandKeychainDelete(CFDictionaryRef query) {
    Boolean previous = false;
    OSStatus status = begin(&previous);
    if (status != errSecSuccess) return status;
    return end(previous, SecItemDelete(query));
}
