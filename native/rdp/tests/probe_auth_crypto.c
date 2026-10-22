/* Offline hardened-runtime regression: no network, no real credentials. */
#include <dlfcn.h>
#include <stdio.h>
#include <stdint.h>
#include <limits.h>
#include <string.h>
typedef void* (*new_fn)(const char*, uint16_t, const char*, const char*, const char*, const char*, uint32_t, uint32_t);
int main(int argc, char** argv) {
    if (argc != 2) return 2;
    char path[PATH_MAX];
    snprintf(path, sizeof(path), "%s/libDeskhandRDPEngine.dylib", argv[1]);
    void* engine = dlopen(path, RTLD_NOW | RTLD_LOCAL);
    if (!engine) { fprintf(stderr, "%s\n", dlerror()); return 3; }
    new_fn create = (new_fn)dlsym(engine, "dh_rdp_new");
    void (*destroy)(void*) = dlsym(engine, "dh_rdp_free");
    if (!create || !destroy) return 4;
    void* session = create("localhost", 3389, "offline-probe", "", "",
        "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 200, 200);
    if (!session) return 5;
    snprintf(path, sizeof(path), "%s/libcrypto.3.dylib", argv[1]);
    void* crypto = dlopen(path, RTLD_NOW | RTLD_LOCAL);
    if (!crypto) return 6;
    void* (*load)(void*, const char*) = dlsym(crypto, "OSSL_PROVIDER_load");
    void* (*fetch)(void*, const char*, const char*) = dlsym(crypto, "EVP_MD_fetch");
    void* (*cipher)(void*, const char*, const char*) = dlsym(crypto, "EVP_CIPHER_fetch");
    void (*free_md)(void*) = dlsym(crypto, "EVP_MD_free");
    void (*free_cipher)(void*) = dlsym(crypto, "EVP_CIPHER_free");
    void (*errors)(FILE*) = dlsym(crypto, "ERR_print_errors_fp");
    if (!load || !fetch || !cipher || !free_md || !free_cipher || !errors) return 7;
    void* legacy = load(NULL, "legacy");
    void* md4 = fetch(NULL, "MD4", NULL);
    void* rc4 = cipher(NULL, "RC4", NULL);
    printf("bundled_legacy=%s NTLM_MD4=%s NTLM_RC4=%s\n",
        legacy ? "PASS" : "FAIL", md4 ? "PASS" : "FAIL", rc4 ? "PASS" : "FAIL");
    if (!legacy || !md4 || !rc4) errors(stderr);
    free_md(md4); free_cipher(rc4); destroy(session);
    return legacy && md4 && rc4 ? 0 : 1;
}
