#!/usr/bin/env python3
"""Prove stable Cargo keeps host links separate from explicit target Rust flags."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import shlex
import shutil
import subprocess


def check(work, linker):
    if platform.system() != 'Linux' or platform.machine() != 'x86_64':
        raise ValueError('native Linux x64 linker control required')
    command = shlex.split(linker)
    if len(command) != 1 or not shutil.which(command[0]):
        raise ValueError('one real target linker executable required')
    real_target = Path(shutil.which(command[0])).resolve(strict=True)
    work.mkdir()
    project = work / 'project'; project.mkdir()
    (project / 'Cargo.toml').write_text('[workspace]\nmembers=["app","macros"]\nresolver="2"\n')
    for name in ('app', 'macros'):
        (project / name / 'src').mkdir(parents=True)
    (project / 'app/Cargo.toml').write_text('[package]\nname="portico_linker_probe"\nversion="0.0.0"\nedition="2021"\n'
        '[dependencies]\nprobe_macros={path="../macros"}\n')
    (project / 'app/build.rs').write_text('fn main(){println!("cargo:rustc-env=PROBE_HOST=checked");}\n')
    (project / 'app/src/main.rs').write_text('fn main(){assert_eq!(env!("PROBE_HOST"),"checked");println!("{}",probe_macros::answer!());}\n')
    (project / 'macros/Cargo.toml').write_text('[package]\nname="probe_macros"\nversion="0.0.0"\nedition="2021"\n[lib]\nproc-macro=true\n')
    (project / 'macros/src/lib.rs').write_text('extern crate proc_macro; use proc_macro::TokenStream; '
        '#[proc_macro] pub fn answer(_:TokenStream)->TokenStream{"7".parse().unwrap()}\n')
    (project / 'Cargo.lock').write_text('version=3\n[[package]]\nname="portico_linker_probe"\nversion="0.0.0"\n'
        'dependencies=["probe_macros"]\n[[package]]\nname="probe_macros"\nversion="0.0.0"\n')
    logger = work / 'linker.py'
    logger.write_text('import json,os,pathlib,sys\n'
        'scope,compiler,*args=sys.argv[1:]\n'
        'with pathlib.Path(__file__).with_name("links.jsonl").open("a") as out:out.write(json.dumps({"scope":scope,"args":args})+"\\n")\n'
        'os.execv(compiler,[compiler,*args])\n')
    host = work / 'host-bin'; host.mkdir()
    target = work / 'target-linker'
    for path, scope, compiler in [(host / 'cc', 'host', Path('/usr/bin/cc')),
                                  (host / 'gcc', 'host', Path('/usr/bin/cc')),
                                  (target, 'target', real_target)]:
        path.write_text('#!/bin/sh\nexec python3 ' + shlex.quote(str(logger)) + ' ' + scope + ' ' +
                        shlex.quote(str(compiler)) + ' "$@"\n'); path.chmod(0o755)
    env = dict(os.environ, PATH=str(host) + os.pathsep + os.environ['PATH'],
               CARGO_HOME=str(work / 'cargo-home'), CARGO_TARGET_DIR=str(work / 'target'),
               CARGO_BUILD_JOBS='2', CARGO_INCREMENTAL='0', RUSTFLAGS='-Clinker=' + str(target))
    for key in list(env):
        if key in ('RUSTC', 'CARGO_BUILD_TARGET', 'CARGO_ENCODED_RUSTFLAGS', 'RUSTC_WRAPPER', 'RUSTC_WORKSPACE_WRAPPER', 'RUSTUP_TOOLCHAIN') or (
                key.startswith('CARGO_TARGET_') and key.endswith('_LINKER')):
            env.pop(key)
    subprocess.run(['cargo', 'build', '--locked', '--offline', '--target', 'x86_64-unknown-linux-gnu', '-j2'],
                   cwd=project, env=env, check=True)
    binary = work / 'target/x86_64-unknown-linux-gnu/debug/portico_linker_probe'
    if subprocess.check_output([str(binary)], text=True).strip() != '7':
        raise ValueError('real stable Rust probe did not execute')
    rows = [json.loads(line) for line in (work / 'links.jsonl').read_text().splitlines()]
    if not any(row['scope'] == 'host' for row in rows) or not any(row['scope'] == 'target' for row in rows):
        raise ValueError('both real host and target linker invocations required')
    for row in rows:
        args = row['args']
        if '-o' not in args:
            raise ValueError('linker output identity missing')
        output = Path(args[args.index('-o') + 1]).resolve()
        expected = work / ('target/debug' if row['scope'] == 'host' else 'target/x86_64-unknown-linux-gnu/debug')
        if not output.is_relative_to(expected):
            raise ValueError('stable Cargo mixed host and target linker output: ' + str(output))
    record = {'passed': True, 'hostLinks': sum(row['scope'] == 'host' for row in rows),
              'targetLinks': sum(row['scope'] == 'target' for row in rows), 'targetLinker': str(real_target),
              'rustc': subprocess.check_output(['rustc', '-vV'], text=True).strip(),
              'executableSHA256': hashlib.sha256(binary.read_bytes()).hexdigest(),
              'scope': 'Real offline build script/proc-macro/executable links; not an engine qualification'}
    (work.parent / 'rust-linker-control.json').write_text(json.dumps(record, indent=2) + '\n')
    return record


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--work', type=Path, required=True)
    parser.add_argument('--linker', required=True)
    args = parser.parse_args()
    print(json.dumps(check(args.work.resolve(), args.linker)))
