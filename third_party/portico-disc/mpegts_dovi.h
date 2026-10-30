/* SPDX-License-Identifier: GPL-3.0-or-later
 * Bounded PSI metadata reader. A validated descriptor is a source fact, not
 * proof that RPU/pictures decode or that a target can join/render them.
 */
#ifndef PORTICO_MPEGTS_DOVI_H
#define PORTICO_MPEGTS_DOVI_H
#include <stdint.h>
#include <stddef.h>
#include <string.h>

#define PD_PID_MAX 8192
#define PD_PMT_MAX 16
#define PD_SECTION_MAX 4096
typedef struct { uint16_t base[PD_PID_MAX]; int malformed; } PD_DoviFacts;
typedef struct {
    uint8_t data[PD_SECTION_MAX];
    uint8_t seen[PD_SECTION_MAX];
    size_t used, expected;
    size_t seen_size;
    unsigned pid;
    unsigned program;
    int cc;
} PD_Section;
typedef struct {
    PD_Section pat, pmt[PD_PMT_MAX];
    unsigned pmts;
    PD_DoviFacts *facts;
} PD_Psi;

static uint32_t pd_crc(const uint8_t *data, size_t size) {
    uint32_t value = UINT32_C(0xffffffff);
    for (size_t i = 0; i < size; i++) {
        value ^= (uint32_t)data[i] << 24;
        for (unsigned b = 0; b < 8; b++)
            value = (value << 1) ^ (value & UINT32_C(0x80000000) ? UINT32_C(0x04c11db7) : 0);
    }
    return value;
}
static unsigned pd_u16(const uint8_t *p) { return ((unsigned)p[0] << 8) | p[1]; }

static void pd_table(PD_Psi *psi, PD_Section *section) {
    const uint8_t *data = section->data;
    size_t size = section->expected;
    if (size < 12 || pd_crc(data, size) || !(data[5] & 1) || data[6] || data[7]) return;
    if (section == &psi->pat) {
        if (data[0] != 0 || (size - 12) % 4) return;
    } else if (data[0] != 2 || size < 16 || pd_u16(data + 3) != section->program) return;
    if (section->seen_size && (size - 4 != section->seen_size || memcmp(data, section->seen, 5) ||
        memcmp(data + 6, section->seen + 6, size - 10) || ((data[5] ^ section->seen[5]) & 0xc1))) {
        psi->facts->malformed = 1; return;
    }
    if (!section->seen_size) { memcpy(section->seen, data, size - 4); section->seen_size = size - 4; }
    if (section == &psi->pat) {
        for (size_t i = 8; i + 4 <= size - 4; i += 4) {
            if (!pd_u16(data+i)) continue;
            unsigned pid = pd_u16(data+i+2) & 8191, found = 0;
            if (pid <= 31 || pid >= 8191) continue;
            for (unsigned j = 0; j < psi->pmts; j++) if (psi->pmt[j].pid == pid) {
                if (psi->pmt[j].program != pd_u16(data+i)) { psi->facts->malformed = 1; return; }
                found = 1;
            }
            if (!found && psi->pmts < PD_PMT_MAX) {
                PD_Section *pmt = &psi->pmt[psi->pmts++];
                pmt->pid = pid; pmt->program = pd_u16(data+i); pmt->cc = -1;
            }
        }
        return;
    }
    size_t at = 12 + (pd_u16(data + 10) & 4095);
    uint16_t pids[256], bases[256];
    uint8_t types[256];
    unsigned count = 0;
    while (at < size - 4) {
        if (at + 5 > size - 4 || count >= 256) return;
        unsigned type = data[at], pid = pd_u16(data + at + 1) & 8191;
        size_t end = at + 5 + (pd_u16(data + at + 3) & 4095);
        if (end > size - 4 || pid <= 31 || pid >= 8191) return;
        unsigned base = 0, registration = 0, hevc = 0;
        for (size_t d = at + 5; d < end;) {
            if (d + 2 > end || d + 2 + data[d+1] > end) return;
            const uint8_t *v = data + d + 2;
            unsigned length = data[d+1];
            if (data[d] == 0x05 && length >= 4 && !memcmp(v, "DOVI", 4)) registration = 1;
            if (data[d] == 0x38 && length >= 13) hevc = 1;
            if (data[d] == 0xb0 && length >= 7 && v[0] == 1 && v[1] == 0) {
                unsigned flags = pd_u16(v + 2);
                unsigned dependency = pd_u16(v + 4) >> 3;
                if ((flags >> 9) == 7 && (flags & 7) == 6 && (v[6] >> 4) == 6 &&
                    dependency > 31 && dependency < 8191 && dependency != pid) {
                    if (base && base != dependency) { psi->facts->malformed = 1; return; }
                    base = dependency;
                }
            }
            d += 2 + length;
        }
        /* Generic Dolby TS uses private PES (0x06), with both DOVI
         * registration and HEVC video descriptors. Blu-ray's explicit
         * HEVC stream type does not need that private-PES classification. */
        if (type != 0x24 && !(type == 0x06 && registration && hevc)) base = 0;
        for (unsigned i = 0; i < count; i++) if (pids[i] == pid) return;
        pids[count] = pid; types[count] = type; bases[count++] = base;
        at = end;
    }
    if (at != size - 4) return;
    for (unsigned i = 0; i < count; i++) if (bases[i]) {
        unsigned found = 0;
        for (unsigned j = 0; j < count; j++) found |= pids[j] == bases[i] && types[j] == 0x24 && !bases[j];
        if (!found) continue;
        unsigned previous = psi->facts->base[pids[i]];
        if (previous && previous != bases[i]) { psi->facts->malformed = 1; return; }
        psi->facts->base[pids[i]] = bases[i];
    }
}

