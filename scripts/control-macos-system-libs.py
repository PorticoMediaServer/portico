#!/usr/bin/env python3
"""Compile against the real selected SDK's XML/iconv with a private pkg-config path."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import shlex
import subprocess


def control(work):
    work.mkdir(parents=True, exist_ok=False)
    spec = importlib.util.spec_from_file_location('system_libs', Path(__file__).with_name('prepare-macos-system-libs.py'))
    module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
    record = module.prepare(work / 'prefix', work / 'sdk-inputs.json')
    env = dict(os.environ, PKG_CONFIG_PATH='', PKG_CONFIG_LIBDIR=str(work / 'prefix/lib/pkgconfig'))
    flags = shlex.split(subprocess.check_output(['pkg-config', '--cflags', '--libs', '--static', 'libxml-2.0'], env=env, text=True))
    assert not any('homebrew' in x or '/usr/local' in x for x in flags), flags
    source = work / 'probe.c'
    source.write_text('''#include <iconv.h>
#include <libxml/parser.h>
int main(void) {
    iconv_t converter = iconv_open("UTF-8", "UTF-8");
    if (converter == (iconv_t)-1) return 1;
    if (iconv_close(converter)) return 2;
    xmlDocPtr document = xmlReadMemory("<portico/>", 10, "probe.xml", 0, XML_PARSE_NONET);
    if (!document) return 3;
    xmlFreeDoc(document); xmlCleanupParser(); return 0;
}
''')
    binary = work / 'probe'
    subprocess.run(['/usr/bin/xcrun', 'clang', '-isysroot', record['sdk'], source, '-o', binary, *flags, '-liconv'], check=True)
    subprocess.run([binary], check=True)
    links = subprocess.check_output(['/usr/bin/otool', '-L', binary], text=True)
    assert '/usr/lib/libxml2.' in links and '/usr/lib/libiconv.' in links, links
    assert '/opt/homebrew/' not in links and '/usr/local/' not in links, links
    record.update(controlOnly=True, engineBuild=False, executed=True, pkgConfigFlags=flags, linkedLibraries=links)
    (work / 'control.json').write_text(json.dumps(record, indent=2) + '\n')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--work', type=Path, required=True)
    control(parser.parse_args().work)
