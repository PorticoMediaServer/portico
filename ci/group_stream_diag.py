from pathlib import Path
import json,os,subprocess
from run import go
plan=json.loads(Path(os.environ['RECORDED_PLAN']).read_text())
p=Path('server/internal/httpapi/social_routes.go')
baseline=p.read_text()
fixed=Path('ci/group_stream_fixed.go.txt').read_text()
for variant,source in [('A',baseline),('B',fixed)]:
    source=source.replace('"encoding/json"','"encoding/json"\n"fmt"',1)
    marker='\twrite(w, 500, map[string]any{"protocolVersion": social.Protocol'
    assert source.count(marker)==1
    source=source.replace(marker,'\tfmt.Printf("GROUP-FALLBACK err=%T:%v\\n",e,e)\n'+marker,1)
    p.write_text(source)
    out=Path('group-stream-'+variant);out.mkdir()
    focused=subprocess.run(['go','test','-json','-p','1','-count=1','-run','^TestGroupStream(AuthenticationHonorsRequestEnd|InitialReadHonorsRequestEnd|LiveInputErrorsKeepTheirStatus|RequiresTheOriginalWriterToFlush)$','./internal/httpapi'],cwd='server',capture_output=True,text=True)
    (out/'controls.jsonl').write_text(focused.stdout)
    (out/'controls.stderr').write_text(focused.stderr)
    print('CONTROLS',variant,focused.returncode,flush=True)
    try: go(plan,8)
    except SystemExit as e: print('COHORT',variant,e.code,flush=True)
    Path('ci-results').rename(out/'cohort')