static void pd_append(PD_Psi *psi, PD_Section *section, const uint8_t *data, size_t size, int new_sections) {
    while (size) {
        if (!section->used && (!new_sections || data[0] == 0xff)) return;
        size_t need = section->used < 3 ? 3 : section->expected;
        size_t take = need - section->used;
        if (take > size) take = size;
        memcpy(section->data + section->used, data, take);
        section->used += take; data += take; size -= take;
        if (section->used == 3 && !section->expected) {
            section->expected = 3 + (pd_u16(section->data + 1) & 4095);
            if (section->expected < 12 || section->expected > PD_SECTION_MAX) {
                section->used = section->expected = 0; return;
            }
        }
        if (section->expected && section->used == section->expected) {
            pd_table(psi, section);
            section->used = section->expected = 0;
        }
    }
}

static void pd_packet(PD_Psi *psi, const uint8_t *ts) {
    unsigned pid = pd_u16(ts + 1) & 8191;
    PD_Section *section = pid == 0 ? &psi->pat : NULL;
    if (!section) for (unsigned i = 0; i < psi->pmts; i++)
        if (psi->pmt[i].pid == pid) { section = &psi->pmt[i]; break; }
    if (!section) return;
    unsigned afc = (ts[3] >> 4) & 3;
    if ((ts[1] & 0x80) || (ts[3] & 0xc0) || !afc) {
        section->used = section->expected = 0; section->cc = -1; return;
    }
    size_t at = 4;
    if (afc & 2) {
        if (ts[4] > 183) return;
        if (ts[4] && (ts[5] & 0x80)) {
            section->cc = -1;
            section->used = section->expected = 0;
        }
        at += 1 + ts[4];
    }
    if (!(afc & 1) || at >= 188) return;
    unsigned cc = ts[3] & 15;
    if (section->cc == (int)cc) return;
    if (section->cc >= 0 && cc != (unsigned)((section->cc + 1) & 15))
        section->used = section->expected = 0;
    section->cc = cc;
    if (ts[1] & 0x40) {
        unsigned pointer = ts[at++];
        if (pointer > 188 - at) { section->used = section->expected = 0; return; }
        if (section->used) pd_append(psi, section, ts + at, pointer, 0);
        section->used = section->expected = 0;
        at += pointer;
        pd_append(psi, section, ts + at, 188 - at, 1);
    } else if (section->used) {
        pd_append(psi, section, ts + at, 188 - at, 0);
    }
}

static void pd_dovi_probe(const uint8_t *data, size_t size, unsigned packet_size, PD_DoviFacts *facts) {
    PD_Psi psi;
    memset(&psi, 0, sizeof(psi)); memset(facts, 0, sizeof(*facts));
    psi.pat.cc = -1; psi.facts = facts;
    if ((packet_size != 188 && packet_size != 192) || size > 1024 * 1024) return;
    for (size_t at = 0; at + packet_size <= size; at += packet_size) {
        const uint8_t *ts = data + at + (packet_size == 192 ? 4 : 0);
        if (ts[0] != 0x47) { memset(facts, 0, sizeof(*facts)); return; }
        pd_packet(&psi, ts);
        if (facts->malformed) { memset(facts->base, 0, sizeof(facts->base)); return; }
    }
}
#endif
