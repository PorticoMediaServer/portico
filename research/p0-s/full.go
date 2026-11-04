// Temporary full-DDL connection and schema-memory companion. Never product code.
package main
import (
 "context"
 "crypto/sha256"
 "database/sql"
 "encoding/hex"
 "encoding/json"
 "flag"
 "fmt"
 "os"
 "os/exec"
 "path/filepath"
 "runtime"
 "sort"
 "strconv"
 "strings"
 "time"
 _ "github.com/PorticoMediaServer/portico/server/internal/dbwork"
 sqlite "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)
func must(e error){if e!=nil{panic(e)}}
func save(p string,v any){b,e:=json.MarshalIndent(v,"","  ");must(e);must(os.WriteFile(p+".tmp",b,0600));must(os.Rename(p+".tmp",p))}
func open(path string)(*sql.DB,*sql.Conn){
 db,e:=sql.Open("sqlite",path);must(e);db.SetMaxOpenConns(1)
 c,e:=db.Conn(context.Background());must(e)
 _,e=c.ExecContext(context.Background(),"PRAGMA busy_timeout=1000; PRAGMA foreign_keys=ON; PRAGMA synchronous=FULL; PRAGMA cache_size=-16384; PRAGMA temp_store=MEMORY;");must(e)
 r,e:=c.QueryContext(context.Background(),"SELECT id,title FROM catalog_entities LIMIT 40");must(e)
 for r.Next(){var id int64;var title string;must(r.Scan(&id,&title))};must(r.Err());must(r.Close())
 return db,c
}
func memory(c *sql.Conn)map[string]int{
 out:=map[string]int{};must(c.Raw(func(dc any)error{
  st,ok:=dc.(sqlite.DBStatus);if !ok{return fmt.Errorf("bundled driver has no DBStatus")}
  for n,op:=range map[string]sqlite.DBStatusOp{"schema":sqlite.DBStatusSchemaUsed,"statement":sqlite.DBStatusStmtUsed,"pager":sqlite.DBStatusCacheUsed}{v,_,e:=st.Status(op,false);if e!=nil{return e};out[n]=v};return nil
 }));return out
}
func rss()int64{b,e:=os.ReadFile("/proc/self/status");must(e);for _,l:=range strings.Split(string(b),"\n"){if strings.HasPrefix(l,"VmRSS:"){v,e:=strconv.ParseInt(strings.Fields(l)[1],10,64);must(e);return v*1024}};return -1}
func pct(a []float64,q float64)float64{b:=append([]float64{},a...);sort.Float64s(b);return b[int(float64(len(b)-1)*q)]}
func main(){
 ddl:=flag.String("ddl","","exact integrated DDL");dbpath:=flag.String("db","","disposable db");out:=flag.String("out","","receipts");child:=flag.Bool("child",false,"100 physical opens");flag.Parse();must(os.MkdirAll(*out,0700))
 if *child{
  xs:=[]float64{};for j:=0;j<100;j++{t:=time.Now();db,c:=open(*dbpath);xs=append(xs,float64(time.Since(t).Nanoseconds())/1e6);must(c.Close());must(db.Close())}
  save(filepath.Join(*out,fmt.Sprintf("bundled-opens-%d.json",os.Getpid())),map[string]any{"samples_ms":xs,"n":100,"p50_ms":pct(xs,.5),"p95_ms":pct(xs,.95),"max_ms":pct(xs,1)});return
 }
 body,e:=os.ReadFile(*ddl);must(e);sum:=sha256.Sum256(body)
 if _,e=os.Stat(*dbpath);os.IsNotExist(e){
  db,e:=sql.Open("sqlite",*dbpath);must(e);_,e=db.Exec("PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; BEGIN;"+string(body)+" COMMIT;");must(e);must(db.Close())
 }
 db,c:=open(*dbpath);var tables,entities int64;var version string
 must(c.QueryRowContext(context.Background(),"SELECT count(*) FROM sqlite_schema WHERE type='table'").Scan(&tables));must(c.QueryRowContext(context.Background(),"SELECT count(*) FROM catalog_entities").Scan(&entities));must(c.QueryRowContext(context.Background(),"SELECT sqlite_version()").Scan(&version))
 save(filepath.Join(*out,"full-manifest.json"),map[string]any{"ddl_sha256":hex.EncodeToString(sum[:]),"tables":tables,"entities":entities,"sqlite_version":version,"go":runtime.Version(),"boundary":"Exact complete DDL; empty-domain fixture unless separately populated. No production statement cache or typed write API."})
 must(c.Close());must(db.Close())
 for j:=0;j<3;j++{cmd:=exec.Command(os.Args[0],"--db",*dbpath,"--out",*out,"--child");cmd.Stdout=os.Stdout;cmd.Stderr=os.Stderr;must(cmd.Run())}
 type held struct{d *sql.DB;c *sql.Conn};connections:=[]held{};pools:=[]any{}
 for _,n:=range []int{0,1,8,16,32}{
  for len(connections)<n{d,c:=open(*dbpath);connections=append(connections,held{d,c})}
  runtime.GC();var gm runtime.MemStats;runtime.ReadMemStats(&gm);native:=[]any{};for _,h:=range connections{native=append(native,memory(h.c))}
  pools=append(pools,map[string]any{"connections":n,"rss_bytes":rss(),"go_heap_alloc":gm.HeapAlloc,"go_sys":gm.Sys,"native":native})
 }
 save(filepath.Join(*out,"bundled-memory.json"),pools)
 for _,h:=range connections{must(h.c.Close());must(h.d.Close())}
}
