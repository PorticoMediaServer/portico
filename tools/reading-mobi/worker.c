/* SPDX-License-Identifier: LGPL-3.0-or-later
 * Portico's fixed, unencrypted-only adapter to the pinned LGPL libmobi tool.
 * This source does not implement or expose DRM/key extraction/decryption.
 * Release builds the pinned tools/mobitool.c with main renamed to
 * portico_mobitool_main and encryption compiled out; see DEPENDENCY.md.
 */
#include <mobi.h>
#include <sys/resource.h>
#include <sys/stat.h>
#include <fcntl.h>
#include <unistd.h>
#include <stdio.h>
#include <stdint.h>
#include <string.h>
#include <errno.h>

extern int portico_mobitool_main(int argc, char **argv);
static int limit(int resource, rlim_t value) {
    struct rlimit bound = {value, value};
    return setrlimit(resource, &bound);
}
int main(void) {
    /* Installed before libmobi sees even a header. No unbounded fallback. */
    if (limit(RLIMIT_CORE, 0) || limit(RLIMIT_AS, 768ULL << 20) ||
        limit(RLIMIT_CPU, 30) || limit(RLIMIT_FSIZE, 128ULL << 20) ||
        limit(RLIMIT_NOFILE, 32) || limit(RLIMIT_NPROC, 0)) return 70;
    /* Capture the publication sink, then discard ALL parser diagnostics,
     * including header preflight before entering the upstream CLI. */
    int publication = dup(STDOUT_FILENO);
    int nullfd = open("/dev/null", O_WRONLY);
    if (publication < 0 || nullfd < 0 || dup2(nullfd, STDOUT_FILENO) < 0 ||
        dup2(nullfd, STDERR_FILENO) < 0) return 73;
    close(nullfd);
    FILE *input = fopen("/input/book.mobi", "rb");
    if (!input) return 71;
    MOBIData *book = mobi_init();
    if (!book) { fclose(input); return 71; }
    MOBI_RET result = mobi_load_file(book, input);
    fclose(input);
    /* Hybrid KF7/KF8 parts both have to be unencrypted. Accept the library
     * two-part ring only; reject unexpected linked parts rather than recovery. */
    const MOBIData *part = book;
    unsigned parts = 0;
    int allowed = result == MOBI_SUCCESS;
    while (allowed && part) {
        if (++parts > 2 || !part->rh || mobi_is_encrypted(part) ||
            part->rh->encryption_type != 0 ||
            (part->mh && part->mh->drm_count && *part->mh->drm_count != 0) ||
            mobi_is_replica(part)) { allowed = 0; break; }
        part = part->next;
        /* libmobi represents a normal hybrid as an exact two-part ring. */
        if (part == book) { if (parts != 2) allowed = 0; break; }
    }
    mobi_free(book);
    if (!allowed) return 72;
    char *args[] = {"portico-mobi", "-e", "-o", "/output", "/input/book.mobi", NULL};
    if (portico_mobitool_main(5, args) != 0) return 74;
    fflush(NULL);
    int output = open("/output/book.epub", O_RDONLY | O_NOFOLLOW);
    struct stat info;
    if (output < 0 || fstat(output, &info) || !S_ISREG(info.st_mode) ||
        info.st_size <= 0 || info.st_size > (128LL << 20)) return 75;
    unsigned char buffer[65536];
    for (;;) {
        ssize_t n = read(output, buffer, sizeof buffer);
        if (n < 0) { if (errno == EINTR) continue; return 76; }
        if (!n) break;
        ssize_t offset = 0;
        while (offset < n) {
            ssize_t written = write(publication, buffer + offset, (size_t)(n - offset));
            if (written < 0 && errno == EINTR) continue;
            if (written <= 0) return 76;
            offset += written;
        }
    }
    close(output);close(publication);
    return 0;
}
