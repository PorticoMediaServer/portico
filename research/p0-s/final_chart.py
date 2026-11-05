import pathlib,json,re
import matplotlib
matplotlib.use('svg')
import matplotlib.pyplot as plt
E=pathlib.Path('/home/justin/projects/Portico/portico-internal/planning/v3/schema-v1/phase0/S-evidence')
d=json.load(open(E/'S-OPEN-291-summary.json'))
plt.rcParams.update({'svg.fonttype':'none','font.family':'DejaVu Sans','font.size':10,'text.color':'#111827','axes.labelcolor':'#64748b','xtick.color':'#64748b','ytick.color':'#111827','axes.edgecolor':'#cbd5e1','axes.linewidth':.6})
fig,axes=plt.subplots(1,3,figsize=(8.6,2.35),gridspec_kw={'width_ratios':[1.2,1.2,1]})
for i,(profile,budget,title) in enumerate([('standard',21,'Standard open'),('legacy8',26,'Legacy8 open')]):
 ax=axes[i];values=[]
 for schema in ['415889','291ab3']:
  values.append(max(p['p95_ms'] for x in d['results'] if x['profile']==profile and x['schema']==schema for p in x['processes']))
 ax.barh([1,0],values,height=.48,color=['#9ca3af','#cf5c50'])
 ax.axvline(budget,color='#24815b',linestyle='--',linewidth=1.2)
 for y,v in zip([1,0],values):ax.text(v+.5,y,f'{v:.2f}',va='center',fontsize=10)
 ax.set(yticks=[1,0],yticklabels=['4158','291a'],xlim=(0,40),ylim=(-.55,1.55),xticks=[0,10,20,30,40],xlabel='p95 milliseconds')
 ax.set_title(title+f' · ≤{budget} ms',fontsize=11,pad=10)
ax=axes[2];values=[next(x['schema_bytes_per_connection']/2**20 for x in d['results'] if x['schema']==schema) for schema in ['415889','291ab3']]
ax.barh([1,0],values,height=.48,color=['#9ca3af','#24815b']);ax.axvline(5,color='#24815b',linestyle='--',linewidth=1.2)
for y,v in zip([1,0],values):ax.text(v-.12,y,f'{v:.3f}',va='center',ha='right',fontsize=10)
ax.set(yticks=[1,0],yticklabels=['4158','291a'],xlim=(0,6.4),ylim=(-.55,1.55),xticks=[0,2,4,6],xlabel='MiB per connection');ax.set_title('Schema heap · ≤5 MiB',fontsize=11,pad=10)
for ax in axes:
 ax.spines[['top','right','left']].set_visible(False);ax.tick_params(axis='y',length=0);ax.tick_params(axis='x',length=3)
fig.tight_layout(pad=.6,w_pad=1.3)
p=E/'S-OPEN-291.svg';fig.savefig(p,transparent=True,metadata={'Date':None,'Creator':'Portico scale research'})
s=p.read_text();s=s[s.index('<svg'):]
for color,var in [('#111827','--foreground'),('#64748b','--muted-foreground'),('#cbd5e1','--border'),('#9ca3af','--muted-foreground'),('#cf5c50','--destructive'),('#24815b','--success')]:s=s.replace(color,f'var({var},{color})')
p.write_text(s)
html='''<!doctype html><html><head><meta charset="utf-8"><style>:root{font-family:var(--font-sans)}body{margin:0;color:var(--foreground)}svg{display:block;width:100%;min-width:650px;height:auto;flex:0 0 auto}p{font-size:12px;line-height:1.5;color:var(--muted-foreground);margin:8px 0 0} .plot{width:100%;height:220px;display:flex;align-items:center;overflow-x:auto}</style></head><body><div class="plot">'''+s+'''</div><p>Open: largest process p95; 3 VMs/profile × 3 fresh processes × 100 physical opens. Complete DDL, required PRAGMAs and a 40-row read. Dashed lines show the accepted limits. Run 41939826702; bundled SQLite 3.53.4.</p></body></html>'''
(E/'S-FINAL-GATES.html').write_text(html)
print(p)
