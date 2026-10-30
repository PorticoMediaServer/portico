#!/usr/bin/env python3
"""Re-sync the schema digest, known-object list and known-column list after an installer change."""
import re,subprocess,pathlib,sys
root=pathlib.Path(__file__).resolve().parent.parent/'server'
p=root/'internal/persistence'
for _ in range(3):
    # Run the actual metadata producers here; broader schema/SQL guards run
    # separately, rather than repeating on every convergence pass.
    producers='^(TestSchemaBaselineDigest|TestKnownSchemaObjectsMatchAFreshInstall|TestKnownSchemaColumnsMatchAFreshInstall|TestSubsystemSchemaObjectsMatchTheSources)$'
    result=subprocess.run(['go','test','./internal/persistence/','-count=1','-run',producers],cwd=root,capture_output=True,text=True)
    out=result.stdout+result.stderr
    print(out,end='',flush=True)
    changed=False
    m=re.search(r'set schemaSourceDigest in this file to:\s+([0-9a-f]{64})',out)
    if m:
        t=(p/'schema_test.go');s=t.read_text();s=re.sub(r'const schemaSourceDigest = "[0-9a-f]{64}"','const schemaSourceDigest = "%s"'%m.group(1),s);t.write_text(s);changed=True
    m=re.search(r'with the contents of:\s+(\S+known_schema.txt)',out)
    if m:
        new=pathlib.Path(m.group(1)).read_text().rstrip('\n')
        t=(p/'known_schema.go');s=t.read_text();s=re.sub(r'const knownSchemaObjects = `[^`]*`','const knownSchemaObjects = `'+new+'`',s,count=1);t.write_text(s);changed=True
    m=re.search(r'with the contents of:\s+(\S+known_columns.txt)',out)
    if m:
        new=pathlib.Path(m.group(1)).read_text().rstrip('\n')
        t=(p/'known_columns.go');s=t.read_text();s=re.sub(r'const knownSchemaColumns = `[^`]*`',lambda _:'const knownSchemaColumns = `\n'+new+'\n`',s,count=1);t.write_text(s);changed=True
    m=re.search(r'with the contents of:\s+(\S+subsystem_schema.txt)',out)
    if m:
        new=pathlib.Path(m.group(1)).read_text().rstrip('\n')
        t=(p/'known_schema.go');s=t.read_text();s=re.sub(r'const subsystemSchemaObjects = `[^`]*`',lambda _:'const subsystemSchemaObjects = `'+new+'`',s,count=1);t.write_text(s);changed=True
    if not changed:
        sys.exit(result.returncode)
sys.exit('Schema metadata did not converge after three actual generation passes')
