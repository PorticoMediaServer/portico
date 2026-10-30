/* SPDX-License-Identifier: GPL-3.0-or-later */
#include "../mpegts_dovi.h"
#include <stdio.h>
#include <stdlib.h>
#define CHECK(x) do { if (!(x)) { fprintf(stderr, "check failed line %d: %s\n", __LINE__, #x); exit(1); } } while (0)
#define BL 0x1011
#define EL 0x1015
#define PMT 0x100

typedef struct { uint8_t data[64 * 188]; size_t size; unsigned cc[PD_PID_MAX]; } Writer;
static void u16(uint8_t *p, unsigned value) { p[0] = (uint8_t)(value >> 8); p[1] = (uint8_t)value; }
static size_t finish(uint8_t *p, size_t size) {
    u16(p + 1, 0xb000 | (unsigned)(size + 1));
    uint32_t crc = pd_crc(p, size);
    p[size] = (uint8_t)(crc >> 24); p[size+1] = (uint8_t)(crc >> 16);
    p[size+2] = (uint8_t)(crc >> 8); p[size+3] = (uint8_t)crc;
    return size + 4;
}
static void packet(Writer *w, unsigned pid, int start, const uint8_t *payload, size_t size, int discontinuity) {
    CHECK(size > 0 && size <= 184 && w->size + 188 <= sizeof(w->data));
    uint8_t *p = w->data + w->size;
    memset(p, 0xff, 188);
    p[0] = 0x47; p[1] = (uint8_t)((pid >> 8) | (start ? 0x40 : 0)); p[2] = (uint8_t)pid;
    p[3] = (uint8_t)(0x10 | (w->cc[pid]++ & 15));
    size_t at = 4;
    if (size < 184) {
        p[3] |= 0x20;
        p[4] = (uint8_t)(183 - size);
        if (p[4]) p[5] = discontinuity ? 0x80 : 0;
        at += p[4] + 1;
    }
    memcpy(p + at, payload, size); w->size += 188;
}
static void table(Writer *w, unsigned pid, const uint8_t *section, size_t size, size_t chunk) {
    uint8_t payload[184];
    int first = 1;
    while (size) {
        size_t take = chunk < size ? chunk : size;
        CHECK(take > 0 && take + first <= sizeof(payload));
        if (first) payload[0] = 0;
        memcpy(payload + first, section, take);
        packet(w, pid, first, payload, take + first, 0);
        section += take; size -= take; first = 0;
    }
}
static void pat(Writer *w) {
    uint8_t p[16] = {0,0,0,0,1,0xc1,0,0,0,1,0xe1,0};
    table(w, 0, p, finish(p, 12), 7);
}
static size_t pmt(uint8_t *p, int private_pes, int registration, int hevc, unsigned dependency) {
    memset(p, 0, 512);
    p[0] = 2; p[4] = 1; p[5] = 0xc1; u16(p + 8, 0xe000 | BL); u16(p + 10, 0xf000);
    size_t at = 12;
    p[at] = 0x24; u16(p + at + 1, 0xe000 | BL); u16(p + at + 3, 0xf000); at += 5;
    p[at] = private_pes ? 0x06 : 0x24; u16(p + at + 1, 0xe000 | EL);
    size_t length = at + 3; at += 5; size_t start = at;
    if (registration) { memcpy(p + at, "\x05\x04" "DOVI", 6); at += 6; }
    if (hevc) { p[at++] = 0x38; p[at++] = 13; memset(p + at, 0, 13); at += 13; }
    p[at++] = 0xb0; p[at++] = 7; p[at++] = 1; p[at++] = 0;
    u16(p + at, (7 << 9) | (1 << 3) | 6); at += 2;
    u16(p + at, dependency << 3); at += 2; p[at++] = 0x60;
    u16(p + length, 0xf000 | (unsigned)(at - start));
    return finish(p, at);
}
static void expect(const Writer *w, unsigned base, int malformed) {
    PD_DoviFacts facts;
    pd_dovi_probe(w->data, w->size, 188, &facts);
    CHECK(facts.base[EL] == base && !facts.base[BL] && !!facts.malformed == malformed);
}
static void selftests(void) {
    uint8_t section[512];
    unsigned count = 0;
    for (int private_pes = 0; private_pes < 2; private_pes++) {
        Writer w = {0}; pat(&w);
        size_t size = pmt(section, private_pes, 1, 1, BL);
        table(&w, PMT, section, size, 11);
        expect(&w, BL, 0); count++;
        uint8_t m2ts[64 * 192];
        for (size_t i = 0; i < w.size / 188; i++) {
            memset(m2ts + i * 192, 0, 4); memcpy(m2ts + i * 192 + 4, w.data + i * 188, 188);
        }
        PD_DoviFacts facts;
        pd_dovi_probe(m2ts, w.size / 188 * 192, 192, &facts);
        CHECK(facts.base[EL] == BL && !facts.malformed); count++;
    }
    for (int missing = 0; missing < 2; missing++) {
        Writer w = {0}; pat(&w);
        size_t size = pmt(section, 1, missing != 0, missing == 0, BL);
        table(&w, PMT, section, size, 40); expect(&w, 0, 0); count++;
    }
    for (int bad = 0; bad < 6; bad++) {
        Writer w = {0}; pat(&w);
        size_t size = pmt(section, 0, 0, 0, BL);
        if (bad == 0) section[size - 1] ^= 1; // Bad CRC.
        if (bad == 1) { section[4] = 2; size = finish(section, size - 4); } // Wrong program.
        if (bad == 2) { section[5] &= 0xfe; size = finish(section, size - 4); } // Not current.
        if (bad == 3) { section[24] = 2; size = finish(section, size - 4); } // Unsupported DOVI version.
        if (bad == 4) { size = pmt(section, 0, 0, 0, EL); } // Self dependency.
        if (bad == 5) { size = pmt(section, 0, 0, 0, BL + 1); } // Absent base PID.
        table(&w, PMT, section, size, 8); expect(&w, 0, 0); count++;
    }
    // A wrong-program table is ignored, without poisoning the next real PMT.
    {
        Writer w = {0}; pat(&w); size_t size = pmt(section, 0, 0, 0, BL);
        section[4] = 2; size = finish(section, size - 4); table(&w, PMT, section, size, 8);
        section[4] = 1; size = finish(section, size - 4); table(&w, PMT, section, size, 8);
        expect(&w, BL, 0); count++;
    }
    // Complete the first PMT using pointer bytes, then start the next section.
    {
        Writer w = {0}; pat(&w); size_t size = pmt(section, 0, 0, 0, BL);
        uint8_t payload[184] = {0}; memcpy(payload + 1, section, 13);
        packet(&w, PMT, 1, payload, 14, 0);
        payload[0] = (uint8_t)(size - 13); memcpy(payload + 1, section + 13, size - 13);
        memcpy(payload + 1 + size - 13, section, size);
        packet(&w, PMT, 1, payload, 1 + size - 13 + size, 0);
        expect(&w, BL, 0); count++;
    }
    // Missing continuation, transport discontinuity, or a pointer overflow
    // cannot synthesize a PMT from unrelated halves.
    for (int fault = 0; fault < 3; fault++) {
        Writer w = {0}; pat(&w); size_t size = pmt(section, 0, 0, 0, BL);
        uint8_t payload[184] = {0}; memcpy(payload + 1, section, 13);
        packet(&w, PMT, 1, payload, 14, 0);
        if (fault == 0) w.cc[PMT]++;
        memcpy(payload, section + 13, size - 13);
        if (fault == 2) payload[0] = 255;
        packet(&w, PMT, fault == 2, payload, size - 13, fault == 1);
        expect(&w, 0, 0); count++;
    }
    // Repetition and a version-only PMT update are harmless. A real dependency
    // change, even to another absent PID, invalidates all observed links.
    for (int changed = 0; changed < 2; changed++) {
        Writer w = {0}; pat(&w); size_t size = pmt(section, 0, 0, 0, BL);
        table(&w, PMT, section, size, 8); table(&w, PMT, section, size, 8);
        size = pmt(section, 0, 0, 0, changed ? BL + 1 : BL);
        section[5] = 0xc3; size = finish(section, size - 4);
        table(&w, PMT, section, size, 8);
        expect(&w, changed ? 0 : BL, changed); count++;
    }
    printf("{\"passed\":true,\"psiCases\":%u}\n", count);
}
int main(int argc, char **argv) {
    if (argc == 1) { selftests(); return 0; }
    CHECK(argc == 4);
    FILE *file = fopen(argv[1], "rb"); CHECK(file);
    uint8_t *data = malloc(1048577); CHECK(data);
    size_t size = fread(data, 1, 1048577, file);
    CHECK(size > 0 && size <= 1048576 && feof(file)); CHECK(!fclose(file));
    PD_DoviFacts facts;
    pd_dovi_probe(data, size, (unsigned)atoi(argv[2]), &facts);
    unsigned expected = (unsigned)atoi(argv[3]);
    CHECK(facts.base[EL] == expected && !facts.base[BL]);
    printf("{\"passed\":true,\"basePid\":%u,\"malformed\":%s}\n", expected, facts.malformed ? "true" : "false");
    free(data); return 0;
}
