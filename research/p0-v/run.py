#!/usr/bin/env python3
"""Go overlay preserves tracked product files and existing golden bytes."""
import argparse,json,os,pathlib,subprocess
p=argparse.ArgumentParser();p.add_argument('--out',required=True);p.add_argument('--shape',choices=['tiny','smoke'],default='tiny');a=p.parse_args()
root=pathlib.Path(__file__).resolve().parents[2];out=pathlib.Path(a.out).resolve();out.mkdir(parents=True,exist_ok=True)
golden=root/'server/internal/httpapi/readpath_golden_test.go';copy=out/'golden_test.go'
copy.write_text(golden.read_text().replace('fixture.Tiny(), path, nil','fixture.'+('Smoke' if a.shape=='smoke' else 'Tiny')+'(), path, nil',1))
overlay=out/'overlay.json';overlay.write_text(json.dumps({'Replace':{str(golden):str(copy),str(root/'server/internal/httpapi/p0v_export_test.go'):str(root/'research/p0-v/export_test.go')}}))
env=dict(os.environ,TMPDIR=str(out),P0V_EXPORT=str(out/'export.json'),P0V_CHRONOLOGY=str(root/'research/p0-v/chronology.json'))
subprocess.run(['go','-C',str(root/'server'),'test','-overlay',str(overlay),'-p','1','-count=1','-timeout=15m','-v','-run','^TestP0V(Export|DVRLegacyOrderControl)$','./internal/httpapi'],env=env,check=True)
